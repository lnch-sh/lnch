import {
  decodeBase64,
  encodeBase64,
  filterHeaders,
  newToken,
  newTunnelID,
  readBody,
  skipResponseHeader,
  text,
  validTunnelID,
  type WireResp,
} from "./tunnel";

export { TunnelDirectory, TunnelSession } from "./tunnel";

const domain = "lnch.sh";

export default {
  async fetch(request, env): Promise<Response> {
    const url = new URL(request.url);
    const host = url.hostname.toLowerCase();

    if (host === `www.${domain}`) {
      url.hostname = domain;
      return Response.redirect(url.toString(), 301);
    }

    const tunnelID = tunnelHost(host);
    if (tunnelID) {
      if (!validTunnelID(tunnelID)) return text("unknown tunnel", 404);
      return publish(request, env, tunnelID, requestPath(url));
    }

    if (url.pathname === "/health") {
      return Response.json({ ok: true, service: "lnch" });
    }
    if (url.pathname === "/v1/tunnels" || url.pathname.startsWith("/v1/tunnels/")) {
      return api(request, env, url);
    }
    const direct = directTunnel(url.pathname);
    if (direct) return publish(request, env, direct.id, direct.path + url.search);

    const asset = await env.ASSETS.fetch(request);
    if (asset.status === 404) return text("lnch: not found", 404);
    return asset;
  },
} satisfies ExportedHandler<Env>;

function tunnelHost(host: string): string {
  const suffix = `.${domain}`;
  if (!host.endsWith(suffix) || host === domain) return "";
  const label = host.slice(0, -suffix.length);
  if (!label || label.includes(".")) return "";
  return label;
}

function directTunnel(pathname: string): { id: string; path: string } | null {
  if (!pathname.startsWith("/t/")) return null;
  const rest = pathname.slice(3);
  const slash = rest.indexOf("/");
  const id = slash === -1 ? rest : rest.slice(0, slash);
  if (!validTunnelID(id)) return null;
  const path = slash === -1 || slash === rest.length - 1 ? "/" : rest.slice(slash);
  return { id, path };
}

function requestPath(url: URL): string {
  return `${url.pathname}${url.search}`;
}

async function api(request: Request, env: Env, url: URL): Promise<Response> {
  if (request.method === "POST" && url.pathname === "/v1/tunnels") {
    return register(request, env, url);
  }
  const next = url.pathname.match(/^\/v1\/tunnels\/([^/]+)\/next$/);
  if (request.method === "GET" && next) {
    return poll(request, env, next[1]);
  }
  const response = url.pathname.match(/^\/v1\/tunnels\/([^/]+)\/responses\/([^/]+)$/);
  if (request.method === "POST" && response) {
    return respond(request, env, response[1], response[2]);
  }
  return text("lnch: not found", 404);
}

async function register(request: Request, env: Env, url: URL): Promise<Response> {
  const body = await readBody(request);
  if (body instanceof Response) return body;
  const directory = env.DIRECTORY.getByName("registry");
  let id = "";
  let token = "";
  let opened = false;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    id = newTunnelID();
    token = newToken();
    if (!(await directory.claim(id))) continue;
    try {
      opened = await env.TUNNEL.getByName(id).open(id, token);
    } catch (error) {
      await directory.drop(id);
      throw error;
    }
    if (opened) break;
    await directory.drop(id);
  }
  if (!opened) return text("could not open a tunnel", 503);

  const host = url.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  const local = host === "localhost" || host === "127.0.0.1" || host === "::1" || host.endsWith(".localhost");
  const direct = local ? `${url.origin}/t/${id}/` : `https://${domain}/t/${id}/`;
  const pretty = local ? direct : `https://${id}.${domain}/`;
  return Response.json(
    { id, url: pretty, direct_url: direct, token },
    { status: 201 },
  );
}

async function poll(request: Request, env: Env, id: string): Promise<Response> {
  if (!validTunnelID(id)) return text("unauthorized", 401);
  if (!(await env.DIRECTORY.getByName("registry").has(id))) return text("unauthorized", 401);
  const result = await env.TUNNEL.getByName(id).next(bearer(request));
  if (!result.ok) return text(result.error, result.status);
  if (!result.value) return new Response(null, { status: 204 });
  return Response.json(result.value);
}

async function respond(request: Request, env: Env, id: string, reqID: string): Promise<Response> {
  if (!validTunnelID(id)) return text("unauthorized", 401);
  const body = await readBody(request);
  if (body instanceof Response) return body;
  let wire: WireResp;
  try {
    wire = JSON.parse(new TextDecoder().decode(body)) as WireResp;
  } catch {
    return text("invalid json", 400);
  }
  if (typeof wire.body === "string" && wire.body.length > 0) {
    try {
      if (decodeBase64(wire.body).byteLength > maxBodyBytes()) return text("response is too large", 413);
    } catch {
      return text("body is not base64", 400);
    }
  }
  const result = await env.TUNNEL.getByName(id).respond(bearer(request), reqID, {
    status: wire.status ?? 0,
    headers: wire.headers ?? {},
    body: wire.body ?? "",
  });
  if (!result.ok) return text(result.error, result.status);
  return Response.json({ ok: true });
}

async function publish(request: Request, env: Env, id: string, path: string): Promise<Response> {
  if (!(await env.DIRECTORY.getByName("registry").has(id))) return text("unknown tunnel", 404);
  const body = await readBody(request);
  if (body instanceof Response) return body;
  const result = await env.TUNNEL.getByName(id).publish(
    request.method,
    path || "/",
    filterHeaders(request.headers),
    encodeBase64(body),
  );
  if (!result.ok) return text(result.error, result.status);
  const headers = new Headers();
  for (const [key, values] of Object.entries(result.value.headers ?? {})) {
    if (skipResponseHeader(key)) continue;
    for (const value of values) headers.append(key, value);
  }
  let bytes = new Uint8Array();
  if (result.value.body) {
    try {
      bytes = decodeBase64(result.value.body);
    } catch {
      return text("tunnel response was not base64", 502);
    }
  }
  return new Response(bytes, { status: result.value.status || 200, headers });
}

function bearer(request: Request): string {
  const header = request.headers.get("authorization") ?? "";
  return header.startsWith("Bearer ") ? header.slice(7) : "";
}

function maxBodyBytes(): number {
  return 16 << 20;
}
