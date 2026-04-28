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

## TL;DR — why would I install this?

You probably already have one or more AI coding agents on your machine: Claude Code, Codex, Gemini CLI, OpenCode, Hermes. Each one ships its own slightly-different Bash tool, slightly-different Read/Edit/Write, its own MCP server list, its own sandbox story, its own way of "calling another agent". They don't share state, they don't share secrets, and adding a new tool means re-registering it everywhere.

clawtool collapses that. **One binary** runs as a long-lived daemon. **Every host CLI** is wired to it as an MCP server (Claude Code via plugin, codex/gemini/opencode via `mcp add`). After that:

- `Bash`, `Read`, `Edit`, `Write`, `Grep`, `Glob`, `WebFetch`, `WebSearch` are the same tool with the same behavior in every host (timeout-safe, structured JSON, format-aware reads — PDF / Word / Excel / Jupyter / HTML).
- `SendMessage` lets any agent dispatch work to any other agent (`claude → codex`, `codex → gemini`, etc.) — async via the BIAM protocol with Ed25519-signed envelopes, edge-triggered fan-in, and a SQLite task store you can `clawtool task list` from a normal terminal.
- A single sandbox profile (bwrap / sandbox-exec / docker / gVisor) governs every tool call, regardless of which agent triggered it.
- Secrets live in one mode-0600 file, not scattered through five different `~/.config/<host>/` directories.
- A 50+ tool catalog stays usable because models bind to schemas through `ToolSearch` (BM25) on demand.

**One install, one daemon, one identity, one tool surface — across every agent.** That's the whole pitch.

## What clawtool is

- **Canonical core tools.** Higher-quality replacements for native Bash, Read, Edit, Write, Grep, Glob, WebFetch — timeout-safe with process-group SIGKILL, structured JSON output (stdout/stderr/exit_code/duration_ms/timed_out/cwd), format-aware reads (PDF, Word, Excel, HTML, Jupyter), atomic writes, deterministic line cursors. Cross-platform parity (Linux, macOS, WSL2).
- **Multi-agent dispatch.** A single `SendMessage` entry point routes prompts to Claude, Codex, Gemini, OpenCode, or Hermes. Async via the BIAM (Bidirectional Inter-Agent Messaging) protocol — Ed25519-signed envelopes, SQLite task store, edge-triggered `TaskNotify` fan-in. Per-instance secrets injection, per-call sandbox profiles, true async (`--async` returns immediately; `clawtool task cancel` aborts).
- **Sandbox parity with claude.ai.** Bash/Read/Edit/Write tool calls can route through a separate gVisor/docker container instead of the host process. The `clawtool sandbox-worker` binary mirrors claude.ai's `process_api` (PID 1, WebSocket :2024, bearer auth). The `clawtool egress` proxy mirrors claude.ai's allowlist gateway (HTTP/HTTPS, CONNECT tunnel, 403 with `x-deny-reason`). On-demand skill mount via `SkillList` + `SkillLoad` MCP tools mirrors `/mnt/skills/public`.
- **Shared MCP fan-in.** A single persistent `clawtool serve --listen --mcp-http` daemon backs every host; codex / gemini / claude all dial it instead of spawning per-host stdio children. One BIAM identity, one task store, one bearer-auth'd endpoint.
- **Search-first discovery.** A 50+ tool catalog stays usable because models bind to schemas via `ToolSearch` (bleve BM25) instead of holding every JSON schema in context.
- **Marketplace plugin.** First-class Claude Code plugin: `claude plugin install clawtool@clawtool-marketplace` registers the MCP server, drops slash commands, and loads the routing skill — no manual `claude mcp add-json` editing.

## Quick install

Pick the path that matches your primary agent:

```bash
# 1) Claude Code primary user — use the marketplace plugin.
#    Registers the MCP server, drops slash commands, loads the routing skill.
claude plugin marketplace add cogitave/clawtool
claude plugin install clawtool@clawtool-marketplace

# 2) Codex / Gemini / OpenCode primary user (or all of the above)
#    — install the standalone binary; the onboard wizard claims each host.
curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh

# 3) Building from source
go install github.com/cogitave/clawtool/cmd/clawtool@latest
```

The `install.sh` script:

- detects your OS / arch (linux+darwin × amd64+arm64), downloads the matching tarball, **verifies SHA-256** against the published `checksums.txt`, and atomically installs to `~/.local/bin/clawtool` (override with `CLAWTOOL_INSTALL_DIR`);
- when run interactively (TTY), **auto-launches `clawtool onboard` immediately after install** — no extra prompt to dismiss; the wizard runs the moment the binary lands. `curl|sh` / CI / Docker layers skip auto-launch automatically (no TTY); set `CLAWTOOL_NO_ONBOARD=1` to opt out elsewhere;
- is safe to re-run; it doubles as an upgrade path. (You can also self-update with `clawtool upgrade` — atomic binary replacement, signed release.)

