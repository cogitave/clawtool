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

**v0.17 shipped.** All five verbs are live:

- `clawtool mcp new <name> [--yes] [--output <dir>]` — interactive
  wizard or `--yes` defaults. Generates a real, compilable
  scaffold for the chosen language.
- `clawtool mcp list [--root <dir>]` — walks `<root>` for
  `.clawtool/mcp.toml` markers and prints one row per project.
- `clawtool mcp run <path>` / `mcp build <path>` — shim through
  the project's own `Makefile` (`make run` / `make build`).
- `clawtool mcp install <path> [--as <instance>]` — reads the
  marker, derives the launch command, writes
  `[sources.<instance>]` into `~/.config/clawtool/config.toml`.

MCP equivalents: `McpNew`, `McpList`. `McpRun` / `McpBuild` /
`McpInstall` surface a hint to invoke the CLI shortcut instead
(those touch the operator's filesystem + language toolchain, so
the model giving advice is the natural pattern).

Smoke-tested end-to-end: `mcp new --yes` → `go mod tidy` →
`go build` → MCP `initialize` handshake responds correctly.
The generated server actually talks the protocol on day one.

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

## Today (production)

```sh
clawtool mcp new my-thing --yes              # scaffold with defaults
cd my-thing && make build                    # compile / install / npm build
clawtool mcp install . --as my-thing         # writes [sources.my-thing]
# Edit internal/tools/<file> and add real logic.
```

Or run the wizard interactively (no `--yes`) to pick language,
transport, packaging, plugin manifest, and your first tool.

## MCP tool names

For agents discovering the surface via `ToolSearch`:

- `McpNew` — full generator. Required args: `name`,
  `description`, `language`. Optional: `transport`, `packaging`,
  `tool_name`, `tool_description`, `output`, `plugin`.
- `McpList` — walks for `.clawtool/mcp.toml` markers under
  `root`.
- `McpRun` / `McpBuild` / `McpInstall` — surface returns a hint
  to use the CLI shortcut (these run in the operator's shell
  because they touch language toolchains).

## Built-in MCP tools (v0.22.62 / .72 surface)

`clawtool serve` registers a set of built-in tools alongside any
scaffolded sources. The v0.22.62–.72 surface added four
chat-driven setup + autonomous-loop tools to the built-in
catalog:

- `OnboardStatus` (v0.22.62) — read-only probe of a repo's
  clawtool setup state. Returns `has_clawtool_dir`,
  `has_claude_md`, `onboarded_marker`, per-recipe
  `recipe_states` (`applied` / `partial` / `absent` / `error`),
  and a `suggested_next_action` string. Pure read; never writes.
  Use BEFORE `InitApply` / `OnboardWizard` to decide what's
  worth running.
- `InitApply` (v0.22.62) — chat-driven mirror of
  `clawtool init`. Dispatches into the same `setup.Apply`
  machinery. `core_only=true` (default) limits to Core recipes;
  `dry_run=true` previews without writes. Returns `applied` /
  `skipped` / `pending` / `failed` arrays plus
  `pending_actions` and `next_steps`. Idempotent.
- `OnboardWizard` (v0.22.62) — non-interactive subset of
  `clawtool onboard`. Persists agent-family default + telemetry
  preference + writes the `~/.config/clawtool/.onboarded`
  marker. Requires `non_interactive=true` to confirm the caller
  understands this is a SUBSET of the wizard (bridge installs +
  daemon ensure + MCP host registration stay CLI-only). Valid
  `agent_family` values: `claude` / `codex` / `gemini` /
  `opencode` / `hermes` / `none` (or empty).
- `AutonomousRun` (v0.22.72) — chat-driven entry point for
  clawtool's self-paced dev loop. Args: `goal` (required),
  `repo`, `agent` (default `claude`), `max_iterations` (default
  10), `cooldown_seconds` (default 300), `dry_run`, `core_only`.
  Returns `done`, `iterations_run`, `files_changed`, `summary`,
  `final_json_path`, `ticks[]`. Refuses to run when the repo
  lacks `.clawtool/` — surfaces a structured error pointing at
  `OnboardWizard` + `InitApply` instead of auto-onboarding (the
  calling agent owns that decision). See `docs/autonomous.md`
  for the full loop contract.

These four are wired through the registry in
`internal/tools/core/manifest.go`; the implementations live in
`internal/tools/setup/`. A single tool ladder for the calling
agent looks like:

```
1. OnboardStatus → decide what to do next
2. OnboardWizard (once per host) → register defaults
3. InitApply → drop core recipes
4. AutonomousRun → drive the dev loop in-process
```

## Cross-references

- `docs/portals.md`, `docs/browser-tools.md`, `docs/http-api.md` —
  for custom browser tooling beyond the built-in surface, scaffold
  a dedicated MCP server with `clawtool mcp new`.
- `docs/autonomous.md` — `AutonomousRun` contract + tick.json
  shape + `clawtool autonomous` CLI counterpart.
- `docs/bootstrap.md` — `clawtool bootstrap` zero-click flow that
  spawns an agent and chains `OnboardWizard` + `InitApply`
  through MCP.
