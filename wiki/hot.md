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

- **clawtool's distinguishing primitive is search-first** (deferred tool loading + semantic discovery). Every other clawtool feature (aggregation, per-tool toggle, single-binary, multi-agent) is table stakes; search is the gap nobody else fills. metamcp has it on roadmap; nobody ships it. See [[004 clawtool initial architecture direction]].
- **MCP is locked in.** All four credible candidates speak MCP. clawtool exposes itself as an MCP server; no proprietary protocol.
- **Distribution decided**: single user-local binary (~/.local/bin/clawtool), no Docker required. 1mcp-agent precedent. Trades container isolation (docker-mcp-gateway) for install simplicity.
- **Tool manifest decided**: extend MCP schema via `annotations.clawtool` (tags, stability, default_enabled, search_keywords). No breaking changes to existing MCP servers.
- **Config UX decided**: CLI dot-notation (docker-mcp-gateway-style ergonomics) + declarative TOML/JSON + hot-reload. GUI is out of scope for v1; mcp-router covers GUI users.
- **Build new, not fork.** Search-first changes core handshake; cleaner to start fresh. Borrow from 1mcp-agent (distribution, hot-reload), docker-mcp-gateway (CLI ergonomics, profiles), metamcp (per-tool override UX).

## Recent Changes

- Created: [[Research Scope 2026-04-26]], [[mcp-router]], [[1mcp-agent]], [[metamcp]], [[docker-mcp-gateway]], [[Universal Toolset Projects Comparison]], [[004 clawtool initial architecture direction]]
- Updated: [[Index]], [[Log]], [[entities _index]], [[comparisons _index]], [[decisions _index]]

## Active Threads

- **Open**: language choice for clawtool (Go / Rust / TypeScript). Trade-off: dist size + cross-compile vs dev iteration.
- **Open**: license choice (Apache 2.0 vs MIT).
- **Open**: ranking model for `tool_search` primitive (BM25 vs embedding vs hybrid). Needs prototype.
- **Open**: catalog format — define clawtool-native or read existing (Docker MCP Catalog, MCP Registry, Smithery)?
- **Deferred to v2**: container isolation, middleware support.
- **Pending user-side**: work account `gh auth login` with `GH_CONFIG_DIR=~/.config/gh-work` (not blocking clawtool).
- **Next deliverable**: prototype of search index + `tool_search` primitive — not full aggregator. Aggregation is solved; search is the new value.
