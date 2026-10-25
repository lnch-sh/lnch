import { DurableObject } from "cloudflare:workers";

const alphabet = "abcdefghjkmnpqrstuvwxyz23456789";
const idPattern = new RegExp(`^[${alphabet}]{8}$`);
const maxBody = 16 << 20;
const idleMs = 3 * 60 * 1000;
const pollMs = 20_000;
const publicMs = 60_000;

export type HeaderMap = Record<string, string[]>;

export type WireReq = {
  id: string;
  method: string;
  path: string;
  headers: HeaderMap;
  body: string;
};

export type WireResp = {
  status: number;
  headers: HeaderMap;
  body: string;
};

export type CallResult<T> = { ok: true; value: T } | { ok: false; status: number; error: string };

type Job = {
  id: string;
  method: string;
  path: string;
  headers: HeaderMap;
  body: string;
  done: (result: WireResp) => void;
  settled: boolean;
};

const hop = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "content-length",
]);

export function newTunnelID(): string {
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const byte of bytes) out += alphabet[byte % alphabet.length];
  return out;
}

export function newToken(): string {
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  return [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function validTunnelID(id: string): boolean {
  return idPattern.test(id);
}

export function filterHeaders(headers: Headers): HeaderMap {
  const out: HeaderMap = {};
  headers.forEach((value, key) => {
    if (hop.has(key) || key === "host") return;
    out[key] = [value];
  });
  return out;
}

export async function readBody(request: Request): Promise<Uint8Array | Response> {
  const declared = request.headers.get("content-length");
  if (declared && Number(declared) > maxBody) {
    return text("request body is too large", 413);
  }
  if (!request.body) return new Uint8Array();
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value) continue;
    total += value.byteLength;
    if (total > maxBody) {
      await reader.cancel();
      return text("request body is too large", 413);
    }
    chunks.push(value);
  }
  const body = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    body.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return body;
}

export function encodeBase64(bytes: Uint8Array): string {
  let binary = "";
  const size = 0x8000;
  for (let i = 0; i < bytes.length; i += size) {
    binary += String.fromCharCode(...bytes.subarray(i, i + size));
  }
  return btoa(binary);
}

export function decodeBase64(value: string): Uint8Array<ArrayBuffer> {
  const binary = atob(value);
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export function skipResponseHeader(key: string): boolean {
  return hop.has(key.toLowerCase());
}

export function text(message: string, status: number): Response {
  return new Response(`${message}\n`, {
    status,
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
}

function tokensMatch(got: string, want: string): boolean {
  if (!want || got.length !== want.length) return false;
  const left = new TextEncoder().encode(got);
  const right = new TextEncoder().encode(want);
  let diff = 0;
  for (let i = 0; i < left.length; i += 1) diff |= left[i] ^ right[i];
  return diff === 0;
}

function fail(status: number, error: string): CallResult<never> {
  return { ok: false, status, error };
}

// TunnelDirectory is the single registry of live tunnel ids.
// Unknown hostnames stop here, so a scan of *.lnch.sh does not create a session per name.
export class TunnelDirectory extends DurableObject<Env> {
  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      this.ctx.storage.sql.exec(`
        CREATE TABLE IF NOT EXISTS tunnels (
          id TEXT PRIMARY KEY,
          seen INTEGER NOT NULL
        )
      `);
    });
  }

  async claim(id: string): Promise<boolean> {
    const now = Date.now();
    const existing = this.ctx.storage.sql
      .exec<{ seen: number }>("SELECT seen FROM tunnels WHERE id = ?", id)
      .toArray()[0];
    if (existing && now - existing.seen < idleMs) return false;
    this.ctx.storage.sql.exec(
      "INSERT OR REPLACE INTO tunnels (id, seen) VALUES (?, ?)",
      id,
      now,
    );
    await this.ctx.storage.setAlarm(now + idleMs);
    return true;
  }

  async has(id: string): Promise<boolean> {
    const row = this.ctx.storage.sql
      .exec<{ seen: number }>("SELECT seen FROM tunnels WHERE id = ?", id)
      .toArray()[0];
    if (!row) return false;
    if (Date.now() - row.seen >= idleMs) {
      this.ctx.storage.sql.exec("DELETE FROM tunnels WHERE id = ?", id);
      return false;
    }
    return true;
  }

  async touch(id: string): Promise<void> {
    const now = Date.now();
    this.ctx.storage.sql.exec("UPDATE tunnels SET seen = ? WHERE id = ?", now, id);
    await this.ctx.storage.setAlarm(now + idleMs);
  }

  async drop(id: string): Promise<void> {
    this.ctx.storage.sql.exec("DELETE FROM tunnels WHERE id = ?", id);
  }

  async alarm(): Promise<void> {
    const cutoff = Date.now() - idleMs;
    this.ctx.storage.sql.exec("DELETE FROM tunnels WHERE seen < ?", cutoff);
    const left = this.ctx.storage.sql
      .exec<{ n: number }>("SELECT COUNT(*) AS n FROM tunnels")
      .one();
    if (left.n > 0) await this.ctx.storage.setAlarm(Date.now() + idleMs);
  }
}

