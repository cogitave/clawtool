# clawtool

[![Latest release](https://img.shields.io/github/v/release/cogitave/clawtool?display_name=tag&sort=semver&color=blue)](https://github.com/cogitave/clawtool/releases/latest)
[![CI](https://github.com/cogitave/clawtool/actions/workflows/ci.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/ci.yml)
[![Release](https://github.com/cogitave/clawtool/actions/workflows/release.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/cogitave/clawtool?logo=go)](go.mod)
[![License](https://img.shields.io/github/license/cogitave/clawtool?color=brightgreen)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/conventional--commits-1.0.0-yellow)](https://www.conventionalcommits.org)

> **Tools. Agents. Wired.**
>
> One canonical tool layer for every AI coding agent. Install once, use everywhere — across Claude Code, Codex, Gemini, OpenCode, Hermes, and Aider.

## TL;DR — why would I install this?

You probably already have one or more AI coding agents on your machine: Claude Code, Codex, Gemini CLI, OpenCode, Hermes, Aider. Each one ships its own slightly-different Bash tool, slightly-different Read/Edit/Write, its own MCP server list, its own sandbox story, its own way of "calling another agent". They don't share state, they don't share secrets, and adding a new tool means re-registering it everywhere.

clawtool collapses that. **One binary** runs as a long-lived daemon. **Every host CLI** is wired to it as an MCP server (Claude Code via plugin, codex/gemini/opencode via `mcp add`). After that:

- `Bash`, `Read`, `Edit`, `Write`, `Grep`, `Glob`, `WebFetch`, `WebSearch` are the same tool with the same behavior in every host (timeout-safe, structured JSON, format-aware reads — PDF / Word / Excel / Jupyter / HTML).
- `SendMessage` lets any agent dispatch work to any other agent (`claude → codex`, `codex → gemini`, etc.) — async via the BIAM protocol with Ed25519-signed envelopes, edge-triggered fan-in, and a SQLite task store you can `clawtool task list` from a normal terminal.
- A single sandbox profile (bwrap / sandbox-exec / docker / gVisor) governs every tool call, regardless of which agent triggered it.
- Secrets live in one mode-0600 file, not scattered through five different `~/.config/<host>/` directories.
- A 60-tool catalog stays usable because models bind to schemas through `ToolSearch` (BM25) on demand.

**One install, one daemon, one identity, one tool surface — across every agent.** That's the whole pitch.

## What clawtool is

- **Canonical core tools.** Higher-quality replacements for native Bash, Read, Edit, Write, Grep, Glob, WebFetch — timeout-safe with process-group SIGKILL, structured JSON output (stdout/stderr/exit_code/duration_ms/timed_out/cwd), format-aware reads (PDF, Word, Excel, HTML, Jupyter), atomic writes, deterministic line cursors. Cross-platform parity (Linux, macOS, WSL2).
- **Multi-agent dispatch.** A single `SendMessage` entry point routes prompts to **Claude, Codex, Gemini, OpenCode, Hermes, or Aider** (six BIAM peers). Async via the BIAM (Bidirectional Inter-Agent Messaging) protocol — Ed25519-signed envelopes, SQLite task store, edge-triggered `TaskNotify` fan-in. Per-instance secrets injection, per-call sandbox profiles, true async (`--async` returns immediately; `clawtool task cancel` aborts).
- **Peer mesh (A2A Phase 1).** Live discovery + messaging across every claude-code / codex / gemini / opencode session on the host. Each runtime auto-registers via session hooks; the orchestrator TUI's Peers tab shows the live roster. `clawtool peer send <name> "..."` and `clawtool peer send --broadcast "..."` deliver inbox messages between sessions — three independent transports (CLI, raw HTTP, MCP) all backed by the same daemon registry. Wire shape mirrors Linux Foundation A2A's Agent Card.
- **Sandbox parity with claude.ai.** Bash/Read/Edit/Write tool calls can route through a separate gVisor/docker container instead of the host process. The `clawtool sandbox-worker` binary mirrors claude.ai's `process_api` (PID 1, WebSocket :2024, bearer auth). The `clawtool egress` proxy mirrors claude.ai's allowlist gateway (HTTP/HTTPS, CONNECT tunnel, 403 with `x-deny-reason`). On-demand skill mount via `SkillList` + `SkillLoad` MCP tools mirrors `/mnt/skills/public`.
- **Shared MCP fan-in.** A single persistent `clawtool serve --listen --mcp-http` daemon backs every host; codex / gemini / claude all dial it instead of spawning per-host stdio children. One BIAM identity, one task store, one bearer-auth'd endpoint.
- **One orchestrator TUI.** `clawtool orch` (aliases: `dashboard`, `tui`, `orchestrator`) opens a Bubble Tea panel with three sidebar tabs — Active dispatches · Done dispatches · Peers — over the same watch socket. `--plain` / `--once` modes print stdout snapshots for chat-visible monitoring.
- **Search-first discovery.** The 60-tool catalog stays usable because models bind to schemas via `ToolSearch` (bleve BM25) instead of holding every JSON schema in context.
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
hosts (claude / codex / gemini / opencode / hermes / aider)
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
| SendMessage | Forward prompts to claude / codex / gemini / opencode / hermes / aider. `--async` for BIAM, `--unattended` injects the host's elevation flag (claude `--dangerously-skip-permissions`, codex `--dangerously-bypass-approvals-and-sandbox`, gemini/opencode/hermes `--yolo`, aider `--yes`). | [internal/agents/supervisor.go](internal/agents/supervisor.go) |
| AgentList | Snapshot of the supervisor's agent registry. | [internal/tools/core/agents_tool.go](internal/tools/core/agents_tool.go) |
| TaskGet · TaskWait · TaskList · TaskNotify | BIAM task introspection + edge-triggered fan-in completion. | [internal/agents/biam](internal/agents/biam) |

### Peer mesh (A2A)

The runtime-side primitive is `clawtool peer`: every claude-code / codex / gemini / opencode session that ships clawtool's bundled hooks auto-registers itself in the daemon's peer registry, so multiple parallel sessions can discover each other and exchange notifications without spawning extra MCP servers.

| Surface | Capability | Reference |
|---|---|---|
| `clawtool a2a card` · `clawtool a2a peers` | Emit this instance's A2A Agent Card; list every registered peer with status / backend / circle filters. | [internal/cli/a2a.go](internal/cli/a2a.go) |
| `clawtool peer register / heartbeat / deregister` | Runtime-side primitives bundled hooks fire on SessionStart / Stop / SessionEnd. Session-keyed peer-id state at `~/.config/clawtool/peers.d/<session>.id`. | [internal/cli/peer.go](internal/cli/peer.go) |
| `clawtool peer send <peer_id\|--name N\|--broadcast> "<text>"` | Enqueue notification / broadcast into the target peer's inbox. | [internal/cli/peer.go](internal/cli/peer.go) |
| `clawtool peer inbox [--peek]` | Drain (or peek) the calling session's pending messages. | [internal/cli/peer.go](internal/cli/peer.go) |
| `clawtool hooks install <runtime>` | Print the wiring snippet for codex / gemini / opencode (claude-code is bundled). | [internal/cli/hooks.go](internal/cli/hooks.go) |
| `GET /v1/peers` · `POST /v1/peers/register` · `POST /v1/peers/{id}/messages` · `POST /v1/peers/broadcast` | Bearer-authed REST surface; persisted at `~/.config/clawtool/peers.json` + per-peer inbox files at `peers.d/`. | [internal/server/peers_handler.go](internal/server/peers_handler.go) · [internal/a2a](internal/a2a) |

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

The recipe catalog spans nine categories (governance / commits / release / ci / quality / supply-chain / knowledge / agents / runtime). Highlights beyond the basics: `mattpocock-skills`, `karpathy-llm-wiki`, `archon-template`, `bifrost-template`, `mcp-toolbox`, `semble`, `shell-mcp`, `clawtool-autonomous-loop`, `promptfoo-redteam`, `rtk-token-filter`, `mem0`, `superclaude`, `claude-flow`, `caveman`, `clawtool-relay`. Run `clawtool recipe list` for the full state matrix; `clawtool init --all` applies every Core-tagged recipe non-interactively (paired with `--summary-json` for scripted / chat-driven runs).

### Source catalog

`clawtool source catalog` lists all 19 built-in MCP source entries. As of v0.22.74:

- **Reference servers** (anthropic-maintained): github, slack, postgres, sqlite, filesystem, fetch, brave-search, google-maps, memory, sequentialthinking, time, git.
- **Productivity**: atlassian (Jira + Confluence), notion, exa (neural web search), context7 (live docs), playwright, desktop-commander.
- **Database / search**: mcp-toolbox (Google's reference DB MCP — Postgres / MySQL / SQLite / BigQuery / Mongo / Redis / Spanner), semble (~98% fewer tokens than grep+read), shell-mcp (sandbox-aware shell execution).

### Chat-driven setup

clawtool exposes four MCP tools so an operator can drive the entire install-and-configure pipeline from a single chat message instead of context-switching to the CLI:

| Tool | Capability | Reference |
|---|---|---|
| OnboardStatus | Read-only JSON: detected hosts, installed bridges, secrets readiness, daemon state. | [internal/tools/setup/onboard_status.go](internal/tools/setup/onboard_status.go) |
| OnboardWizard | Runs `clawtool onboard --yes` end-to-end with per-step telemetry mirrored back to the model. | [internal/tools/setup/onboard_wizard.go](internal/tools/setup/onboard_wizard.go) |
| InitApply | Applies recipes in the current repo (mirrors `clawtool init --all`); returns the `--summary-json` document. | [internal/tools/setup/init_apply.go](internal/tools/setup/init_apply.go) |
| AutonomousRun | Kicks off `clawtool autonomous "<goal>"` and streams `tick-N.json` writes back as task frames. | [internal/tools/setup/autonomous_run.go](internal/tools/setup/autonomous_run.go) |

Compose them as **one message, full pipeline**: detect → onboard → init → autonomous loop, no shell context-switch.

### Autonomous mode

```bash
clawtool autonomous "Refactor the BIAM runner to use a fan-out scheduler"
clawtool autonomous --resume .clawtool/autonomous/final.json
clawtool autonomous --watch ./repo
```

The CLI builds a session prompt from the goal + iteration metadata, dispatches it to the chosen BIAM peer (default: claude), and ends when the agent emits `done: true` in `<workdir>/.clawtool/autonomous/tick-N.json`, `--max-iterations` is hit, or the operator sends SIGINT. `--resume` continues a prior run from its `final.json`; `--watch` tails an existing run's tick stream into the terminal. Pair with `OnboardStatus` + `InitApply` (above) for the "tek mesaj, tüm pipeline" loop.

### Self-direction stack — Ideator → Autopilot → Autonomous

Three layers, one per question. Ideator surveys cheap repo-local signals and proposes work; Autopilot is the operator-gated TOML queue that decides when each proposal becomes pending; Autonomous is the dev loop above that actually runs it. Full reference: [docs/ideator.md](docs/ideator.md).

| Verb | Capability | Reference |
|---|---|---|
| `clawtool ideate [--apply] [--top N] [--source <name>]` | Eleven cheap-on-fail signal sources: `adr_questions`, `adr_drafting`, `todos`, `ci_failures`, `manifest_drift`, `bench_regression`, `deps_outdated`, `deadcode_hits`, `vuln_advisories` (govulncheck, cached on go.sum hash; drops findings already covered by the workflow `GO_VERSION` pin), `stale_files` (heuristic fallback), `pr_review_pending` (open PRs awaiting review >24h, age-banded priority). Bounded source concurrency (default 4; `CLAWTOOL_IDEATOR_MAX_CONCURRENCY=N` to override on slow filesystems). | [internal/ideator](internal/ideator) · [docs/ideator.md](docs/ideator.md) |
| `clawtool autopilot {add\|accept\|next\|done\|skip\|list\|status}` | TOML queue at `~/.config/clawtool/autopilot/queue.toml` with `proposed → pending → in_progress → done/skipped` flow. Ideator-emitted items land at `proposed`; the operator-gate `accept` is non-negotiable so the agent can't silently drive its own pipeline past human review. | [internal/autopilot](internal/autopilot) |
| MCP: `IdeateRun`, `IdeateApply`, `AutopilotStatus/List/Add/Accept/Next/Done/Skip`, `AutonomousRun` | Same three layers exposed to chat agents so the loop runs without context-switching. | [internal/tools/core](internal/tools/core) |

### Project setup verbs

| Verb | Capability |
|---|---|
| `clawtool init [--all] [--summary-json] [--yes]` | Apply the per-category recipe wizard. `--all` non-interactively applies every Core recipe; `--summary-json` emits a single decodable JSON document so InitApply / chat-driven flows can parse the result. |
| `clawtool apm import [<path>] [--dry-run] [--repo <p>]` | Import a microsoft/apm manifest (apm.yml). MCP servers register via `clawtool source add`; skills + playbooks land in `<repo>/.clawtool/apm-imported-manifest.toml`. |
| `clawtool source registry [--backend mcp\|smithery\|both] [--limit N]` | Probe an upstream MCP catalog (registry.modelcontextprotocol.io and/or registry.smithery.ai) read-only — discovery before adopting a new server. |
| `clawtool source inspect <instance> [--format text\|json]` | Audit a configured source's exposed tool surface by spawning the npm-published MCP Inspector against its stdio command. |
| `clawtool playbook list-archon [--dir <p>] [--format <text\|json>]` | List Archon (coleam00/Archon) DAG workflows under `.archon/workflows/`. Read-only; phase 2 will wire execution. |

The repo also ships a top-level `playbooks/` directory — a markdown layer (Zhixiang Luo's 10xProductivity-style) of agent-readable tool integration recipes that live alongside the MCP source-server layer. See [playbooks/README.md](playbooks/README.md).

## Configuration

| Path | Purpose |
|---|---|
| `~/.config/clawtool/config.toml` | Primary config (XDG). Tool toggles, sources, agents, dispatch policy, sandbox profiles, `[sandbox_worker]` block. |
| `~/.config/clawtool/secrets.toml` | Mode-0600 credential store for API keys / OAuth tokens / DB passwords. |
| `~/.config/clawtool/daemon.json` | Persistent daemon state (pid, port, started_at, token_file, log_file). |
| `~/.config/clawtool/listener-token` | Bearer token shared between hosts and the daemon. Mode 0600. |
| `~/.config/clawtool/peers.json` | A2A peer registry (live claude-code / codex / gemini / opencode sessions on this host). |
| `~/.config/clawtool/peers.d/<session>.id` | Session→peer_id pointer written by `clawtool peer register`; consumed by `peer heartbeat / deregister / inbox`. |
| `~/.config/clawtool/peers.d/<peer_uuid>.inbox.json` | Per-peer mailbox (256-message soft cap) persisted from the daemon's in-memory queue. |
| `~/.config/clawtool/worker-token` | Bearer token shared between daemon and sandbox-worker. |
| `~/.config/clawtool/identity.ed25519` | BIAM identity keypair (mode 0600). |
| `~/.local/share/clawtool/biam.db` | SQLite task store (Ed25519-signed envelopes, status, history). |
| `~/.local/state/clawtool/daemon.log` | Daemon stdout/stderr log. |
| `./.clawtool/rules.toml` | Project-scoped rules (predicate → verdict). |
| `./.clawtool/<name>.toml` | Project markers (mcp / brain / etc.). |
| `./wiki/` (gitignored) | Operator's local Obsidian vault for ADRs, source surveys, and daily logs. Cross-references the source code; never enters CI. Created by the `karpathy-llm-wiki` / `brain` recipes. |

Diagnostic surfaces: `clawtool overview` (one-screen status), `clawtool doctor` (deep diagnostic with fix hints), `clawtool dashboard` (live Bubble Tea TUI), `clawtool sandbox doctor` (engine availability), `clawtool source check` (credential verification).

## Sandbox-worker quick path

```bash
# 1. Generate the worker bearer token
clawtool sandbox-worker --init-token

# 2. Build the worker image (one-time). Use a moving tag — the binary inside
#    is keyed to your local clawtool version, not a frozen 0.21 release.
docker build -f Dockerfile.unified --target worker -t clawtool-worker:dev .

# 3. Run the worker container
docker run --rm \
    -v "$(pwd)":/workspace \
    -p 127.0.0.1:2024:2024 \
    -v "$XDG_CONFIG_HOME/clawtool/worker-token":/etc/worker-token:ro \
    clawtool-worker:dev \
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

- **Self-direction stack — Ideator → Autopilot → Autonomous** (v0.22.140 → v0.22.145) — Ten cheap-on-fail signal sources (CI failures, deadcode, deps_outdated with indirect-dep filter, manifest drift, BM25 regression, ADR open questions, ADR drafting age, TODOs, govulncheck advisories with workflow-pin filter + 12h cache, stale-files heuristic) feed an operator-gated TOML autopilot queue at `proposed → pending → in_progress → done`. The autonomous loop above consumes the queue. Bounded source concurrency (default 4) keeps wall time predictable on slow filesystems; vuln_advisories cache + workflow `GO_VERSION` filter keep the loop signal-rich without ghost alarms. Full reference: [docs/ideator.md](docs/ideator.md).
- **Autonomous mode** (v0.22.71 + v0.22.74) — `clawtool autonomous "<goal>"` runs a self-paced single-message dev loop against the chosen BIAM peer; the agent emits `tick-N.json` after each iteration, the loop ends on `done: true`, `--max-iterations`, or SIGINT. v0.22.74 added `--resume <final.json>` to continue a prior run and `--watch <workdir>` to tail an existing run's tick stream into the terminal. Sister MCP tool `AutonomousRun` (v0.22.72) streams the same loop back to a chat-driving model.
- **Chat-driven setup MCP tools** (v0.22.62) — `OnboardStatus`, `OnboardWizard`, `InitApply` so an operator can drive detect → onboard → recipe-apply from a single chat message instead of context-switching to the CLI. `init --all` (v0.22.56) and `init --summary-json` (v0.22.60) make the wizard scriptable.
- **Catalog growth — DB / search / shell** (v0.22.63 → v0.22.70) — `mcp-toolbox` (Google's reference DB MCP), `semble` (~98% fewer tokens than grep+read), `shell-mcp` (sandbox-aware shell execution) joined the built-in source catalog. `clawtool source registry --backend mcp|smithery|both` (v0.22.50) lets the operator probe upstream catalogs read-only; `clawtool source inspect <instance>` (v0.22.52) audits a configured source's tool surface via the npm-published MCP Inspector.
- **APM + Archon importers** (v0.22.64 + v0.22.67) — `clawtool apm import` ingests a microsoft/apm manifest (sources register via `source add`; skills + playbooks land in `.clawtool/apm-imported-manifest.toml`). `clawtool playbook list-archon` surfaces Archon DAG workflows under `.archon/workflows/` (read-only; phase 2 will wire execution).
- **A2A Phase 1 — peer discovery + messaging** (v0.22.36) — every running claude-code / codex / gemini / opencode session registers into a shared peer registry through bundled SessionStart hooks. Three independent transports (CLI `clawtool peer send`, raw HTTP `POST /v1/peers/{id}/messages`, MCP `SendMessage`) deliver inbox messages between sessions; `clawtool a2a peers` and the orchestrator TUI's new Peers tab show the live roster. Status-fidelity hooks flip peers between `busy` (UserPromptSubmit) and `online` (Notification idle_prompt) so operators see actual activity, not just registration timestamps.
- **Single TUI, four aliases** (v0.22.36) — `clawtool dashboard`, `tui`, `orchestrator`, `orch` all open the same Bubble Tea program. The legacy parallel dashboard implementation was retired; one window, three tabs (Active · Done · Peers), shared watch-socket reconnect policy. `--plain` / `--once` snapshot mode kept for chat-visible monitoring.
- **Architecture audit pass** (v0.22.36) — `internal/xdg` package consolidates the `XDG_CONFIG_HOME` fallback chain across the tree (~17 inline copies), `tools/core/atomic` writeAtomic helper exposes a single temp+rename primitive, and a deadcode sweep removed ~290 LoC of speculative test seams while wiring two genuine ones (`Client.Read/Write` round-trip test, `FrameSubsCount` symmetry test). Tree's `deadcode -test ./...` now reports empty.
- **Auto-launch onboarding** (v0.22.16) — `install.sh` now auto-runs `clawtool onboard` on a TTY install (no [Y/n] prompt to dismiss). Bypass with `CLAWTOOL_NO_ONBOARD=1`. Plus per-step telemetry across the wizard (start / host_detect / bridge_install / mcp_claim / daemon_start / identity_create / secrets_init / telemetry_consent / finish) so we can finally see *where* in the funnel people drop off.
- **Onboarded marker + nudges** (v0.22.13) — `~/.config/clawtool/.onboarded` is a single source of truth that three surfaces consume: install.sh skips the prompt when present, the Claude Code SessionStart hook stops nagging, and the `clawtool` no-args TUI no longer pre-selects the wizard.
- **System-notification banner** (v0.22.12+v0.22.16) — daemon-pushed notifications (release-available, daemon-degraded) latch in both the orchestrator and dashboard TUIs, fade after 30s. Severity drives the tint, Kind drives the icon. The orchestrator gained an Active/Done tab + viewport-bounded sidebar at the same time.
- **`SendMessage` real-time streaming** (v0.22.x) — BIAM runner broadcasts per-line `StreamFrame`s alongside Task transitions over a multiplexed unix socket (`WatchEnvelope{Kind: task | frame | system}`). The orchestrator's per-task ringbuffer renders within ~50ms instead of waiting on SQLite poll. (Replaces the older "task watch v2" item that used to live here.)
- **Cross-process dispatch handoff** — CLI `clawtool send --async` now hands the prompt to the daemon over a dedicated dispatch socket, so frame fanout reaches every consumer (orchestrator, dashboard, `task watch`) regardless of which process originated the dispatch.
- **`clawtool telemetry status / on / off` + `clawtool onboard --yes`** (v0.22.18) — the wizard's "flip telemetry off any time" hint now points at a real subcommand instead of dead-ending in "unknown command", and unattended onboarding (Docker, CI, automation scripts) is one flag away.
- **Docker e2e harness** — `test/e2e/onboard/` builds an image with mock claude/codex/gemini binaries on PATH and runs `clawtool onboard --yes` against it; `CLAWTOOL_E2E_DOCKER=1 go test ./test/e2e/onboard/...` exercises the full host-detect → bridge-install → MCP-claim → daemon-start path end-to-end.

## Roadmap

- **A2A Phase 2 — cross-host mesh** — mDNS / Tailscale tsnet for discovery beyond a single host; WebSocket transport for push notifications (Phase 1 polls the registry every 2s); token + model surfacing in `clawtool.dispatch` once the bridge stream-parser exposes them. Extends the same `peer_id` identity tuple beyond local-mesh.
- **Persona templates absorb (claude-octopus)** — `clawtool agent template apply <code-review-team>` to scaffold curated bridges (`code-reviewer` + `test-writer` + `security-auditor`) with model + system_prompt + tool allowlist combos, so a fresh repo gets a working multi-agent setup in one command.
- **Cross-host BIAM identity routing** — per-call `from_instance` parameter on `SendMessage` so codex / gemini / claude can mutually notify each other through the shared daemon.
- **Onboarding state machine** — collapse `init` + `onboard` into one engine; per-feature opt-in matrix; verify-summary at the end (`send --list`, `bridge list`, `source check`, `sandbox doctor`). The v0.22.13–v0.22.18 nudge + auto-launch + telemetry-verb bundle covers the *discovery* half; the engine collapse is what's left.
- **Sandbox-worker phase 2 follow-up** — wire `Client.Read` / `Client.Write` (round-trip-tested) through `tools/core` so Read/Edit/Write tool calls can route to the worker; per-conversation ephemeral workers; gVisor `runsc` runtime selection wired into the docker engine adapter.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/feature-shipping-contract.md](docs/feature-shipping-contract.md). The four-plane review checklist is enforced by CI; commits append no `Co-Authored-By` trailer for AI agents.

## License

[MIT](LICENSE)
