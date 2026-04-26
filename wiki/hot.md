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

- **clawtool's positioning has TWO pillars**: (1) canonical core tools — bash/grep/read/edit/write/glob/webfetch shipped at quality higher than each agent's native built-in; goal is agents preferring clawtool over their own implementations ([[005 Positioning replace native agent tools]]). (2) search-first — `tool_search` primitive (deferred loading + semantic discovery), the prerequisite that lets a 50+ tool catalog scale.
- **Multi-instance + naming locked** ([[006 Instance scoping and tool naming]]): instance names are kebab-case (`github-personal`, `github-work`), tool names from sources are snake_case (`create_issue`). Wire form `<instance>__<tool>` (two underscores), CLI selector `<instance>.<tool>` (one dot). Core tools use PascalCase (`Bash`, `Read`, `Edit`) matching Claude's native convention. No collision possible — disjoint charsets. First-instance bare name allowed; second instance forces explicit rename.
- **Engineering discipline** ([[007 Leverage best-in-class not reinvent]]): clawtool wraps mature engines (ripgrep for Grep, defuddle/Readability for WebFetch, OpenAI apply_patch format for Edit, doublestar for Glob) and adds a uniform polish layer (timeout-safe, structured JSON, secret redaction, MCP correctness). Reimplementing from scratch only when no upstream meets the bar. ToolSearch is the one thing we genuinely build (nobody ships it). See [[Canonical Tool Implementations Survey 2026-04-26]] for per-tool engine choices.
- **Language locked: Go. License: MIT** (LICENSE file in repo root). Both top-level open questions closed.
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

- ✅ **v0.3 SHIPPED**. Two new core tools wrapping best-in-class engines per [[007 Leverage best-in-class not reinvent]]:
  - **Grep** wraps ripgrep (`~/.local/bin/rg` 15.1.0 musl static, MIT-or-Unlicense) with system grep fallback. Uniform output (`matches[], matches_count, truncated, engine, duration_ms, cwd`). 5 unit tests + 5 e2e assertions green; engine == "ripgrep" verified end-to-end.
  - **Read** uses stdlib bufio for text, pdftotext shell-out for PDF (gracefully reports missing engine), native ipynb JSON parse. Stable line-based cursors (1-indexed inclusive), deterministic `total_lines`, 5 MiB content cap, binary-rejected for unknown formats. 6 unit tests + 5 e2e assertions green.
  - Detection layer at `internal/tools/core/engines.go` (sync.Once cache, `LookupEngine("rg"|"grep"|"pdftotext")`).
- ✅ **Test totals across all of clawtool**: 16 unit tests in `tools/core` (Bash 5 + Grep 5 + Read 6) + 11 config + 8 cli + 23 e2e = **58 green tests**.
- ✅ **v0.2 PROTOTYPE WORKING**. (See [[Prototype Bringup 2026-04-26]] for the v0.1+v0.2 baseline.)
- ✅ **Closed**: language → **Go**, license → **MIT** (LICENSE in repo).
- **Open**: ranking model for `tool_search` (BM25 vs embedding vs hybrid). Prototype with BM25 first.
- **Open**: catalog format — define clawtool-native or read existing (Docker MCP Catalog, MCP Registry, Smithery)? Defer until 5+ instance types.
- **Deferred to v2**: container isolation, middleware support, plugin packaging (Claude Code plugin, Codex plugin) — phase 2 after binary feature-complete.
- **v0.2 shipped**: (1) `~/.config/clawtool/config.toml` read/write ✅ — TOML schema per ADR-006 (core_tools, tools, sources, tags, groups, profile); (2) CLI subcommands (`init`, `tools list/enable/disable/status`, `version`, `help`) ✅ — selector validation (PascalCase or kebab-case `.` snake_case); (3) Makefile (`build`, `test`, `e2e`, `install` atomic, `lint`, `dist`) ✅; (4) LICENSE (MIT) + README.md ✅.
- **v0.4 increments**: (1) **Source catalog** per [[008 Curated source catalog]] — `clawtool source add github` resolves to `@modelcontextprotocol/server-github` from a built-in TOML catalog with required-env hints and auth flow; (2) tag + group resolution in config (full ADR-004 §4 precedence); (3) `ToolSearch` BM25 baseline via `bleve`; (4) hot-reload config watcher; (5) `Edit` + `Write` core tools using OpenAI `apply_patch` format.
- **Pending user-side**: work account `gh auth login` with `GH_CONFIG_DIR=~/.config/gh-work` (not blocking clawtool).
- **Next deliverable revised**: prototype of (a) MCP server stub, (b) **3-5 canonical core tools** (bash, ripgrep, read at minimum) at quality, (c) `tool_search` BM25 baseline, (d) `clawtool tools enable/disable` CLI. *Not* a full aggregator. Make it usable end-to-end on small surface, then expand.
