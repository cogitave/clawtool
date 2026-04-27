# clawtool HTTP API

`clawtool serve --listen :8080` mounts a thin HTTP gateway in front of the
same supervisor + recipe registry the CLI and MCP server use. It is the
right surface to call from Postman, cURL, n8n, or any non-MCP client that
wants to dispatch a prompt to Claude / Codex / OpenCode / Gemini.

> TLS is **not** terminated inside clawtool. Front it with nginx, caddy, or
> Cloudflare Tunnel. clawtool only mounts plain HTTP and relies on the
> reverse proxy for HTTPS.

## Boot

```sh
# 1. generate a 256-bit hex bearer token (mode 0600)
clawtool serve init-token              # writes ~/.config/clawtool/listener-token
                                       # also prints the token to stdout

# 2. start the gateway
clawtool serve --listen :8080 --token-file ~/.config/clawtool/listener-token

# Optional: also mount the full MCP toolset over Streamable HTTP at /mcp.
clawtool serve --listen :8080 --token-file ~/.config/clawtool/listener-token --mcp-http
```

Flag summary:

| Flag | Default | Notes |
| --- | --- | --- |
| `--listen` | (none — required) | `host:port` passed to `http.ListenAndServe`. |
| `--token-file` | `$XDG_CONFIG_HOME/clawtool/listener-token` | Bearer token, mode 0600. Refused when missing or empty. |
| `--mcp-http` | off | Mount the MCP toolset at `/mcp` via `mcp-go`'s StreamableHTTPServer (still bearer-protected). |

## Auth

Every endpoint expects:

```
Authorization: Bearer <token>
```

The token is compared in constant time. Missing or wrong → `401`
with a JSON `{"error": "..."}` body. The token-file may be world/group-
readable on dev setups (you'll see a stderr warning); production should
keep it `chmod 0600`.

## Endpoints

All endpoints accept and emit `application/json` unless noted.

### `GET /v1/health`

Liveness probe. Always `200` for an authenticated caller.

```json
{ "status": "ok", "version": "v0.15.x" }
```

### `GET /v1/agents[?status=callable]`

Snapshot of the supervisor's registry — same shape as
`clawtool send --list` and the MCP `AgentList` tool. Pass
`?status=callable` to filter to dispatchable instances.

```json
{
  "count": 2,
  "agents": [
    {
      "instance": "claude",
      "family": "claude",
      "bridge": "",
      "status": "callable",
      "callable": true,
      "auth_scope": "claude",
      "tags": [],
      "failover_to": []
    },
    {
      "instance": "codex1",
      "family": "codex",
      "bridge": "codex-bridge",
      "status": "callable",
      "callable": true,
      "auth_scope": "codex1",
      "tags": ["fast", "cheap"],
      "failover_to": []
    }
  ]
}
```

### `POST /v1/send_message`

Dispatch a prompt to the resolved agent's upstream CLI and stream the
response back. Body (JSON):

```json
{
  "instance": "codex1",
  "prompt": "Summarize this repo in one paragraph.",
  "tag": "",
  "opts": {
    "session_id": "",
    "model": "",
    "format": "text",
    "cwd": ""
  }
}
```

| Field | Meaning |
| --- | --- |
| `instance` | Pinned instance name (e.g. `codex1`, `claude-personal`). Empty triggers ADR-014 resolution: `tag` > sticky default > single-callable fallback. |
| `prompt` | Required. Plain text — clawtool does not wrap or templatize. |
| `tag` | Sugar for `opts.tag`. With `tag` set, dispatch routes via tag-routed policy (any callable instance carrying that tag). |
| `opts.session_id` | Vendor-specific resume UUID (claude / codex / opencode). Ignored by transports that don't support resume. |
| `opts.model` | Vendor-specific model name. Empty = upstream default. |
| `opts.format` | `text` / `json` / `stream-json`. Pass-through; not every upstream honours every value. |
| `opts.cwd` | Working directory the upstream CLI runs in. Defaults to clawtool's own cwd. |

Response: `200` with `Content-Type: application/x-ndjson`. The body is
the upstream's stream verbatim (NDJSON for claude/gemini stream-json,
ACP frames for opencode acp, plain text otherwise). Disconnecting the
HTTP client cancels the upstream process.

Errors:
- `400` — body decode error / missing `prompt` / unknown instance.
- `401` — bad bearer.

### `GET /v1/recipes[?category=<name>][&repo=<path>]`

List project-setup recipes. Same row shape as the MCP `RecipeList` tool.
Pass `repo=/abs/path` to evaluate `Detect` for each recipe in that repo
(adds `status` + `detail` per row).

