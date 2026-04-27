# clawtool MCP Authoring (`clawtool mcp new`)

`clawtool mcp` is the authoring surface for **MCP servers** —
sister to `clawtool skill new` (which scaffolds Agent Skills per
agentskills.io). One operator-facing distinction worth keeping
clear:

| Surface | What it builds | Where it runs |
| --- | --- | --- |
| `clawtool skill new` | An agentskills.io skill folder (SKILL.md + scripts/ + references/ + assets/) | Loaded by the agent's skill runtime |
| `clawtool mcp new` | A standalone **MCP server** (Go / Python / TypeScript) | Hosted by `clawtool serve` (or any MCP-aware client) |

## Status

- **Surface**: shipped in v0.16.4 — CLI subcommand + `Mcp*` MCP
  tool names register today, ToolSearch indexes them, the noun
  is locked.
- **`clawtool mcp list`**: ships read-only (walker stub today,
  upgrades when generated artifacts arrive).
- **`clawtool mcp new / run / build / install`**: surface returns
  a deferred-feature error until v0.17 — the same pattern v0.16.1
  used for `portal ask` before the CDP driver landed in v0.16.2.

The full design lives in
[ADR-019](../wiki/decisions/019-mcp-authoring-scaffolder.md). The
walk-through below previews the v0.17 user experience so operators
can stage their own work.

## What v0.17 will scaffold

```sh
clawtool mcp new my-thing
```

Wizard prompts (huh.Form):

1. **Description** — the server's self-description (becomes the
   server's "instructions" string).
2. **Language** — TypeScript (`@modelcontextprotocol/sdk`),
   Python (`fastmcp`), Go (`mark3labs/mcp-go`).
3. **Transport** — stdio (default — installable as a clawtool
   source) or streamable-HTTP (standalone network service).
4. **Packaging** — native (binary / npm / pypi) or Docker.
5. **First tool**:
   - `name` (snake_case)
   - `description`
   - input schema (simple fields wizard or paste JSON Schema)
6. **Add another tool?** — loop on yes; v1 supports tools only,
   prompts and resource composition arrive later.
7. **Generate Claude Code plugin files?** — default yes (writes
   `.claude-plugin/plugin.json`).

## Output (per language)

Common across all three:

```
my-thing/
├── .clawtool/mcp.toml         # clawtool metadata: language, transport, tools[]
├── .claude-plugin/             # plugin.json + marketplace.json.template
├── README.md
├── Makefile                    # build / run / install targets
├── .gitignore
└── Dockerfile                  # only when Docker selected
```

Per-language source layout:

- **Go**: `cmd/my-thing/main.go`, `internal/tools/example.go`,
  `go.mod`. Build & run: `make build && ./bin/my-thing`.
- **Python**: `src/mything/{__init__,__main__,server,tools/example}.py`,
  `pyproject.toml`, `tests/`. Build & run:
  `pip install -e . && python -m mything`.
- **TypeScript**: `src/server.ts`, `src/tools/example.ts`,
  `package.json`, `tsconfig.json`, `test/`. Build & run:
  `npm install && npm run build && node dist/server.js`.

Dockerfile is opt-in; the Docker recipe wraps the same launch
command in `docker run -i --rm my-thing:latest`.

## Install + run

```sh
clawtool mcp build ./my-thing
clawtool mcp install ./my-thing --as my-thing
clawtool serve
```

`mcp install` writes a `[sources.my-thing]` block into
`~/.config/clawtool/config.toml`, identical to the catalog flow
in `clawtool source add`. The runtime entry point — Claude
Code, Codex, OpenCode, the HTTP gateway — sees the new server
through the existing aggregation in
`internal/sources/manager.go`. No new code path.

For **third-party** MCP servers (GitHub, Postgres, Slack), keep
using `clawtool source add` from the catalog. `mcp install` is
the in-repo edit-test-debug shortcut.

`clawtool serve --plugin <path>` is **not** the recommended path
for scaffolded servers — it bypasses config / secrets / source
health / `<instance>__<tool>` naming.

## Plugin parity (Claude Code marketplace)

Every scaffolded repo includes `.claude-plugin/` from day one.
The operator manages the manifest, pushes the repo to git, and
uses Claude Code's native marketplace commands. clawtool does
not own the publish lifecycle (no `clawtool mcp publish`).

For the marketplace mechanics, see Claude Code's plugin
documentation:
[claude.com/docs/claude-code/plugins](https://code.claude.com/docs/en/plugins).

## Today (before v0.17 lands)

- Hand-roll the MCP server using your language's canonical SDK
  (`mark3labs/mcp-go` / `fastmcp` / `@modelcontextprotocol/sdk`).
- Once it builds, register via:
  ```sh
  clawtool source add my-thing --command "/path/to/binary"
  ```
  This is exactly the path `mcp install` will write a shortcut
  for in v0.17.

## MCP tool names (registered now)

For agents discovering the surface via `ToolSearch`:

- `McpList` — read-only walker (ships today, empty until v0.17).
- `McpNew` / `McpRun` / `McpBuild` / `McpInstall` — surface
  visible; returns deferred-feature error until v0.17.

## Cross-references

- ADR-019 (`wiki/decisions/019-mcp-authoring-scaffolder.md`) —
  full design + rationale (Codex/Gemini parallel review synthesis).
- ADR-007 (`wiki/decisions/007-leverage-best-in-class-not-reinvent.md`)
  — picks the canonical SDK per language; we never write our own
  MCP wire protocol.
- ADR-008 (`wiki/decisions/008-curated-source-catalog.md`) —
  catalog boundary; `mcp install` writes into the same registry
  the catalog populates.
- ADR-014 (`wiki/decisions/014-clawtool-relay-and-cli-multiplexer.md`)
  — `clawtool serve` runtime that consumes the registered source.
- `docs/portals.md`, `docs/browser-tools.md`, `docs/http-api.md` —
  for custom browser tooling beyond the built-in surface, scaffold
  a dedicated MCP server with `clawtool mcp new`.
