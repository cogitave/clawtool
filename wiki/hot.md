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

- ✅ **v0.7 SHIPPED** — Web tier: `WebFetch` + `WebSearch`.
  - **`WebFetch`** wraps stdlib `net/http` for transport + `go-readability` for HTML extraction (same engine `Read` uses for `.html`). 30s default timeout (max 120s), 10 MiB body cap, `clawtool/0.7` UA, content-type-aware dispatch (html→readability, text/*→passthrough, binary→reject). Output: `{url, final_url, status, format, engine, title, byline, content, size_bytes, fetched_at, duration_ms, truncated}`. Live-tested against example.com (title="Example Domain", 79ms).
  - **`WebSearch`** has a pluggable Backend interface; v0.7 ships **Brave** (free-tier-friendly, no card required, well-documented). Backend resolution: `secrets[websearch].backend` → env `CLAWTOOL_WEBSEARCH_BACKEND` → default `brave`. API key from `secrets[websearch].BRAVE_API_KEY` or env. Output: `{query, results[{title,url,snippet}], backend, duration_ms, ...}`. Missing-key error guides user to set BRAVE_API_KEY + provides the exact `clawtool source set-secret` command. Tavily / SearXNG / Google land in v0.8.
  - **CoreToolDocs** updated: `search the web for results` → ToolSearch ranks **WebSearch** top (score 0.89), `fetch a URL` → **WebFetch** top. Pillar #2 (search-first deferred-loading) remains coherent across the new surface.
  - **Tests**: 11 new (WebFetch 6: html+plaintext+binary+redirect+scheme-reject+timeout; WebSearch 5: brave happy path + missing-key sentinel + 4xx response + unknown-backend + stripHTML helper) + 6 new e2e assertions (tools/list +2, WebFetch scheme-reject, WebSearch missing-key error). **Test totals: 99 Go unit + 50 e2e = 149 green.**
- ✅ **v0.6 SHIPPED** — `Read` expanded to 9 formats (was 4: text/pdf/ipynb/binary).
  - **`.docx`** via pandoc shell-out — universal office converter (Microsoft/NASA/academia ship it everywhere); GPL but no Go linkage so clawtool's MIT stays clean.
  - **`.xlsx`** via `github.com/xuri/excelize/v2` (BSD-3) — pure Go, no CGO, Microsoft/Alibaba/Oracle production-tested; per-sheet rendering with `sheets[]` metadata so the agent pages workbook with `sheet="Sheet2"`.
  - **`.csv` / `.tsv`** via stdlib `encoding/csv` — header-aware preview, pipe-rendered rows, total-rows footer, `LazyQuotes` + `FieldsPerRecord=-1` so ragged real-world files don't abort.
  - **`.html`** via `github.com/go-shiori/go-readability` (Apache-2.0) — Mozilla Readability port, strips nav/ads/footer chrome, surfaces title + byline + article body. Sniff-detect for files without `.html` extension.
  - **`.json` / `.yaml` / `.toml` / `.xml`** — text passthrough with format tag (already readable; we just identify the format).
  - Files split into `read_legacy.go` (PDF + ipynb), `read_office.go` (docx + xlsx), `read_structured.go` (csv/tsv), `read_html.go`. Public API unchanged.
  - **CoreToolDocs** updated: Read description and keywords list every supported format so ToolSearch ranks Read for "open spreadsheet", "extract docx", "parse csv", etc.
  - **Test totals**: **88 Go unit + 46 e2e = 134 green**. New: read_office 2, read_structured 5 (csv 1 + tsv 1 + structured passthrough 4 subtests in 1 test = 2 tests; with two-test additions), read_html 2, e2e Read multi-format 8. Read base test count refactored to 7 tests (signature change to add `sheet` parameter).
- ✅ **v0.5 SHIPPED** — `ToolSearch` + `Glob` core tools.
  - **`ToolSearch`** is clawtool's identity primitive (ADR-005): bleve BM25 in-memory index built at `clawtool serve` startup from every enabled core tool + every aggregated source tool. `name^3`/`keywords^2`/`description^1` field boosts so literal-name lookups still rank above semantic neighbors. Output: `{query, results[{name,score,description,type,instance}], total_indexed, engine:"bleve-bm25", duration_ms}`. `type` filter (`core`/`sourced`/`any`) and `limit` cap. End-to-end verified: "search file contents regex" → Grep top (≈0.94), "echo back input" with stub source live → `stub__echo` top (≈1.24).
  - **`Glob`** wraps `bmatcuk/doublestar/v4` (ADR-007). `**` double-star supported. Forward-slash output for platform stability. Streaming match via `GlobWalk`. Hard cap (default 1000, max 10000) protects agent context. Engine field exposes backend choice.
  - **Server boot order** updated: load config+secrets → start sources.Manager → build search.Index from descriptors of enabled cores + aggregated source tools → register cores filtered by config.IsEnabled → register source tools with manager-routed handlers.
  - **`KnownCoreTools`** now `[Bash, Glob, Grep, Read, ToolSearch]`. `clawtool tools list` shows all five.
  - **Test totals**: **81 Go unit + 38 e2e = 119 green**. New: search 11, glob 5, e2e ToolSearch+Glob 8.
- ✅ **v0.4 turn 2 SHIPPED** — MCP client/server proxy. clawtool now spawns each configured source as a child MCP server, aggregates its tools under `<instance>__<tool>` wire names per ADR-006, and routes tools/call to the right child. Server.go integrates `internal/sources/Manager` which uses `mark3labs/mcp-go/client.NewStdioMCPClient` (ADR-007 wrap-don't-reinvent again). Health states tracked: Starting / Running / Down / Unauthenticated. Stub-server fixture at `test/e2e/stub-server/` verifies the loop end-to-end without external dependencies. Manager.Start failures on individual sources are non-fatal (others continue). Disabled core tools correctly absent from tools/list (config.IsEnabled gate). Tests: 7 sources unit (subprocess-spawning), 6 new e2e proxy assertions. Smoke: `clawtool serve` with `[sources.stub] command=[stub-server]` exposes `Bash`, `Grep`, `Read`, `stub__echo` in tools/list; calling `stub__echo {text:"hello-routing"}` returns `"echo:hello-routing"` routed through the proxy.
- ✅ **v0.4 turn 1 SHIPPED** — source catalog + secrets store + source CLI per [[008 Curated source catalog]].
  - **Catalog** at `internal/catalog/`. 12 entries embedded via go:embed (github, slack, postgres, sqlite, filesystem, fetch, brave-search, google-maps, memory, sequentialthinking, time, git). Per-runtime `ToSourceCommand()` (npx / node / python via uvx / docker / binary). `SuggestSimilar` is bidirectional substring (catches both "git" → "github" and "github-typo" → "github"). 11 unit tests.
  - **Secrets** at `internal/secrets/`. TOML store at `~/.config/clawtool/secrets.toml` (mode 0600, separate from config.toml so config can be safely committed). Scope-based (`<instance> | "global"`) with global fallback. `Resolve()` interpolates `${VAR}` against secrets first, then process env. Atomic temp+rename save. 7 unit tests.
  - **CLI source subcommands** at `internal/cli/source.go`: `add`, `list`, `remove`, `set-secret`, `check`. `clawtool source add github` resolves bare names, prints package + description + homepage + auth hint with copy-paste set-secret command for missing env. `--as <instance>` lets users add multiple instances of the same source per ADR-006. Bidirectional flag-position support (flags can come after positionals via `reorderFlagsFirst` helper). 12 unit tests.
- ✅ **v0.3 SHIPPED** — Grep + Read core tools per [[007 Leverage best-in-class not reinvent]].
- ✅ **Test totals: 99 Go unit + 50 e2e = 149 green.** Per package: catalog 11, cli 21, config 11, search 11, secrets 7, sources 13, tools/core 40 (Bash 5 + Glob 5 + Grep 5 + Read 7 + Read-Office 2 + Read-Structured 5 + Read-HTML 2 + WebFetch 6 + WebSearch 5 + stripHTML/resolveBackend +helpers).
- ✅ **v0.2 PROTOTYPE WORKING**. (See [[Prototype Bringup 2026-04-26]] for the v0.1+v0.2 baseline.)
- ✅ **Closed**: language → **Go**, license → **MIT** (LICENSE in repo).
- **Open**: ranking model for `tool_search` (BM25 vs embedding vs hybrid). Prototype with BM25 first.
- **Open**: catalog format — define clawtool-native or read existing (Docker MCP Catalog, MCP Registry, Smithery)? Defer until 5+ instance types.
- **Deferred to v2**: container isolation, middleware support, plugin packaging (Claude Code plugin, Codex plugin) — phase 2 after binary feature-complete.
- **v0.2 shipped**: (1) `~/.config/clawtool/config.toml` read/write ✅ — TOML schema per ADR-006 (core_tools, tools, sources, tags, groups, profile); (2) CLI subcommands (`init`, `tools list/enable/disable/status`, `version`, `help`) ✅ — selector validation (PascalCase or kebab-case `.` snake_case); (3) Makefile (`build`, `test`, `e2e`, `install` atomic, `lint`, `dist`) ✅; (4) LICENSE (MIT) + README.md ✅.
- **v0.6 increments** (priority): (1) **`WebFetch`** core tool — `go-readability` / `defuddle` wrap, URL canonicalization, citation metadata; (2) **`WebSearch`** with pluggable backends (Brave / Tavily / SearXNG) and source-allowlist polish; (3) **`Edit`** + **`Write`** core tools — OpenAI `apply_patch` format wrap, atomic temp+rename, line-ending preserve; (4) **gitignore-aware Glob** via `sabhiram/go-gitignore`; (5) `clawtool source available` — list catalog entries not yet added; (6) `clawtool tools list --include-sources` — runtime-aware listing.
- **v0.8 increments** (priority): (1) **`Edit`** + **`Write`** core tools — OpenAI `apply_patch` format wrap, atomic temp+rename, line-ending preserve, conflict detection; (2) **WebSearch additional backends** — Tavily + SearXNG; (3) **WebFetch cache** — in-memory LRU keyed on canonical URL; (4) **gitignore-aware Glob** via `sabhiram/go-gitignore`.
- **v0.9+ increments**: (1) `source list --runtime` + watcher for live Manager.Health(); (2) auto-restart-on-crash; (3) tag + group resolution in config (full ADR-004 §4 precedence); (4) hot-reload config watcher; (5) ToolSearch embedding rerank for top-K; (6) long-form `clawtool source add custom -- <command>`; (7) `source rename` workflow; (8) Read OCR (tesseract); (9) Read media metadata (ffprobe).
- **Pending user-side**: work account `gh auth login` with `GH_CONFIG_DIR=~/.config/gh-work` (not blocking clawtool).
- **Next deliverable revised**: prototype of (a) MCP server stub, (b) **3-5 canonical core tools** (bash, ripgrep, read at minimum) at quality, (c) `tool_search` BM25 baseline, (d) `clawtool tools enable/disable` CLI. *Not* a full aggregator. Make it usable end-to-end on small surface, then expand.
