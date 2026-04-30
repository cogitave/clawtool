# Mastra — setup playbook

Connects clawtool's agent surface to a [Mastra](https://github.com/mastra-ai/mastra)
TypeScript agent server. Mastra agents run inside a local Node
process and expose each agent over an HTTP `generate` endpoint;
the clawtool agent reaches them by shelling out to `curl` (or
the `mastra` CLI) via `mcp__clawtool__Bash`.

## When to use this vs `clawtool source add`

- **`source add <name>`** resolves a bare name against clawtool's
  built-in catalog (`internal/catalog/builtin.toml`) and spawns a
  packaged MCP server. Mastra is not in the catalog, and clawtool's
  source layer currently only supports `type = "mcp"` (see
  `internal/config/config.go` — `Source.Type` is documented as
  `currently only "mcp"`). A first-class `type = "http"` entry for
  Mastra is on the backlog; until it lands, treat Mastra as a
  CLI/HTTP playbook tool, not a source.

- **This playbook** has the agent shell out to the live Mastra
  server. Pick this when:
  - You're prototyping Mastra agents on `localhost` and want
    clawtool to call them without a daemon-config round-trip.
  - You want the agent to use **your** running `npm run dev`
    server (so logs and traces stay in your Mastra terminal).

## Prerequisites

- Node.js 20+ (`node --version`).
- A scaffolded Mastra project. The fastest path:
  ```bash
  npx create-mastra@latest my-mastra
  cd my-mastra
  npm run dev
  ```
  `npm run dev` boots the Mastra server on
  `http://localhost:4111` by default and prints the registered
  agent IDs.
- `curl` and `jq` (already on macOS / most Linux distros).

## Step 1 — confirm the server is up

```bash
curl -s http://localhost:4111/api/agents | jq 'keys'
```

Should print a JSON array of agent IDs (e.g. `["weatherAgent"]`).
If the call hangs or 404s, re-check `npm run dev` is still
running and the port matches the Mastra console banner.

## Step 2 — round-trip an agent

Mastra exposes `POST /api/agents/<agent-id>/generate` with a JSON
body shaped like the Vercel AI SDK `generate` call:

```bash
AGENT_ID="weatherAgent"   # whatever Step 1 returned

curl -s -X POST \
  "http://localhost:4111/api/agents/${AGENT_ID}/generate" \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"ping"}]}' \
  | jq -r '.text'
```

A successful round-trip prints the agent's reply text. This is
the smoke test the agent runs to confirm the wiring before
composing larger workflows.

## Step 3 — what the agent can do now

The agent calls `mcp__clawtool__Bash` with `curl` against the
Mastra server, the same way the GitHub playbook shells out to
`gh`. Common shapes:

| Intent | Command |
|---|---|
| List registered agents | `curl -s http://localhost:4111/api/agents \| jq 'keys'` |
| Generate a reply | `curl -s -X POST http://localhost:4111/api/agents/<id>/generate -H 'Content-Type: application/json' -d '{"messages":[{"role":"user","content":"<prompt>"}]}'` |
| Inspect tools wired to an agent | `curl -s http://localhost:4111/api/agents/<id> \| jq '.tools'` |
| Stream output (SSE) | `curl -N -X POST http://localhost:4111/api/agents/<id>/stream -H 'Content-Type: application/json' -d '{...}'` |

For the full Mastra HTTP surface, see the upstream docs:
<https://mastra.ai/docs>.

## Troubleshooting

- **`401 Unauthorized`** — the Mastra dev server is open by
  default, but production deployments often sit behind an API
  key. Add the header to every `curl`:
  ```bash
  curl -H "Authorization: Bearer ${MASTRA_API_KEY}" ...
  ```
  Store the key in the operator's keychain rather than echoing
  it on the command line.

- **`CORS` errors** when driving the server from a browser-side
  helper — pass the expected origin header explicitly:
  ```bash
  curl -H "Origin: http://localhost:3000" ...
  ```
  Server-side `curl` from `mcp__clawtool__Bash` is unaffected;
  CORS only bites browser fetches.

- **Connection refused** — `npm run dev` exited. Re-start it in
  the Mastra project root and re-run Step 1.

- **Different port** — Mastra honours `PORT=...` or a
  `mastra.config.ts` override. Read the banner line `Mastra dev
  server listening on http://localhost:<port>` and substitute
  through every step.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/mastra/setup.md`.
**Auth flows covered**: open dev server (no auth) and Bearer-token
production deployments. SSO / OAuth-fronted Mastra deployments are
not yet covered — open an issue with the auth shape if you hit
one.
