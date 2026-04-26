---
type: decision
title: "004 clawtool initial architecture direction"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - architecture
  - spec
status: developing
related:
  - "[[Universal Toolset Projects Comparison]]"
  - "[[Agent-Agnostic Toolset]]"
  - "[[1mcp-agent]]"
  - "[[docker-mcp-gateway]]"
  - "[[metamcp]]"
sources:
  - "[[Research Scope 2026-04-26]]"
---

# 004 — clawtool Initial Architecture Direction

> **Status: developing.** This ADR captures direction, not final spec. It locks in answers where the research is conclusive and explicitly leaves the genuinely-open questions open.

## Context

After surveying the universal-toolset / MCP-aggregator landscape ([[Universal Toolset Projects Comparison]]), four open spec questions remain:

1. Distribution mechanism
2. "Search-first" interpretation
3. Tool manifest format
4. Configuration UX
5. (added) Build-new vs extend-existing

## Decisions

### 1. Distribution: MCP-native, single-binary, no Docker requirement

**Decision**: clawtool exposes itself as an MCP server. Distribution is a single user-local binary (`~/.local/bin/clawtool` style — same path family as `direnv`, `gh`, `claude`). No Docker, no daemon, no GUI required for core operation.

**Rationale**: All four candidates speak MCP — it is the standard. Docker is the heaviest dependency among credible candidates and is a non-trivial install for many WSL / minimal Linux developers. 1mcp-agent already proves single-binary works at this layer. Containerization can be opt-in for users who want it (read existing Docker MCP catalog as a *source*, not a runtime requirement).

**Consequence**: clawtool will not match docker-mcp-gateway's container isolation. Security trade is documented and intentional. Users who need isolation install docker-mcp-gateway.

### 2. "Search-first" means: deferred tool loading + semantic discovery

**Decision**: clawtool will implement "search-first" as a two-part feature:

- **Deferred tool loading**: tool schemas are not pushed to the agent at handshake; agent calls a `tool_search` primitive to discover tools, then schemas are loaded on demand. Models the Claude Code `ToolSearch` pattern.
- **Semantic discovery**: `tool_search` accepts natural-language queries ("how do I read a file?"), not just substring filters. Returns ranked tool candidates with brief descriptions.

