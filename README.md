# lnch

Launch any dev stack in one command.

`lnch` detects a project, starts its dev servers in dependency order, assigns ports, checks health, and collects logs. `lnch share` prints a public `https://<id>.lnch.sh` URL and a QR code. The hosted relay is `https://lnch.sh`.

The CLI and `lnch relay` are the open-source core. Run your own relay with `--relay`, or pass `--relay off` to keep the link on the LAN.

The missing piece between `npm run dev` and a full docker-compose / Tilt setup.

## Install from source

This works today. Go 1.23 or newer is required.

```bash
git clone https://github.com/lnch-sh/lnch
cd lnch
go install ./cmd/lnch
```

From this checkout, `go install ./cmd/lnch` puts `lnch` on `$(go env GOPATH)/bin`.

## Install a release

`install.sh` and `install.ps1` download a GitHub release of `lnch-sh/lnch`. They exit with the source instructions above when no release exists.

```bash
curl -fsSL https://lnch.sh/install.sh | sh
```

```powershell
irm https://lnch.sh/install.ps1 | iex
```

Release archives are named `lnch_linux_amd64.tar.gz`, `lnch_darwin_arm64.tar.gz`, and `lnch_windows_amd64.zip` (amd64 and arm64). Set `LNCH_BIN` to choose the install directory. Set `LNCH_VERSION` (shell) to pin a tag such as `v0.1.0`.

Homebrew and Scoop formulas are not published here. Add them when a release has real checksums.

## 30-second quickstart

```bash
cd my-app
lnch detect
lnch
lnch down
```

`lnch` with no arguments is `lnch up`. In a terminal it opens the dashboard. Quit with `q`. The services keep running only while that process is in the foreground. For a background stack:

```bash
lnch up --detach
lnch status
lnch down
```

`lnch status` prints one snapshot and exits. It does not open a dashboard. Use `lnch status --watch` for the live view.

## What it detects

| Found | What runs |
| --- | --- |
| `lnch.toml` or `.lnch.toml` | Exactly that file. Detection stops. |
| `Procfile` or `Procfile.dev` | Each entry. Detection stops. |
| `package.json` | `dev`, then `start`, then `serve`, via pnpm, yarn, bun, or npm |
| `go.mod` | `go run ./cmd/<name>` for each `cmd/*` main, otherwise `go run .` |
| `Cargo.toml` | `cargo run` for packages with a binary |
| Python | Django `runserver`, uvicorn, or `flask run` |
| Compose file with a `services:` key | `docker compose up` |
| `Makefile` or `justfile` | `make dev` / `just dev`, then `start`, and only when nothing else matched |

A root script that is only a monorepo runner (`turbo`, `nx`, `lerna`, recursive pnpm, and the same family) does not also start nested `package.json` scripts. Other ecosystems in the tree are still detected. Workspace packages come from `package.json` `workspaces`, `pnpm-workspace.yaml`, and `go.work`.

`lnch init` writes `lnch.toml` from that detection. `--stdout` prints it. `--force` overwrites an existing file.

## Share

```bash
lnch share
```

That registers a tunnel with `https://lnch.sh` and prints `https://<id>.lnch.sh`, plus a QR code in a terminal. The same tunnel also answers at `https://lnch.sh/t/<id>/`. Phones open the public HTTP URL. They do not trust the local HTTPS certificate.

`--relay off` stays on the LAN, for example `http://192.168.1.20:43112/`. To run the relay yourself:

```bash
lnch relay --addr 127.0.0.1:9090
lnch share --relay http://127.0.0.1:9090
```

On a host that receives `*.example.com`, `lnch relay --addr :8080 --domain example.com --scheme https` prints `https://<id>.example.com`.

Relay choice is `--relay`, then `share.relay` in `lnch.toml`, then `LNCH_RELAY`, then `https://lnch.sh`. `off` disables it. If the relay cannot be reached within five seconds, lnch keeps the LAN URL and records the error. `LNCH_RELAY_TOKEN` or `--relay-token` must match a self-hosted relay's `--register-token`. The hosted relay does not require a token.

Anyone with the link can reach the dev server. WebSocket upgrades, including Vite hot reload, stay on the local URL.

## Local HTTPS

