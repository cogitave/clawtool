---
type: meta
title: "Hot Cache"
created: 2026-04-26
updated: 2026-04-26
tags:
  - meta
  - hot-cache
status: developing
---

# Recent Context

## Last Updated

2026-04-26. **Research phase round 1 done.** Surveyed 4 universal-toolset projects, drafted initial architecture ADR.

## Key Recent Facts

- **clawtool's positioning has TWO pillars**: (1) canonical core tools — bash/grep/read/edit/write/glob/webfetch shipped at quality higher than each agent's native built-in; goal is agents preferring clawtool over their own implementations ([[005 Positioning replace native agent tools]]). (2) search-first — `tool_search` primitive (deferred loading + semantic discovery), the prerequisite that lets a 50+ tool catalog scale. Aggregation/per-tool toggle/single-binary/multi-agent are table stakes; canonical-tool quality + search-first together are what makes clawtool worth building.
- **MCP is locked in.** All four credible candidates speak MCP. clawtool exposes itself as an MCP server; no proprietary protocol.
- **Distribution decided**: single user-local binary (~/.local/bin/clawtool), no Docker required. 1mcp-agent precedent. Trades container isolation (docker-mcp-gateway) for install simplicity.
- **Tool manifest decided**: extend MCP schema via `annotations.clawtool` (tags, stability, default_enabled, search_keywords). No breaking changes to existing MCP servers.
- **Config UX decided**: CLI dot-notation (docker-mcp-gateway-style ergonomics) + declarative TOML/JSON + hot-reload. **Multi-level selectors**: server (`github`), tool (`github.delete_repo`), tag (`tag:destructive`), group (`group:review-set`). Precedence: tool > group > tag > server; **deny wins** at same level. `clawtool tools status` resolves and explains the effective state. GUI is out of scope for v1; mcp-router covers GUI users.
- **Build new, not fork.** Search-first changes core handshake; cleaner to start fresh. Borrow from 1mcp-agent (distribution, hot-reload), docker-mcp-gateway (CLI ergonomics, profiles), metamcp (per-tool override UX).
- **Two distribution layers**: (1) standalone binary `~/.local/bin/clawtool` via npm/brew/curl — the actual product, generic MCP server. (2) per-agent plugins (Claude Code, Codex, …) as thin install+registration wrappers, **no state fork**. All agents read from one `~/.config/clawtool/`. Scenarios: A) power-user manual `mcp add`, B) CC-only plugin install zero-friction, C) multi-agent shared state via single config dir. **"Install once, use everywhere" = shared config, not portable binary.**

## Recent Changes

- Created: [[Research Scope 2026-04-26]], [[mcp-router]], [[1mcp-agent]], [[metamcp]], [[docker-mcp-gateway]], [[Universal Toolset Projects Comparison]], [[004 clawtool initial architecture direction]]
- Updated: [[Index]], [[Log]], [[entities _index]], [[comparisons _index]], [[decisions _index]]

## Active Threads

- **Open**: language choice for clawtool — **weight increased** by ADR-005. Syscall-level reliability for canonical bash/file tools argues for Go or Rust over TypeScript.
- **Open**: license choice (Apache 2.0 vs MIT). MIT slightly preferred for canonical-layer adoption (lower barrier to vendor inclusion).
- **Open**: ranking model for `tool_search` primitive (BM25 vs embedding vs hybrid). Needs prototype.
- **Open**: catalog format — define clawtool-native or read existing (Docker MCP Catalog, MCP Registry, Smithery)?
- **Deferred to v2**: container isolation, middleware support, plugin packaging (Claude Code plugin, Codex plugin) — phase 2 after binary works.
- **Pending user-side**: work account `gh auth login` with `GH_CONFIG_DIR=~/.config/gh-work` (not blocking clawtool).
- **Next deliverable revised**: prototype of (a) MCP server stub, (b) **3-5 canonical core tools** (bash, ripgrep, read at minimum) at quality, (c) `tool_search` BM25 baseline, (d) `clawtool tools enable/disable` CLI. *Not* a full aggregator. Make it usable end-to-end on small surface, then expand.