**Rationale**: This is the genuine industry gap. metamcp lists Elasticsearch-for-MCP on the roadmap; nobody ships it. Deferred loading directly attacks the context-window problem (Claude Code already pioneered this internally — see [[Agent-Agnostic Toolset]] for the three "search-first" interpretations and why we're picking this one).

**Consequence**: clawtool ships its own search index. Spec needs to define: ranking model (BM25? embedding? hybrid?), index update lifecycle, query semantics.

### 3. Tool manifest: extend MCP tool schema with clawtool fields

**Decision**: clawtool **does not invent** a new tool manifest. It uses the MCP tool schema (`name`, `description`, `inputSchema`) as the floor. clawtool-specific fields are added as optional extensions in a `clawtool` namespace inside annotations, e.g.:

```json
{
  "name": "ripgrep_search",
  "description": "Search file contents with regex",
  "inputSchema": {...},
  "annotations": {
    "clawtool": {
      "tags": ["search", "files"],
      "stability": "stable",
      "default_enabled": true,
      "search_keywords": ["grep", "rg", "find text"]
    }
  }
}
```

**Rationale**: Inventing a new format breaks compatibility with every existing MCP server. Annotation extension is forward-compatible — agents that don't understand `annotations.clawtool` see standard MCP and proceed.

**Consequence**: clawtool's added value lives in the annotations layer (search ranking, default-enable, stability tier), not in incompatible schema changes.

### 4. Configuration UX: CLI-first, declarative file fallback, hot-reload, multi-level selectors

**Decision**:
- Primary: CLI with dot-notation, modeled on `docker mcp profile tools` but extended with **multi-level tool selectors** (server, tool, tag, group):
  ```bash
  # Tool-level (most specific) — operates on one tool
  clawtool tools enable ripgrep.search
  clawtool tools disable github.delete_repo

  # Server-level — operates on every tool of a server
  clawtool tools disable github                    # all github.* off
  clawtool tools enable ripgrep                    # all ripgrep.* on

  # Tag-level — operates on every tool with a tag (annotation-driven)
  clawtool tools disable tag:destructive           # cuts across servers
  clawtool tools enable tag:read-only

  # Group-level — operates on a user-defined named set
  clawtool group create review-set ripgrep github.create_issue github.list_pulls
  clawtool tools enable group:review-set

  # Profile (orthogonal) — bundles a complete enable/disable state
  clawtool profile create personal
  clawtool profile use personal
  ```
- Secondary: declarative TOML or JSON file (`~/.config/clawtool/config.toml`), discoverable per-directory via `.clawtoolrc` or via a `[clawtool]` section in `.envrc`-style env. Hot-reload supported (1mcp-agent precedent).
- Tertiary: GUI — out of scope for v1. Anyone who wants GUI uses mcp-router on top.

#### Selector resolution & precedence

Selectors compose. When a tool's effective state is computed, layers are evaluated in this order, with **later layers overriding earlier ones**:

1. **Server-level rule** (`github`)
2. **Tag-level rule** (`tag:destructive`)
3. **Group-level rule** (`group:review-set`)
4. **Tool-level rule** (`github.delete_repo`) — most specific, wins last

So: `tools disable github` then `tools enable github.create_issue` leaves only `create_issue` enabled, the rest of `github.*` disabled. Mental model: each level is an override of the previous.

**Conflict tie-breaker** at the same level (e.g., two tag rules covering the same tool): **deny wins** — explicit `disable` overrides explicit `enable`. Safety default; prevents accidental re-enable of destructive tools via tag overlap.

The `clawtool tools status <selector>` command resolves and prints the effective state plus which rule won, for inspection.

**Rationale**: Real config workflows mix scopes. "Disable everything destructive across all servers" (tag), "enable a curated PR-review set for this project" (group), "turn this one tool off" (tool), "give me everything from this server" (server) — all common. Forcing users to enumerate one-tool-at-a-time is the docker-gateway weakness; forcing server-only enable is the 1mcp-agent weakness. Tags + groups + clear precedence cover the gap.

**Consequence**: clawtool's tool manifest must carry tag information (already specified — `annotations.clawtool.tags` in decision 3). Group definitions are clawtool-state, stored in profile config. The status/inspect command becomes essential for users to debug "why is this tool enabled?".

GUI users redirected to mcp-router as complement, not competitor. clawtool ≠ mcp-router replacement.

### 5. Build new vs extend 1mcp-agent: build new, but borrow shamelessly

**Decision**: clawtool is a new project. We do **not** fork 1mcp-agent.

**Rationale**:
- Architectural fit: search-first changes the core handshake; bolting it onto 1mcp-agent is more work than starting clean.
- License clarity: starting fresh with a known license (MIT or Apache 2.0 — TBD) avoids fork inheritance ambiguity.
- Identity: a focused project with a single distinguishing primitive (search-first) is easier to communicate than "1mcp-agent fork with extra features."

**Borrow from 1mcp-agent**:
- Single-binary npx + native distribution model
- Hot-reload config pattern
- Multi-agent CLI setup commands (`cli-setup --codex|--claude`)
- OAuth 2.1 reference for any future remote serving

**Borrow from docker-mcp-gateway**:
- Dot-notation CLI ergonomics
- Profile concept
- Catalog source resolution (read existing Docker MCP Catalog entries)

**Borrow from metamcp**:
- Per-tool override UX (description / name overrides)
- Middleware concept (kept as v2 extension; not in v1)

### 6. Distribution & Usage Scenarios

clawtool ships in **two layers**, used independently or together.

#### Layer 1 — The binary (the actual product)

- Single user-local executable installed via `npm i -g @clawtool/clawtool`, `brew install clawtool`, or `curl …/install.sh` (binary release).
- Lives at `~/.local/bin/clawtool` (user-scope, no sudo) by default.
- Speaks **MCP over stdio** (default) or **Streamable HTTP**.
- Reads state from `~/.config/clawtool/config.toml` (global) + optional `.clawtoolrc` (project-scope, hot-reloaded).
- Nothing about Layer 1 is agent-specific — it's a generic MCP server. Any MCP client can connect.

#### Layer 2 — Per-agent plugins (convenience wrappers)

- **Claude Code**: `claude plugin install clawtool@…` — auto-installs the binary if missing, registers the MCP server in CC's config, adds slash commands like `/clawtool-tools-enable`, `/clawtool-tools-status`.
- **Codex CLI**: same pattern via Codex's plugin marketplace.
- **Cursor / Windsurf / others**: future per-agent plugins follow the same recipe.
- Plugins **do not fork state**. They are thin install + registration helpers. All agents continue to read from the single `~/.config/clawtool/`.

#### Three usage scenarios

| Scenario | Who | Steps |
|---|---|---|
| **A. Power-user / minimal** | Wants control, OK editing config files | `npm i -g @clawtool/clawtool` → edit `~/.config/clawtool/config.toml` → `claude mcp add clawtool …` (and/or `codex mcp add …`) once per agent |
| **B. Claude-Code-only** | Wants zero-friction CC integration | `claude plugin install clawtool@…` → done. Plugin handles binary install + MCP registration + slash commands. |
| **C. Multi-agent** | Uses CC + Codex + others in parallel | Install binary once (Scenario A's first step) → install per-agent plugin where available, fall back to manual `mcp add` elsewhere. **Single source of truth at `~/.config/clawtool/`.** |

#### Key invariant

State (which tools are enabled, profiles, groups, tag rules) lives in **one place per device**: `~/.config/clawtool/`. Agents are thin readers. Running `clawtool tools enable github.create_issue` from any terminal — or toggling via a plugin slash command in CC — propagates to **every** connected agent instantly via hot-reload.

This is what "install once, use everywhere" means concretely: not "the binary is portable" (any binary is) but "the configuration is shared." Switching agents does not switch toolsets.

---

## Alternatives Rejected

- **Fork 1mcp-agent** — see decision 5.
- **GUI-first** — terminal AI coding workflows make this the wrong primary target. mcp-router covers GUI users.
- **Docker-required** — kills install simplicity for WSL / minimal-Linux users.
- **Invent new tool manifest** — breaks every existing MCP server.
- **Plain substring search instead of semantic** — leaves the actual industry gap unfilled; defeats the differentiation.

## Open (Explicitly Deferred)

- Ranking model for `tool_search` (BM25 vs embedding vs hybrid) — needs prototype.
- Container isolation as optional layer — v2 question.
- Middleware support — v2 question.
- Catalog format ownership — should clawtool define a clawtool-native catalog, or read others (Docker MCP Catalog, MCP Registry, Smithery)?
- License choice — Apache 2.0 vs MIT vs something else.
- Implementation language — Go (single static binary, easy cross-compile), Rust (best for the same), TypeScript (fastest dev iteration but heavier dist).
- Selector grammar finalization — current draft (`server`, `server.tool`, `tag:X`, `group:Y`, `profile use`) needs validation against real workflows. Possible additions: `negation` (`!github.delete_repo`), `wildcards` (`github.list_*`).

## Consequences

- We are building a new project, not contributing upstream. Cost: real implementation work. Benefit: control over the search-first primitive without fighting an existing codebase.
- "Search-first" is the project's identity. Every architectural choice should be checked against "does this make tool search work better?"
- Backward compatibility with existing MCP servers is a hard constraint. We extend, never replace, the MCP tool schema.
- We will not match docker-mcp-gateway's security posture out of the box. Documentation must be honest about this.
- The next deliverable is a **prototype of the search index + `tool_search` primitive**, not a full-featured aggregator. Aggregation is solved; search is not.

## Status

Developing. Pending: language choice, license, prototype plan.