```json
{
  "count": 24,
  "recipes": [
    {
      "name": "license-mit",
      "category": "governance",
      "description": "Drop an SPDX-tagged MIT LICENSE file…",
      "upstream": "https://spdx.org/licenses/MIT.html",
      "stability": "stable",
      "status": "applied",
      "detail": "LICENSE present, SPDX header matched"
    }
  ]
}
```

Categories: `governance`, `commits`, `release`, `ci`, `quality`,
`supply-chain`, `knowledge`, `agents`, `runtime`.

### `POST /v1/recipe/apply`

Apply one recipe to a repo. HTTP callers must pass `repo` explicitly —
the gateway refuses to default to `cwd` so an orchestrator can't
silently mutate `$HOME`.

```json
{
  "name": "dependabot",
  "repo": "/srv/projects/myrepo",
  "options": { "interval": "weekly" }
}
```

Response on success (`200`):

```json
{
  "recipe": "dependabot",
  "category": "supply-chain",
  "repo": "/srv/projects/myrepo",
  "skipped": false,
  "skip_reason": "",
  "installed_prereqs": [],
  "manual_prereqs": [],
  "verify_ok": true
}
```

On failure the body still carries the rich detail above plus an `error`
key, and the status flips to `400`. `verify_error` shows up when the
recipe applied but its post-apply verify failed.

### `POST /mcp` (optional, when `--mcp-http`)

Streamable HTTP transport for the full MCP toolset (Bash, Read, Edit,
Write, Grep, Glob, ToolSearch, WebFetch, WebSearch, SendMessage,
AgentList, BridgeAdd/List/Remove/Upgrade, TaskGet/Wait/List, Verify,
SemanticSearch, SkillNew, RecipeList/Apply, plus aggregated source
tools). Wraps `github.com/mark3labs/mcp-go`'s StreamableHTTPServer.

Use this from any MCP-aware client that talks Streamable HTTP — the
tools, schemas, and replies are identical to the stdio surface.

## Examples

### cURL

```sh
TOKEN=$(cat ~/.config/clawtool/listener-token)

curl -s http://localhost:8080/v1/health \
  -H "Authorization: Bearer $TOKEN"

curl -s "http://localhost:8080/v1/agents?status=callable" \
  -H "Authorization: Bearer $TOKEN"

# Trigger Gemini, stream the reply
curl -N \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data '{
    "instance": "gemini",
    "prompt": "Refactor README.md for clarity",
    "opts": { "format": "text" }
  }' \
  http://localhost:8080/v1/send_message
```

### Postman

1. **New Request → POST** `http://localhost:8080/v1/send_message`.
2. **Authorization** tab → Type **Bearer Token** → paste the token.
3. **Body** → **raw** → **JSON**:

   ```json
   {
     "instance": "gemini",
     "prompt": "Refactor README.md for clarity",
     "opts": { "format": "text" }
   }
   ```

4. **Send**. The response panel streams NDJSON as it arrives (Postman
   batches into chunks; the underlying transport is chunked
   transfer-encoding so disconnect-cancellation works the same way).

For `/v1/recipes` and `/v1/recipe/apply` use the same auth setup — they
are plain `GET` / `POST` JSON.

### n8n / Zapier / scripts

Treat clawtool as any HTTP service: bearer header + JSON body. The
streamed response works with any client that handles
`application/x-ndjson` or chunked transfer encoding.

## Failure modes

| Status | Cause |
| --- | --- |
| `400` | Malformed JSON, missing `prompt`, unknown recipe / category, `recipe/apply` without `repo`, dispatch error before any byte streamed. |
| `401` | Missing / malformed `Authorization`, or bearer mismatch. |
| `404` | Unknown path. Body lists the supported endpoints. |
| `405` | Wrong verb (e.g. `GET /v1/send_message`). |
| `500` | Supervisor failure loading config; check the gateway's stderr. |

Streaming dispatches that error mid-flight close the response without
flipping the status — the upstream's emitted bytes are returned as-is
and the connection ends. The bearer-auth, dispatch-policy, and rate-
limit logic is shared with the CLI and MCP surfaces, so any change to
those (`[dispatch]` stanza, `[agents.X]` tags, `[secrets.X]`) takes
effect on the HTTP gateway too.

## Cross-references

- Server flags + config layout: see `README.md` "Install" and the
  `[dispatch]` / `[agents]` / `[hooks]` examples.
- Dispatch policies (round-robin, failover, tag-routed): `README.md`
  "What's new in v0.14 / v0.15".
- BIAM async (`bidi=true`): `README.md` "How to use BIAM async
  dispatch". Async-via-HTTP is on the roadmap; today the HTTP
  `send_message` is synchronous-streaming.
- MCP-only tooling (TaskGet, SemanticSearch, etc.) is callable via
  `--mcp-http` Streamable HTTP, not through the v1 REST surface.