HTTP services get an `https://localhost` proxy signed by a CA in `~/.lnch`. Trust it once:

```bash
lnch trust
```

On Windows this adds the CA to the current user's root store. On macOS it adds it to the login keychain. On Linux the command prints the `update-ca-certificates` steps. Restart the browser afterward.

`--no-https` skips the proxies. TCP services, including Compose, are not wrapped.

## Dashboard

`lnch up` in a terminal, and `lnch status --watch`, use the dashboard.

| Key | Action |
| --- | --- |
| `j` / `k` | Select a service |
| `r` | Restart the selected service |
| `s` | Share |
| `o` or Enter | Open the selected URL |
| `g` | Toggle the QR code |
| `/` | Filter logs |
| `q` | Quit. `lnch up` also stops the stack. |
| `X` | Stop the stack from `lnch status --watch` |

`NO_COLOR` or `TERM=dumb` skips the QR drawing. `--no-tui` streams prefixed logs instead. `--json` prints one JSON document on success and `{"error","hints"}` on failure.

## Commands

```bash
lnch up --dry-run
lnch up --detach --wait 30s
lnch share --relay http://127.0.0.1:9090
lnch status --json
lnch logs web --tail 50
lnch logs api --follow
lnch restart web
lnch open
lnch open --public
lnch down
lnch detect --json
lnch init --stdout
lnch trust
lnch mcp
lnch relay --addr 127.0.0.1:9090
lnch version
```

`--root` selects the project directory. `lnch down` is safe to repeat: a stopped project prints `already stopped` and exits 0. A second `lnch up` attaches to a stack that is already answering, instead of starting another copy.

## Config

[`examples/lnch.toml`](examples/lnch.toml) is a Postgres, Go API, and Vite app. Services start after `depends_on` is healthy or running. `${port}`, `$PORT`, and `${db.port}` expand when each process starts.

```toml
name = "shop"

[share]
service = "web"
relay = "http://127.0.0.1:9090"

[[services]]
name = "api"
command = "go run ./cmd/api"
port = 8080
depends_on = ["db"]
health = "http://127.0.0.1:${port}/health"
restart = "on-failure"
env.DATABASE_URL = "postgres://127.0.0.1:${db.port}/shop?sslmode=disable"
```

`protocol` is `http` or `tcp`. `restart` is `no` or `on-failure` (up to five times). `health` is an HTTP URL, `tcp://127.0.0.1:${port}`, or `none`. `primary` is the service `lnch open` and the share URL serve. `public = true` limits the share mux to the marked services.

## Agents

Scripts and MCP use the detached, JSON path. They should not sit in the dashboard.

```bash
lnch up --detach --json --no-tui
lnch status --json
lnch down --json
```

`lnch mcp` speaks MCP on stdio:

```json
{"mcpServers":{"lnch":{"command":"lnch","args":["mcp"]}}}
```

Tools: `lnch_detect`, `lnch_up`, `lnch_down`, `lnch_status`, `lnch_logs`, `lnch_restart`, `lnch_share`. Pass `relay` to `lnch_up` when the stack is launched. `lnch_share` publishes a stack that is already running and uses the relay from that launch.

## Layout

Runtime files live in `~/.lnch` (`runs/`, `plans/`, `logs/`, and the local CA). Project ids are a hash of the absolute path. Each service receives `PORT`, `LNCH=1`, `LNCH_SERVICE`, and `LNCH_ROOT`.

## Development

```bash
go test ./...
go build -o bin/lnch ./cmd/lnch
```

The site in [`web/`](web/) is a Vite + Tailwind homepage and docs page, served by a Cloudflare Worker named `lnch`.

```bash
cd web
npm install
npm run dev
npm run deploy
```

`npm run deploy` publishes the Worker, attaches `lnch.sh` and `www.lnch.sh`, and routes `*.lnch.sh` at the tunnel. `www` redirects to the apex. The zone has to be on the same Cloudflare account. A proxied wildcard DNS record for `*` is what lets `https://<id>.lnch.sh` resolve.

CI runs `go test` and `go build` on Ubuntu and Windows, and `npm run build` for the site. A `v*` tag builds the archives the install scripts download.

## License

MIT. See [LICENSE](LICENSE).
