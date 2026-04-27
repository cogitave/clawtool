# clawtool

[![Latest release](https://img.shields.io/github/v/release/cogitave/clawtool?display_name=tag&sort=semver&color=blue)](https://github.com/cogitave/clawtool/releases/latest)
[![CI](https://github.com/cogitave/clawtool/actions/workflows/ci.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/ci.yml)
[![Release](https://github.com/cogitave/clawtool/actions/workflows/release.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/cogitave/clawtool?logo=go)](go.mod)
[![License](https://img.shields.io/github/license/cogitave/clawtool?color=brightgreen)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/conventional--commits-1.0.0-yellow)](https://www.conventionalcommits.org)

> **Tools. Agents. Wired.**

## What clawtool is
- **Canonical core tools**: clawtool replaces native Bash, Read, Edit, Write, Grep, and Glob with higher-quality, timeout-safe, and format-aware equivalents. These tools provide uniform structured JSON output and rigorous process-group reaping, ensuring that agent operations are deterministic and safe across Linux, macOS, and WSL2 environments.
- **Multi-agent dispatch**: A single `SendMessage` entry point routes prompts to Claude, Codex, Gemini, or Opencode instances. This supervisor layer uses the Bidirectional Inter-Agent Messaging (BIAM) protocol with an async SQLite task store and edge-triggered `TaskNotify` fan-in, allowing one agent to delegate long-running tasks to another without blocking the conversation.
- **Marketplace plugin**: clawtool is packaged as a first-class Claude Code marketplace plugin. On installation, it auto-registers the MCP server and injects necessary slash commands and skills with zero ceremony. There is no need for manual `claude mcp add-json` editing or complex environment setup.
- **Search-first discovery**: When a tool catalog grows past a few dozen entries, agents can no longer hold every schema in context. clawtool implements a `ToolSearch` primitive powered by bleve BM25 ranking, allowing agents to discover and bind to the right tool based on natural-language intent.

## Quick install
```bash
claude plugin marketplace add cogitave/clawtool
claude plugin install clawtool@clawtool-marketplace
```
Or build from source:
```bash
go install github.com/cogitave/clawtool/cmd/clawtool@latest
```

## Onboarding (5-minute walk)
- **Confirm the install**: Verify the connection between Claude Code and the clawtool MCP server to ensure all core tools are correctly registered.
```bash
/clawtool
```
- **List enabled tools**: Inspect the full catalog of core, bridge, and aggregated third-party tools currently available to your agent instances.
```bash
/clawtool-tools-list
```
- **Add a source**: Enable a third-party source from the built-in catalog (e.g., GitHub, Slack, or Postgres) to expand your agent's knowledge and action space.
```bash
clawtool source add github
```
- **Run your first ToolSearch**: Use natural-language queries to discover the right tool for any task, allowing the agent to bind to the specific schema it needs.
```bash
mcp__clawtool__ToolSearch query="commit my work"
```
- **Set a sandbox profile**: Run the sandbox diagnostic to see supported host engines (bwrap, sandbox-exec, or docker) and pick a profile for isolated execution.
```bash
clawtool sandbox doctor
```
- **Send your first cross-agent message**: Delegate a complex sub-task to a different agent family (e.g., Codex) via the supervisor's async dispatch layer.
```bash
clawtool send --agent codex "audit this repo"
```
- **Scaffold a subagent**: Create a new subagent persona with a custom dispatcher manifest, allowed-tools list, and model preference for specialized tasks.
```bash
clawtool agent new my-researcher --description "Expert in codebase exploration and dependency mapping"
```

## What's in the box

clawtool ships with a comprehensive catalog of tools designed to replace or augment the native capabilities of AI coding agents. These tools are grouped by their functional area and are designed to work together to provide a seamless, multi-agent developer experience.

### Core tools
The core toolset replaces the standard Bash, File, and Search operations with higher-fidelity equivalents. These tools are built with a focus on safety, atomic operations, and format-awareness, ensuring that agents can work reliably across different operating systems and file types.

| Tool | Capability | Reference |
|---|---|---|
| Bash | Run shell commands with timeout safety and structured JSON (supports `background=true` for async execution). | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| BashOutput | Snapshot of a background Bash task — live stdout, stderr, and terminal exit status. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| BashKill | Cancel a background Bash task via SIGKILL to the entire process group. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| Read | Format-aware file reader with stable line cursors; supports PDF, Word, Excel, HTML, Jupyter, and more. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| Edit | Precise substring replacement with atomic temp+rename, BOM preservation, and ambiguity guards. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| Write | Atomic file creation or replacement with parent directory auto-create and BOM preservation. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| Grep | High-performance regex content search powered by ripgrep with .gitignore-aware traversal. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| Glob | Directory traversal and file matching with double-star support via the doublestar engine. | [ADR-021](wiki/decisions/021-core-tools-polish.md) |
| WebFetch | Retrieve URLs and return clean article text via Mozilla Readability with a 10MB body cap. | |
| WebSearch | Ranked web search results via Brave Search, Tavily, or SearXNG backends. | |
| ToolSearch | Search-first discovery across the tool catalog using bleve BM25 ranking and field boosts. | |
| SemanticSearch | Intent-based code search using vector embeddings; index is built lazily on first call. | |
| Verify | Multi-runner test/lint execution (Make, pnpm, go, pytest, cargo, just) with log excerpting. | |

### Multi-agent dispatch
Multi-agent dispatch allows agents to communicate and delegate tasks to one another. By leveraging the BIAM protocol, clawtool enables complex, multi-step workflows where specialized agents handle specific sub-tasks asynchronously.

| Tool | Capability | Reference |
|---|---|---|
| SendMessage | Forward prompts to Claude, Codex, Gemini, or Opencode instances with async/bidi support. | [ADR-015](wiki/decisions/015-bidirectional-inter-agent-messaging.md) |
| AgentList | Snapshot of the supervisor's agent registry, including family, bridge, and callable status. | |
| TaskGet | Retrieve the current status, metadata, and full message history for a specific BIAM task. | [ADR-015](wiki/decisions/015-bidirectional-inter-agent-messaging.md) |
| TaskWait | Block the caller until a BIAM task reaches a terminal state (done, failed, or cancelled). | [ADR-015](wiki/decisions/015-bidirectional-inter-agent-messaging.md) |
| TaskList | Enumeration of recent BIAM task history for the current supervisor instance. | [ADR-015](wiki/decisions/015-bidirectional-inter-agent-messaging.md) |
| TaskNotify | Edge-triggered notification that wakes the caller as soon as any watched task completes. | [ADR-015](wiki/decisions/015-bidirectional-inter-agent-messaging.md) |

### Rules engine
Predicate-based invariants the operator declares in `.clawtool/rules.toml` and enforces at lifecycle events (pre-commit, post-edit, session-end, pre-send, pre-unattended). Pure in-process Go evaluation against a typed Context — no shell roundtrip.

| Tool | Capability | Reference |
|---|---|---|
| RulesCheck | Evaluate `.clawtool/rules.toml` against a Context (event + changed paths + commit message + tool calls + args). Returns Verdict with passed/warned/blocked per rule. | [docs/rules.md](docs/rules.md) |

### Authoring scaffolders
Authoring tools provide agents and users with the ability to create new capabilities on the fly. Whether it is a new subagent persona, an agentskills.io skill, or a complete MCP server, these scaffolders ensure that new additions follow project conventions.

| Tool | Capability | Reference |
|---|---|---|
| AgentNew | Scaffold a new subagent persona and dispatcher manifest. | |
| SkillNew | Generate a new agentskills.io-standard skill directory with SKILL.md and assets. | |
| McpList | Walk and discover local MCP server projects based on the `.clawtool/mcp.toml` marker. | [ADR-019](wiki/decisions/019-mcp-authoring-scaffolder.md) |
| McpNew | Scaffold a fresh, compilable MCP server project in Go, Python, or TypeScript. | [ADR-019](wiki/decisions/019-mcp-authoring-scaffolder.md) |
| McpRun | Development-mode runner for local MCP projects speaking over stdio. | [ADR-019](wiki/decisions/019-mcp-authoring-scaffolder.md) |
| McpBuild | Packaging and build tool for MCP server projects (binary, npm, or Docker). | [ADR-019](wiki/decisions/019-mcp-authoring-scaffolder.md) |
| McpInstall | Build and register a local project as an aggregated MCP source in config.toml. | [ADR-019](wiki/decisions/019-mcp-authoring-scaffolder.md) |

### Browser + Portal
Browser and Portal tools enable agents to interact with modern web applications and authenticated UI targets. The Obscura engine handles complex JavaScript rendering, while Portals allow for saved, repeatable interaction flows with saved auth state.

| Tool | Capability | Reference |
|---|---|---|
| BrowserFetch | Stateless rendering of hydrated SPA content using the Obscura V8+CDP headless engine. | [ADR-017](wiki/decisions/017-browser-tools-not-transport.md) |
| BrowserScrape | Bulk parallel rendering and JS-expression extraction across multiple URLs. | [ADR-017](wiki/decisions/017-browser-tools-not-transport.md) |
| PortalList | Enumerate saved web-UI targets, including base URLs and authenticated login state. | [ADR-018](wiki/decisions/018-portal-feature.md) |
| PortalAsk | Drive a saved portal through its login flow and interaction predicate to extract data. | [ADR-018](wiki/decisions/018-portal-feature.md) |
| PortalUse | Set the sticky-default portal for subsequent PortalAsk calls in the current context. | [ADR-018](wiki/decisions/018-portal-feature.md) |
| PortalWhich | Identify the active portal resolving via environment variables or sticky config files. | [ADR-018](wiki/decisions/018-portal-feature.md) |
| PortalUnset | Clear the current sticky portal selection for the project or global context. | [ADR-018](wiki/decisions/018-portal-feature.md) |
| PortalRemove | Delete a portal configuration and its associated secrets from the local store. | [ADR-018](wiki/decisions/018-portal-feature.md) |

### Sandbox
Sandbox tools manage the isolation of agent operations on the host system. By defining profiles that restrict filesystem access, network connectivity, and resource consumption, clawtool provides a layer of security over powerful agent tools.

| Tool | Capability | Reference |
|---|---|---|
| SandboxList | List configured sandbox profiles and their isolation policy types. | [ADR-020](wiki/decisions/020-sandbox-feature.md) |
| SandboxShow | Display the detailed path, network, env, and resource constraints of a specific profile. | [ADR-020](wiki/decisions/020-sandbox-feature.md) |
| SandboxDoctor | Diagnostic report on host engine support for bwrap, sandbox-exec, or docker fallback. | [ADR-020](wiki/decisions/020-sandbox-feature.md) |

### Bridges + Recipes
Bridges and Recipes manage the connection to other agent ecosystems and the application of project-wide standards. Bridges wire up external CLIs, while Recipes inject canonical configuration for CI, linting, and release management.

| Tool | Capability | Reference |
|---|---|---|
| BridgeList | List installable and active bridges to agent CLIs with current install state. | |
| BridgeAdd | Install a canonical bridge for a supported agent family (codex, opencode, gemini, hermes). | [ADR-014](wiki/decisions/014-clawtool-relay-and-cli-multiplexer.md) |
| BridgeRemove | Uninstall an agent bridge and clear sticky instance pointers. | |
| BridgeUpgrade | Refresh a bridge to its latest version and re-run its registration. | |
| RecipeList | Enumerate available project-setup recipes (governance, CI, release, etc.). | |
| RecipeStatus | Report on the detection and application state of recipes for the current repo. | |
| RecipeApply | Execute a recipe to inject canonical configuration and upstream glue. | |

## Architecture

```
agent (Claude / Codex / Gemini / Opencode)
    │  MCP (stdio)
    ▼
clawtool serve
├── core tools (Bash, Read, Edit, ...)        ← timeout-safe / structured / format-aware
├── BIAM dispatch + TaskNotify fan-in         ← async multi-agent supervision
├── sandbox profiles (bwrap / sandbox-exec / docker)
├── portals (saved web-UI targets)
├── MCP scaffolder (Mcp* tools)
└── aggregated MCP source servers (github, slack, postgres, …)
```

clawtool operates as a centralized tool and agent supervisor. It aggregates native capabilities, third-party MCP sources, and other coding-agent CLIs into a single, cohesive surface. By wrapping every operation in a timeout-safe and structured-output layer, clawtool ensures that agents never hang on a shell child and always receive parseable responses.

The project adheres to a **three-plane shipping contract** ([docs/feature-shipping-contract.md](docs/feature-shipping-contract.md)) to maintain consistency across the entire ecosystem. Every new feature or tool must land in three places within the same commit:
1.  **MCP Plane**: The core logic and tool registration on the MCP server.
2.  **Marketplace Plane**: The surface area exposed to the Claude Code marketplace (slash commands and manifest).
3.  **Skill Plane**: The routing-bias and prompt-engineering rows that teach the model how and when to use the new feature.

This supervisor-orchestrator pattern ([ADR-016](wiki/decisions/016-supervisor-orchestrator-pattern.md)) allows clawtool to act as the "nervous system" for AI coding agents, wiring together specialized capabilities with a uniform delivery guarantee.

## Configuration
- `~/.config/clawtool/config.toml` — The primary configuration file (XDG compliant) where you manage core tool status, source instances, agent supervisor policies, and global hooks.
- `~/.config/clawtool/secrets.toml` — A dedicated, mode-0600 credential store for API keys, database passwords, and OAuth tokens. Keeping secrets separate allows `config.toml` to be safely committed to public dotfile repositories.
- `~/.local/share/clawtool/biam.db` — The persistent SQLite database for the Bidirectional Inter-Agent Messaging (BIAM) protocol, storing task status, message history, and signed Ed25519 identity envelopes.
- `./.clawtool/<name>.toml` — Project-scoped overrides and metadata markers (e.g., `mcp.toml`, `brain.toml`) used for repository-specific sandbox profiles, portals, and authoring state.

You can inspect and troubleshoot your environment using the diagnostic suite: `clawtool doctor` for a general host health check, `clawtool tools list` for per-selector tool resolution, `clawtool source check` for credential verification, and `clawtool sandbox show <profile>` to see active isolation constraints.

## Roadmap
- **ADR-022 Checkpoint** (drafting) — Commit core tool with Conventional Commits validation, autocommit, and dirty-tree guards.
- **ADR-023 Unattended mode** (planned) — Background supervisor to keep agents productive between user prompts.
- **ADR-024 A2A networking** (planned) — Agent-to-Agent protocol with mDNS discovery and Tailscale tsnet integration.
- **Tool Manifest Registry refactor** (planned) — Consolidation of hand-maintained tool registrations into a typed registry.
- **Hermes-agent bridge** (planned for v0.20) — Integration with the NousResearch agent ecosystem.
- **Aider + Goose bridges** (radar) — Candidates for v0.21 bridge expansions.

## Contributing
Refer to [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/feature-shipping-contract.md](docs/feature-shipping-contract.md) for development guidelines.

We strictly enforce the no-Co-Authored-By rule for AI agents and require all changes to satisfy the three-plane review checklist.

## License
[MIT](LICENSE)