## First run — what to expect

```bash
clawtool                    # no-args lands you in a friendly TUI menu;
                            # if you haven't onboarded yet, it pre-selects
                            # the wizard and tells you so.
clawtool onboard            # interactive wizard — runs in ~30 seconds
clawtool overview           # one-screen status of daemon + sandbox-worker + agents + bridges
clawtool doctor             # deep diagnostic with fix hints per finding
clawtool send --list        # lists every callable agent the daemon can dispatch to
clawtool task list --active # see in-flight BIAM dispatches across all hosts
clawtool dashboard          # live Bubble Tea TUI — tasks, frames, system events
clawtool orchestrator       # split-pane TUI for watching multiple async dispatches
```

What the **onboard wizard** does (one-time, takes about 30 seconds):

1. Detects host CLIs on `$PATH` (claude / codex / gemini / opencode / hermes).
2. Asks **which CLI you'll mostly drive clawtool through** — that answer pre-selects defaults for the next two steps.
3. Offers to install missing **bridges** (Claude Code marketplace plugins for codex / gemini, binary check for opencode / hermes). Bridges are how clawtool fans `SendMessage` calls out to the right CLI.
4. **Registers clawtool as an MCP server in every detected host** (`mcp add` for codex / gemini / opencode) — every host dials one shared daemon instead of spawning per-host stdio children. This is the fan-in.
5. Starts the long-running daemon (`clawtool daemon start`) so cross-session memory + dispatch survive shell restarts.
6. Generates a BIAM identity (Ed25519 keypair, mode 0600) for signed multi-agent messaging.
7. Drops a 0600 `secrets.toml` stub so per-source API keys have a place to land.
8. Records telemetry consent (opt-in only — disabled by default).
9. Writes an `~/.config/clawtool/.onboarded` marker so future sessions know setup is done.

Once onboarded, both Claude Code's SessionStart hook and the no-args TUI stay quiet about setup; if the marker is missing, **both surfaces nudge you back to `clawtool onboard`** — you'll never wonder why the agents can't see clawtool's tools yet.

### Common questions

