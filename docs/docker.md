# clawtool in Docker

clawtool ships as a multi-stage Docker image based on
`gcr.io/distroless/static-debian12:nonroot`. Final image is ~7 MB
— the entire Go binary, ca-certificates, and nothing else. No
shell, no package manager, no glibc.

## Quick start

```sh
# Pull
docker pull cogitave/clawtool:latest

# Run as a stdio MCP server (most common — Claude Code etc. spawn this)
docker run -i --rm cogitave/clawtool:latest

# Verify it speaks MCP
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  | docker run -i --rm cogitave/clawtool:latest \
  | head -1
```

You should see `serverInfo` come back in the response — same
handshake the `make docker-smoke` Makefile target runs.

## Building locally

```sh
make docker          # builds cogitave/clawtool:dev
make docker-smoke    # builds + runs the MCP initialize handshake check
```

Or by hand:

```sh
docker build -t cogitave/clawtool:dev .
```

The Dockerfile is a two-stage build: `golang:1.26-alpine` compiles
the static binary with `CGO_ENABLED=0`, then it gets copied into
`distroless/static-debian12:nonroot`. No source paths in the
runtime image (build uses `-trimpath`).

## Running modes

### Stdio (default — for Claude Code / Codex / any MCP client)

```sh
docker run -i --rm cogitave/clawtool:latest
```

Use `-i` so the client can write to stdin. The container exits
when the client closes stdin.

To register with Claude Code:

```sh
claude mcp add --transport stdio clawtool -- docker run -i --rm cogitave/clawtool:latest
```

### HTTP gateway

```sh
# 1. Generate a token outside the container
docker run --rm -v $(pwd):/data cogitave/clawtool:latest \
  serve init-token /data/listener-token

# 2. Launch
docker run -d --name clawtool-serve \
  -p 8080:8080 \
  -v $(pwd)/listener-token:/data/listener-token:ro \
  cogitave/clawtool:latest \
  serve --listen 0.0.0.0:8080 --token-file /data/listener-token --mcp-http

# 3. Sanity check
curl http://localhost:8080/v1/health \
  -H "Authorization: Bearer $(cat listener-token)"
```

The HTTP surface is documented in `docs/http-api.md`. The
`--mcp-http` flag also exposes the full MCP toolset over
Streamable HTTP at `/mcp` for clients that prefer it.

### Compose (HTTP + Caddy reverse proxy)

`docker-compose.yml` at the repo root brings up clawtool serve +
Caddy with auto-provisioned TLS:

```sh
# 1. Token (one time)
clawtool serve init-token ./listener-token

# 2. Set your domain in .env (or leave default for localhost)
echo "CLAWTOOL_DOMAIN=mcp.example.com" > .env

# 3. Up
docker compose up -d
```

Caddy handles certificate management; clawtool's bearer-token
auth is enforced behind it. Volumes persist config / cache /
data across container restarts.

## Persisting state

Three XDG dirs map to the container's nonroot home:

| Host | Container | What lives here |
| --- | --- | --- |
| `clawtool-config` (named volume) | `/home/nonroot/.config/clawtool` | `config.toml`, `secrets.toml`, identity, sticky pointers |
| `clawtool-cache` (named volume) | `/home/nonroot/.cache/clawtool` | worktrees, semantic-search index, update cache |
| `clawtool-data` (named volume) | `/home/nonroot/.local/share/clawtool` | BIAM SQLite store, telemetry id |

For the stdio mode you usually don't need any of these — the
container is short-lived. For the HTTP gateway, persist all
three so BIAM state + sources survive restarts.

## Mounting your existing config

If you already have a clawtool install on the host, point the
container at it read-only:

```sh
docker run --rm -i \
  -v ~/.config/clawtool:/home/nonroot/.config/clawtool:ro \
  cogitave/clawtool:latest
```

The container will see your sources, agents, portals, hooks,
sandboxes — but can't mutate them (read-only mount).

## Sandbox profiles inside Docker

The container has no `bwrap` / `sandbox-exec` and Docker-in-Docker
adds friction. If you want sandbox enforcement around dispatched
agents, **don't run clawtool in Docker** — run it on the host
(via `make install` or the install.sh) and let the sandbox
profiles use the host's bwrap / sandbox-exec.

The Docker image is for stateless MCP / HTTP serving. Sandbox is
for dispatch-time isolation on the host.

## Image size

```text
$ docker images cogitave/clawtool
REPOSITORY              TAG       SIZE
cogitave/clawtool       dev       15MB
```

That's the whole runtime — the clawtool Go binary +
ca-certificates + distroless's tiny base. No shell, no apt, no
python. Verified via the `make docker-smoke` target which runs
the MCP `initialize` handshake against the built image and
asserts the response carries `serverInfo`.

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| `connection refused` on `/v1/health` | container exited | `docker logs clawtool-serve` — likely a missing token-file mount |
| `permission denied` reading config volume | mounted with wrong UID | distroless runs as UID 65532; chown the host dir or use a named volume |
| MCP client times out | client didn't pass `-i` | `docker run -i` is required for stdio MCP |
| Image won't pull | private registry | `docker login` against the registry hosting `cogitave/clawtool` |

## Cross-references

- `Dockerfile` — multi-stage build definition.
- `docker-compose.yml` + `Caddyfile` — HTTP gateway stack.
- `docs/http-api.md` — `/v1` endpoint reference.
- `internal/setup/recipes/runtime/clawtool_relay.go` — drops a
  similar Compose file into a project repo via `clawtool init`.
