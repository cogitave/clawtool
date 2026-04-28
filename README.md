# clawtool

[![Latest release](https://img.shields.io/github/v/release/cogitave/clawtool?display_name=tag&sort=semver&color=blue)](https://github.com/cogitave/clawtool/releases/latest)
[![CI](https://github.com/cogitave/clawtool/actions/workflows/ci.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/ci.yml)
[![Release](https://github.com/cogitave/clawtool/actions/workflows/release.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/cogitave/clawtool?logo=go)](go.mod)
[![License](https://img.shields.io/github/license/cogitave/clawtool?color=brightgreen)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/conventional--commits-1.0.0-yellow)](https://www.conventionalcommits.org)

> **Tools. Agents. Wired.**
>
> One canonical tool layer for every AI coding agent. Install once, use everywhere — across Claude Code, Codex, Gemini, OpenCode, and Hermes.

## What clawtool is

- **Canonical core tools.** Higher-quality replacements for native Bash, Read, Edit, Write, Grep, Glob, WebFetch — timeout-safe with process-group SIGKILL, structured JSON output (stdout/stderr/exit_code/duration_ms/timed_out/cwd), format-aware reads (PDF, Word, Excel, HTML, Jupyter), atomic writes, deterministic line cursors. Cross-platform parity (Linux, macOS, WSL2).
- **Multi-agent dispatch.** A single `SendMessage` entry point routes prompts to Claude, Codex, Gemini, OpenCode, or Hermes. Async via the BIAM (Bidirectional Inter-Agent Messaging) protocol — Ed25519-signed envelopes, SQLite task store, edge-triggered `TaskNotify` fan-in. Per-instance secrets injection, per-call sandbox profiles, true async (`--async` returns immediately; `clawtool task cancel` aborts).
- **Sandbox parity with claude.ai.** Bash/Read/Edit/Write tool calls can route through a separate gVisor/docker container instead of the host process. The `clawtool sandbox-worker` binary mirrors claude.ai's `process_api` (PID 1, WebSocket :2024, bearer auth). The `clawtool egress` proxy mirrors claude.ai's allowlist gateway (HTTP/HTTPS, CONNECT tunnel, 403 with `x-deny-reason`). On-demand skill mount via `SkillList` + `SkillLoad` MCP tools mirrors `/mnt/skills/public`.
- **Shared MCP fan-in.** A single persistent `clawtool serve --listen --mcp-http` daemon backs every host; codex / gemini / claude all dial it instead of spawning per-host stdio children. One BIAM identity, one task store, one bearer-auth'd endpoint.
- **Search-first discovery.** A 50+ tool catalog stays usable because models bind to schemas via `ToolSearch` (bleve BM25) instead of holding every JSON schema in context.
- **Marketplace plugin.** First-class Claude Code plugin: `claude plugin install clawtool@clawtool-marketplace` registers the MCP server, drops slash commands, and loads the routing skill — no manual `claude mcp add-json` editing.

## Quick install

```bash
# Claude Code marketplace path (recommended)
claude plugin marketplace add cogitave/clawtool
claude plugin install clawtool@clawtool-marketplace

# Or direct binary install
curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh

# Or from source
go install github.com/cogitave/clawtool/cmd/clawtool@latest
```

## Onboarding

```bash
clawtool onboard            # interactive wizard — host detection, MCP claims, identity, telemetry consent
clawtool overview           # one-screen status of daemon + sandbox-worker + agents + bridges
clawtool doctor             # full diagnostic with fix hints per finding
```

The wizard:

1. Detects host CLIs (claude / codex / gemini / opencode / hermes).
2. Offers to install missing bridges (Claude Code marketplace plugins for codex / gemini, binary checks for opencode / hermes).
3. **Registers clawtool as an MCP server in every detected host** (codex / gemini / opencode) — points each at the persistent shared daemon. This is the fan-in step: every host dials one daemon, not a per-host stdio child.
4. Generates a BIAM identity (Ed25519 keypair, mode 0600) for `clawtool send --async`.
5. Records telemetry consent.
6. Optional: drops repo-level recipes via `clawtool init`.

## Architecture

```
hosts (claude / codex / gemini / opencode / hermes)
    │  MCP — stdio (Claude Code) or HTTP (codex/gemini via `mcp add --url`)
    ▼
clawtool serve --listen --mcp-http (the daemon)
    │  bearer auth, WebSocket fan-in
    │
    ├── core tools (Bash, Read, Edit, Write, Grep, Glob, WebFetch, …)
    ├── BIAM dispatch + TaskNotify fan-in (Ed25519, SQLite)
    ├── secrets injection (per-instance API keys)
    ├── sandbox profiles (bwrap / sandbox-exec / docker)
    ├── portals (saved web-UI targets)
    ├── aggregated MCP source servers (github, slack, postgres, …)
    │
    └── (optional) sandbox-worker fan-out
        │  WebSocket dial, bearer auth
        ▼
        clawtool sandbox-worker (in a gVisor / docker container)
            ├── exec / read / write / glob / grep handlers
            ├── /workspace mount + path-jail (host paths invisible)
            └── HTTP_PROXY → clawtool egress (allowlist; 403 deny)
```

The asymmetry that matters: **the orchestrator dials the worker, not the reverse.** clawtool's daemon owns connection lifetimes for both legs — hosts dial the daemon, the daemon dials the worker. This is the canonical sandbox shape every claude.ai-style mimic converges on. Design context: [wiki/decisions/029-sandbox-worker.md](wiki/decisions/029-sandbox-worker.md).

The project adheres to a **four-plane shipping contract** ([docs/feature-shipping-contract.md](docs/feature-shipping-contract.md)) — every new feature or tool must land on the MCP plane (core logic + registration), the marketplace plane (slash commands + manifest), the skill plane (SKILL.md routing-map row), and the surface-drift test allowlist (or get a real backing tool). The `TestSurfaceDrift_*` test family enforces this at CI time.

## What's in the box

### Core tools

| Tool | Capability | Reference |
|---|---|---|
| Bash | Shell exec; timeout-safe via process-group SIGKILL; structured JSON; `background=true` for async via BashOutput / BashKill. | [internal/tools/core/bash.go](internal/tools/core/bash.go) |
| BashOutput | Snapshot of a background Bash task — live stdout / stderr / status / exit_code. | [internal/tools/core/bash_bg_tool.go](internal/tools/core/bash_bg_tool.go) |
| BashKill | SIGKILL a background Bash task's process group. | [internal/tools/core/bash_bg_tool.go](internal/tools/core/bash_bg_tool.go) |
| Read | Format-aware (PDF / docx / xlsx / csv / html / ipynb / json / yaml / toml / xml); deterministic line cursors; binary refusal. | [internal/tools/core/read.go](internal/tools/core/read.go) |
| Edit | Atomic temp+rename; line-ending and BOM preserve; ambiguity guard. | [internal/tools/core/edit.go](internal/tools/core/edit.go) |
| Write | Atomic write; auto-create parents; Read-before-Write enforcement. | [internal/tools/core/write.go](internal/tools/core/write.go) |
| Grep | ripgrep first, system grep fallback; .gitignore-aware; multi-pattern. | [internal/tools/core/grep.go](internal/tools/core/grep.go) |
| Glob | doublestar `**` recursion; .gitignore-aware (toggleable); cross-platform forward-slash output. | [internal/tools/core/glob.go](internal/tools/core/glob.go) |
| WebFetch | URL → clean article text via Mozilla Readability; SSRF guard; 10 MiB cap. | [internal/tools/core/webfetch.go](internal/tools/core/webfetch.go) |
| WebSearch | Pluggable backend (Brave / Tavily / SearXNG); secrets-managed API key. | [internal/tools/core/websearch.go](internal/tools/core/websearch.go) |
| ToolSearch | bleve BM25 ranking across the loaded catalog. | [internal/tools/core/toolsearch.go](internal/tools/core/toolsearch.go) |
| SemanticSearch | Vector embeddings; lazy index. | [internal/tools/core/semanticsearch.go](internal/tools/core/semanticsearch.go) |
| Verify | Multi-runner test/lint (Make / pnpm / go / pytest / cargo / just) with log excerpting. | [internal/tools/core/verify.go](internal/tools/core/verify.go) |
| Commit | Git commit with Conventional Commits validation + Co-Authored-By block + pre_commit rules gate. | [internal/checkpoint/commit.go](internal/checkpoint/commit.go) |

### Multi-agent dispatch

| Tool | Capability | Reference |
|---|---|---|
| SendMessage | Forward prompts to claude / codex / gemini / opencode / hermes. `--async` for BIAM, `--unattended` injects the host's elevation flag (claude `--dangerously-skip-permissions`, codex `--dangerously-bypass-approvals-and-sandbox`, gemini/opencode/hermes `--yolo`). | [internal/agents/supervisor.go](internal/agents/supervisor.go) · [ADR-014](wiki/decisions/014-relay-and-supervisor.md) |
| AgentList | Snapshot of the supervisor's agent registry. | [internal/tools/core/agents_tool.go](internal/tools/core/agents_tool.go) |
| TaskGet · TaskWait · TaskList · TaskNotify | BIAM task introspection + edge-triggered fan-in completion. | [internal/agents/biam](internal/agents/biam) · [ADR-015](wiki/decisions/015-biam-protocol.md) |

### Sandbox + worker

| Surface | Capability | Reference |
|---|---|---|
| `clawtool serve --listen --mcp-http` | The persistent shared daemon. Bearer-auth WebSocket; hosts dial it. | [internal/server/http.go](internal/server/http.go) |
| `clawtool daemon start \| stop \| status \| restart \| path \| url` | Lifecycle of the persistent daemon. State at `~/.config/clawtool/daemon.json`. | [internal/daemon/daemon.go](internal/daemon/daemon.go) |
| `clawtool sandbox-worker --listen :2024` | Worker process inside a docker / runsc container. WebSocket :2024, bearer auth, /workspace mount, path-jail. | [internal/sandbox/worker](internal/sandbox/worker) |
| `clawtool egress --listen :3128 --allow ...` | HTTP/HTTPS allowlist proxy with CONNECT tunnel. 403 with `x-deny-reason`. | [internal/sandbox/egress](internal/sandbox/egress) |
| Sandbox profiles | bwrap / sandbox-exec / docker engines. Fail-closed when profile policy can't be enforced. | [internal/sandbox](internal/sandbox) · [ADR-020](wiki/decisions/020-sandbox-feature.md) |

### Rules engine

| Tool | Capability | Reference |
|---|---|---|
| RulesCheck | Evaluate `.clawtool/rules.toml` against a Context (event + changed paths + commit message + tool calls). Returns Verdict per rule. | [docs/rules.md](docs/rules.md) · [internal/rules](internal/rules) |
| RulesAdd | Append a rule to local or user rules.toml — same writer the CLI uses. | [internal/tools/core/rules_add_tool.go](internal/tools/core/rules_add_tool.go) |

### Authoring scaffolders

| Tool | Capability | Reference |
|---|---|---|
| AgentNew | Scaffold a Claude Code subagent persona. | [internal/agentgen](internal/agentgen) |
| SkillNew | Generate an agentskills.io-standard skill folder. | [internal/skillgen](internal/skillgen) |
| SkillList · SkillLoad | On-demand skill discovery + content load (claude.ai `/mnt/skills/public` mimic). | [internal/tools/core/skill_load_tool.go](internal/tools/core/skill_load_tool.go) |
| McpList / McpNew / McpRun / McpBuild / McpInstall | MCP server scaffolder, runner, builder, installer (Go / Python / TypeScript). | [internal/mcpgen](internal/mcpgen) · [ADR-019](wiki/decisions/019-mcp-authoring-scaffolder.md) |

### Browser + Portal

| Tool | Capability | Reference |
|---|---|---|
| BrowserFetch · BrowserScrape | Headless browser via Obscura (CDP). | [internal/portal](internal/portal) |
| Portal* | Saved web-UI targets — `PortalAsk` drives login flow → predicate → response extraction. | [ADR-018](wiki/decisions/018-portal-feature.md) |

### Bridges + Recipes

| Tool | Capability | Reference |
|---|---|---|
| BridgeList · BridgeAdd · BridgeRemove · BridgeUpgrade | Install canonical bridges (codex-plugin-cc, gemini-plugin-cc, opencode acp, hermes-agent). | [internal/setup/recipes/bridges](internal/setup/recipes/bridges) · [ADR-014](wiki/decisions/014-relay-and-supervisor.md) |
| RecipeList · RecipeStatus · RecipeApply | Project-setup recipes (license / codeowners / dependabot / release-please / brain / etc.). | [internal/setup](internal/setup) |

## Configuration

| Path | Purpose |
|---|---|
| `~/.config/clawtool/config.toml` | Primary config (XDG). Tool toggles, sources, agents, dispatch policy, sandbox profiles, `[sandbox_worker]` block. |
| `~/.config/clawtool/secrets.toml` | Mode-0600 credential store for API keys / OAuth tokens / DB passwords. |
| `~/.config/clawtool/daemon.json` | Persistent daemon state (pid, port, started_at, token_file, log_file). |
| `~/.config/clawtool/listener-token` | Bearer token shared between hosts and the daemon. Mode 0600. |
| `~/.config/clawtool/worker-token` | Bearer token shared between daemon and sandbox-worker. |
| `~/.config/clawtool/identity.ed25519` | BIAM identity keypair (mode 0600). |
| `~/.local/share/clawtool/biam.db` | SQLite task store (Ed25519-signed envelopes, status, history). |
| `~/.local/state/clawtool/daemon.log` | Daemon stdout/stderr log. |
| `./.clawtool/rules.toml` | Project-scoped rules (predicate → verdict). |
| `./.clawtool/<name>.toml` | Project markers (mcp / brain / etc.). |

Diagnostic surfaces: `clawtool overview` (one-screen status), `clawtool doctor` (deep diagnostic with fix hints), `clawtool dashboard` (live Bubble Tea TUI), `clawtool sandbox doctor` (engine availability), `clawtool source check` (credential verification).

## Sandbox-worker quick path

```bash
# 1. Generate the worker bearer token
clawtool sandbox-worker --init-token

# 2. Build the worker image (one-time)
docker build -f Dockerfile.worker -t clawtool-worker:0.21 .

# 3. Run the worker container
docker run --rm \
    -v "$(pwd)":/workspace \
    -p 127.0.0.1:2024:2024 \
    -v "$XDG_CONFIG_HOME/clawtool/worker-token":/etc/worker-token:ro \
    clawtool-worker:0.21 \
    sandbox-worker --token-file /etc/worker-token

# 4. (Optional) Run the egress allowlist proxy
clawtool egress --listen :3128 --allow .openai.com,.anthropic.com,.github.com &

# 5. Tell the daemon to route through the worker
cat >> ~/.config/clawtool/config.toml <<'EOF'
[sandbox_worker]
mode = "container"
url  = "ws://127.0.0.1:2024/ws"
EOF
clawtool daemon restart
```

After this, every Bash tool call (from any host — claude / codex / gemini) executes inside the worker container, behind the egress allowlist, with model-generated code never touching the operator's host process.

## Roadmap

- **Cross-host BIAM identity routing** (#196) — per-call `from_instance` parameter on `SendMessage` so codex / gemini / claude can mutually notify each other through the shared daemon. Tasarım turu pending.
- **Onboarding state machine** (#194, ADR-027) — collapse `init` + `onboard` into one engine; per-feature opt-in matrix; verify-summary at the end (`send --list`, `bridge list`, `source check`, `sandbox doctor`).
- **Task watch v2** (#185) — Unix socket push from BIAM runner to consumers; eliminates the 250ms poll.
- **Orchestrator multi-pane TUI** — Phase 1 ships: `clawtool dashboard` consumes the daemon's task-watch Unix socket so state transitions reach the tasks pane in real time (sub-50ms). Phase 2 adds split-pane streaming per dispatched agent. Design: [ADR-028](wiki/decisions/028-orchestrator-tui.md).
- **Sandbox-worker phase 2 follow-up** — Read/Edit/Write routing through the worker (Phase 2 covered Bash); per-conversation ephemeral workers; gVisor `runsc` runtime selection wired into the docker engine adapter.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/feature-shipping-contract.md](docs/feature-shipping-contract.md). The four-plane review checklist is enforced by CI; commits append no `Co-Authored-By` trailer for AI agents.

## License

[MIT](LICENSE)