- **"Do I have to install the binary if I only use Claude Code?"** No — the marketplace plugin is enough for Claude Code. You'd only want the binary too if you also use codex / gemini / opencode and want the shared daemon, or if you want the `clawtool` CLI on your terminal.
- **"What writes my MCP config?"** `clawtool onboard` shells out to each host's own `mcp add` command — it doesn't poke at config files behind your back. You can audit / remove with the host's own tools (`claude mcp list`, `codex mcp list`, …).
- **"Where does state live?"** Everything is under `~/.config/clawtool/` (config, secrets, identity, daemon state) and `~/.local/share/clawtool/` (BIAM SQLite store) by default. Honors `XDG_CONFIG_HOME` / `XDG_DATA_HOME`. See the [Configuration](#configuration) table below.
- **"Is the daemon always running?"** Only after onboard. It's a normal user-process (not a system service); `clawtool daemon stop` kills it cleanly. It auto-restarts when a host MCP call comes in (`daemon.Ensure`).
- **"How do I update?"** `clawtool upgrade` does a signed self-replacement. New releases also push a system notification through the daemon, so any host with clawtool wired in will surface a "vX → vY available" banner without you having to check.

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

The asymmetry that matters: **the orchestrator dials the worker, not the reverse.** clawtool's daemon owns connection lifetimes for both legs — hosts dial the daemon, the daemon dials the worker. This is the canonical sandbox shape every claude.ai-style mimic converges on.

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
| SendMessage | Forward prompts to claude / codex / gemini / opencode / hermes. `--async` for BIAM, `--unattended` injects the host's elevation flag (claude `--dangerously-skip-permissions`, codex `--dangerously-bypass-approvals-and-sandbox`, gemini/opencode/hermes `--yolo`). | [internal/agents/supervisor.go](internal/agents/supervisor.go) |
| AgentList | Snapshot of the supervisor's agent registry. | [internal/tools/core/agents_tool.go](internal/tools/core/agents_tool.go) |
| TaskGet · TaskWait · TaskList · TaskNotify | BIAM task introspection + edge-triggered fan-in completion. | [internal/agents/biam](internal/agents/biam) |

### Sandbox + worker

| Surface | Capability | Reference |
|---|---|---|
| `clawtool serve --listen --mcp-http` | The persistent shared daemon. Bearer-auth WebSocket; hosts dial it. | [internal/server/http.go](internal/server/http.go) |
| `clawtool daemon start \| stop \| status \| restart \| path \| url` | Lifecycle of the persistent daemon. State at `~/.config/clawtool/daemon.json`. | [internal/daemon/daemon.go](internal/daemon/daemon.go) |
| `clawtool sandbox-worker --listen :2024` | Worker process inside a docker / runsc container. WebSocket :2024, bearer auth, /workspace mount, path-jail. | [internal/sandbox/worker](internal/sandbox/worker) |
| `clawtool egress --listen :3128 --allow ...` | HTTP/HTTPS allowlist proxy with CONNECT tunnel. 403 with `x-deny-reason`. | [internal/sandbox/egress](internal/sandbox/egress) |
| Sandbox profiles | bwrap / sandbox-exec / docker engines. Fail-closed when profile policy can't be enforced. | [internal/sandbox](internal/sandbox) |

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
| McpList / McpNew / McpRun / McpBuild / McpInstall | MCP server scaffolder, runner, builder, installer (Go / Python / TypeScript). | [internal/mcpgen](internal/mcpgen) |

### Browser + Portal

| Tool | Capability | Reference |
|---|---|---|
| BrowserFetch · BrowserScrape | Headless browser via Obscura (CDP). | [internal/portal](internal/portal) |
| Portal* | Saved web-UI targets — `PortalAsk` drives login flow → predicate → response extraction. | [internal/portal](internal/portal) |

### Bridges + Recipes

| Tool | Capability | Reference |
|---|---|---|
| BridgeList · BridgeAdd · BridgeRemove · BridgeUpgrade | Install canonical bridges (codex-plugin-cc, gemini-plugin-cc, opencode acp, hermes-agent). | [internal/setup/recipes/bridges](internal/setup/recipes/bridges) |
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

## Recently shipped

- **Auto-launch onboarding** (v0.22.16) — `install.sh` now auto-runs `clawtool onboard` on a TTY install (no [Y/n] prompt to dismiss). Bypass with `CLAWTOOL_NO_ONBOARD=1`. Plus per-step telemetry across the wizard (start / host_detect / bridge_install / mcp_claim / daemon_start / identity_create / secrets_init / telemetry_consent / finish) so we can finally see *where* in the funnel people drop off.
- **Onboarded marker + nudges** (v0.22.13) — `~/.config/clawtool/.onboarded` is a single source of truth that three surfaces consume: install.sh skips the prompt when present, the Claude Code SessionStart hook stops nagging, and the `clawtool` no-args TUI no longer pre-selects the wizard.
- **System-notification banner** (v0.22.12+v0.22.16) — daemon-pushed notifications (release-available, daemon-degraded) latch in both the orchestrator and dashboard TUIs, fade after 30s. Severity drives the tint, Kind drives the icon. The orchestrator gained an Active/Done tab + viewport-bounded sidebar at the same time.
- **`SendMessage` real-time streaming** (v0.22.x) — BIAM runner broadcasts per-line `StreamFrame`s alongside Task transitions over a multiplexed unix socket (`WatchEnvelope{Kind: task | frame | system}`). The orchestrator's per-task ringbuffer renders within ~50ms instead of waiting on SQLite poll. (Replaces the older "task watch v2" item that used to live here.)
- **Cross-process dispatch handoff** — CLI `clawtool send --async` now hands the prompt to the daemon over a dedicated dispatch socket, so frame fanout reaches every consumer (orchestrator, dashboard, `task watch`) regardless of which process originated the dispatch.
- **`clawtool telemetry status / on / off` + `clawtool onboard --yes`** (v0.22.18) — the wizard's "flip telemetry off any time" hint now points at a real subcommand instead of dead-ending in "unknown command", and unattended onboarding (Docker, CI, automation scripts) is one flag away.
- **Docker e2e harness** — `test/e2e/onboard/` builds an image with mock claude/codex/gemini binaries on PATH and runs `clawtool onboard --yes` against it; `CLAWTOOL_E2E_DOCKER=1 go test ./test/e2e/onboard/...` exercises the full host-detect → bridge-install → MCP-claim → daemon-start path end-to-end.

## Roadmap

- **Cross-host BIAM identity routing** — per-call `from_instance` parameter on `SendMessage` so codex / gemini / claude can mutually notify each other through the shared daemon.
- **Onboarding state machine** — collapse `init` + `onboard` into one engine; per-feature opt-in matrix; verify-summary at the end (`send --list`, `bridge list`, `source check`, `sandbox doctor`). The v0.22.13–v0.22.18 nudge + auto-launch + telemetry-verb bundle covers the *discovery* half; the engine collapse is what's left.
- **Sandbox-worker phase 2 follow-up** — Read/Edit/Write routing through the worker (Phase 2 covered Bash); per-conversation ephemeral workers; gVisor `runsc` runtime selection wired into the docker engine adapter.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/feature-shipping-contract.md](docs/feature-shipping-contract.md). The four-plane review checklist is enforced by CI; commits append no `Co-Authored-By` trailer for AI agents.

## License

[MIT](LICENSE)
