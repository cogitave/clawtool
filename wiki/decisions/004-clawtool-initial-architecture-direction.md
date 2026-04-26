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

### 4. Configuration UX: CLI-first, declarative file fallback, hot-reload

**Decision**:
- Primary: CLI with dot-notation, modeled on `docker mcp profile tools`. Examples:
  ```bash
  clawtool tools enable ripgrep.search
  clawtool tools disable github.delete_repo
  clawtool profile create personal
  clawtool profile use personal
  ```
- Secondary: declarative TOML or JSON file (`~/.config/clawtool/config.toml`), discoverable per-directory via `.clawtoolrc` or via a `[clawtool]` section in `.envrc`-style env. Hot-reload supported (1mcp-agent precedent).
- Tertiary: GUI — out of scope for v1. Anyone who wants GUI uses mcp-router on top.

**Rationale**: Terminal-driven AI coding workflows want CLI. docker-mcp-gateway's dot-notation is the gold standard for ergonomics. Declarative file ensures reproducibility (commit `.clawtoolrc` to a project = teammates get same toolset). Hot-reload is table stakes.

**Consequence**: GUI users are explicitly redirected to mcp-router as a complement, not a competitor. clawtool ≠ mcp-router replacement.

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

## Consequences

- We are building a new project, not contributing upstream. Cost: real implementation work. Benefit: control over the search-first primitive without fighting an existing codebase.
- "Search-first" is the project's identity. Every architectural choice should be checked against "does this make tool search work better?"
- Backward compatibility with existing MCP servers is a hard constraint. We extend, never replace, the MCP tool schema.
- We will not match docker-mcp-gateway's security posture out of the box. Documentation must be honest about this.
- The next deliverable is a **prototype of the search index + `tool_search` primitive**, not a full-featured aggregator. Aggregation is solved; search is not.

## Status

Developing. Pending: language choice, license, prototype plan.