// TunnelSession is one open tunnel. The CLI long-polls it, and public requests wait here.
export class TunnelSession extends DurableObject<Env> {
  private token = "";
  private tunnelID = "";
  private queue: Job[] = [];
  private pending = new Map<string, Job>();
  private poller: ((job: Job | null) => void) | null = null;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      this.ctx.storage.sql.exec(`
        CREATE TABLE IF NOT EXISTS session (
          id INTEGER PRIMARY KEY CHECK (id = 1),
          tunnel_id TEXT NOT NULL,
          token TEXT NOT NULL,
          seen INTEGER NOT NULL
        )
      `);
      const row = this.ctx.storage.sql
        .exec<{ tunnel_id: string; token: string }>(
          "SELECT tunnel_id, token FROM session WHERE id = 1",
        )
        .toArray()[0];
      if (row) {
        this.tunnelID = row.tunnel_id;
        this.token = row.token;
      }
    });
  }

  async open(id: string, token: string): Promise<boolean> {
    const row = this.ctx.storage.sql
      .exec<{ seen: number }>("SELECT seen FROM session WHERE id = 1")
      .toArray()[0];
    if (row && Date.now() - row.seen < idleMs) return false;
    const now = Date.now();
    this.ctx.storage.sql.exec(
      "INSERT OR REPLACE INTO session (id, tunnel_id, token, seen) VALUES (1, ?, ?, ?)",
      id,
      token,
      now,
    );
    this.tunnelID = id;
    this.token = token;
    this.queue = [];
    await this.ctx.storage.setAlarm(now + idleMs);
    return true;
  }

  async next(token: string): Promise<CallResult<WireReq | null>> {
    if (!tokensMatch(token, this.token)) return fail(401, "unauthorized");
    await this.touch();
    const queued = this.queue.shift();
    if (queued) {
      this.pending.set(queued.id, queued);
      return { ok: true, value: this.wire(queued) };
    }
    const job = await new Promise<Job | null>((resolve) => {
      const timer = setTimeout(() => {
        if (this.poller) this.poller = null;
        resolve(null);
      }, pollMs);
      this.poller = (next) => {
        clearTimeout(timer);
        this.poller = null;
        resolve(next);
      };
    });
    if (!job) return { ok: true, value: null };
    this.pending.set(job.id, job);
    return { ok: true, value: this.wire(job) };
  }

  async respond(token: string, reqID: string, wire: WireResp): Promise<CallResult<true>> {
    if (!tokensMatch(token, this.token)) return fail(401, "unauthorized");
    await this.touch();
    const job = this.pending.get(reqID);
    this.pending.delete(reqID);
    if (!job || job.settled) return fail(404, "unknown request");
    job.settled = true;
    const status = wire.status || 200;
    job.done({
      status,
      headers: wire.headers ?? {},
      body: wire.body ?? "",
    });
    return { ok: true, value: true };
  }

  async publish(
    method: string,
    path: string,
    headers: HeaderMap,
    body: string,
  ): Promise<CallResult<WireResp>> {
    if (!this.token) return fail(404, "unknown tunnel");
    if (this.queue.length >= 32) return fail(503, "tunnel is busy");
    await this.touch();
    const job: Job = {
      id: newToken().slice(0, 16),
      method,
      path,
      headers,
      body,
      settled: false,
      done: () => {},
    };
    let timer: ReturnType<typeof setTimeout> | undefined;
    const result = new Promise<WireResp>((resolve) => {
      job.done = resolve;
    });
    if (this.poller) {
      const wake = this.poller;
      this.poller = null;
      wake(job);
    } else {
      this.queue.push(job);
    }
    const timeout = new Promise<null>((resolve) => {
      timer = setTimeout(() => resolve(null), publicMs);
    });
    const winner = await Promise.race([result.then((value) => value), timeout]);
    if (timer) clearTimeout(timer);
    if (winner) return { ok: true, value: winner };
    job.settled = true;
    this.queue = this.queue.filter((item) => item !== job);
    this.pending.delete(job.id);
    return fail(504, "tunnel timed out");
  }

  async alarm(): Promise<void> {
    const row = this.ctx.storage.sql
      .exec<{ seen: number; tunnel_id: string }>("SELECT seen, tunnel_id FROM session WHERE id = 1")
      .toArray()[0];
    if (!row || Date.now() - row.seen >= idleMs) {
      this.ctx.storage.sql.exec("DELETE FROM session");
      this.token = "";
      this.queue = [];
      if (row?.tunnel_id) {
        await this.env.DIRECTORY.getByName("registry").drop(row.tunnel_id);
      }
      return;
    }
    await this.ctx.storage.setAlarm(Date.now() + idleMs);
  }

  private wire(job: Job): WireReq {
    return {
      id: job.id,
      method: job.method,
      path: job.path,
      headers: job.headers,
      body: job.body,
    };
  }

  private async touch(): Promise<void> {
    const now = Date.now();
    this.ctx.storage.sql.exec("UPDATE session SET seen = ? WHERE id = 1", now);
    await this.ctx.storage.setAlarm(now + idleMs);
    if (this.tunnelID) await this.env.DIRECTORY.getByName("registry").touch(this.tunnelID);
  }
}
