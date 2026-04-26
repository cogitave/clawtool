---
type: meta
title: "Log"
created: 2026-04-26
updated: 2026-04-26
tags:
  - meta
  - log
status: developing
---

# Operation Log

Append-only. Newest entries at the **top**. Never edit past entries.

---

## 2026-04-26

### V0.7 SHIPPED — WebFetch + WebSearch (web tier)

clawtool's canonical core list now hits 7 of 7 from ADR-005's tabletop. Web operations route through the same wrap-don't-reinvent discipline.

- **`internal/tools/core/webfetch.go`**: stdlib `net/http` client (UA, redirect, TLS, proxy all stock) + `github.com/go-shiori/go-readability` for HTML body extraction (same engine the Read tool uses for `.html` files — unified extractor across local + web sources). 30s default timeout (max 120s), 10 MiB body cap to protect agent context. Content-type-aware dispatch: `text/html` / `application/xhtml` → readability; `text/*` / `application/{json,yaml,xml,toml}` → passthrough; everything else → structured `binary-rejected` error. Output: `{url, final_url, status, content_type, format, engine, title, byline, site_name, content, size_bytes, fetched_at, duration_ms, truncated, error_reason}`.
- **`internal/tools/core/websearch.go`** + **`websearch_brave.go`**: pluggable `Backend` interface (`Name`, `Search(ctx, query, limit)`). v0.7 ships Brave because it has the most lenient free-tier policy and well-documented JSON. Backend resolution: `secrets[scope=websearch].backend` → `CLAWTOOL_WEBSEARCH_BACKEND` env → default `brave`. API key from same secrets scope or `BRAVE_API_KEY` env. Brave-specific polish: strips `<strong>` / `<b>` markers from snippet `description` so HTML markup doesn't pollute agent context. Output: `{query, results[{title,url,snippet}], results_count, backend, duration_ms, truncated, error_reason}`.
- **CoreToolDocs** entries added — descriptions and keywords picked so ToolSearch surfaces the right tool: `"search the web for results"` → WebSearch top (score 0.89), `"fetch a URL"` / `"download a page"` → WebFetch top.
- **`KnownCoreTools`** now `[Bash, Glob, Grep, Read, ToolSearch, WebFetch, WebSearch]` — 7 entries, matching ADR-005's canonical list.
- **Server.go** registers WebFetch + WebSearch alongside other cores, gated on `config.IsEnabled`. WebSearch receives a reference to the secrets store so the API key is read at call time (key changes don't require restart).
- **Unit tests** (11 new): WebFetch covers HTML readability extraction (httptest fixture serving cluttered article), plain-text passthrough, binary-content rejection, follow-redirect semantics, non-http scheme rejection, timeout. WebSearch covers Brave happy path (httptest mocking the API + asserting headers/query), missing-key sentinel error, 4xx response, unknown-backend error path, the stripHTML helper. Total tests now **99 unit**.
- **E2E** (4 new assertions): tools/list registers WebFetch + WebSearch (now 4 added: Glob, ToolSearch, WebFetch, WebSearch); WebFetch with non-http scheme returns structured error mentioning `http://`; WebSearch without API key returns error mentioning `BRAVE_API_KEY`. Total e2e: **50 assertions**.
- **Live smoke** against `https://example.com`: title="Example Domain", engine=go-readability, 79ms.
- New deps: nothing new — both tools reuse `net/http` stdlib and the existing `go-readability` import. Zero binary growth from this turn.
- Updated [[Hot]], [[Canonical Tool Implementations Survey 2026-04-26]] WebFetch + WebSearch rows ("Adopted v0.7"), this log.

### V0.6 SHIPPED — Read expansion to 9 formats (docx, xlsx, csv/tsv, html, json/yaml/toml/xml)

User-driven: "PDF değil çoklu format okumalı, big-firm-preferred paketlerden faydalanalım." Per ADR-007 we wrap, never reimplement. Big firms picked these specific engines and we adopt the same ones.

- **`.docx` → pandoc** shell-out. Universal office-format converter (Word, OpenOffice, RTF, LaTeX, EPUB), used by Microsoft / NASA / academic publishers. GPL license but no Go linkage means clawtool's MIT stays clean. Absent-engine path returns a structured install-hint error mirroring our PDF pattern.
- **`.xlsx` → `github.com/xuri/excelize/v2`** (BSD-3). Pure Go, no CGO. Used in Microsoft, Alibaba, Oracle production. New `sheet` argument lets the agent page through workbook structure; `Sheets []string` metadata surfaces the full sheet list. Default = first sheet. TSV-style row rendering preserves column boundaries.
- **`.csv` / `.tsv` → stdlib `encoding/csv`**. Header-aware preview with `# columns (N): a | b | c`, pipe-spaced data rows for easy visual scan, `# total data rows: N` footer. `LazyQuotes` + `FieldsPerRecord=-1` for resilience to ragged real-world files.
- **`.html` / `.htm` → `github.com/go-shiori/go-readability`** (Apache-2.0). Go port of Mozilla's Readability.js, the same algorithm Firefox Reader View uses. Strips nav/ads/footer chrome, returns title + byline + sitename + excerpt + article body. Also detected via content sniff for files without `.html` extension.
- **`.json` / `.yaml` / `.toml` / `.xml`** — already human-readable, no engine needed. We just tag the format so agents can branch on `format` field.
- Engine layer updated: `engines.go` now detects `pandoc` alongside `rg`/`grep`/`pdftotext`.
- File layout split for clarity:
  - `read.go` — public surface, format dispatch, ReadResult shape, text engine, format detection, line-range helper.
  - `read_legacy.go` — pdf + ipynb (kept for compatibility).
  - `read_office.go` — docx + xlsx.
  - `read_structured.go` — csv + tsv.
  - `read_html.go` — html.
- `executeRead` signature gained one parameter (`sheet`); existing tests updated mechanically.
- `CoreToolDocs` Read description + keywords expanded so ToolSearch ranks Read for "open spreadsheet", "extract docx", "parse csv", "html article" and similar queries.
- Tests added: Xlsx (in-memory generated via excelize itself, multi-sheet, default + named + unknown-sheet error path), Docx-without-engine, CSV header-aware, TSV tab delimiter, JSON/YAML/TOML/XML passthrough w/ format tag (subtest-style table), HTML readability strips clutter, HTML extension-less sniff. Plus 8 e2e assertions covering HTML + CSV via real MCP stdio.
- **Test totals**: **88 Go unit + 46 e2e = 134 green**. New deps: `xuri/excelize/v2`, `go-shiori/go-readability`. Both vetted MIT-compatible licenses.
- [[Canonical Tool Implementations Survey 2026-04-26]] Read row expanded with the full format matrix.

### V0.5 SHIPPED — ToolSearch (bleve BM25) + Glob (doublestar)

User-prioritised: ToolSearch first, because without a search-first brain the deferred-loading story across a 50+ tool catalog falls apart.

- **`internal/search/`**: bleve-BM25 index. `Build([]Doc) → *Index` is constructed once at `clawtool serve` boot from every tool we plan to register (enabled core tools + aggregated source tools). Per ADR-007 we wrap `github.com/blevesearch/bleve/v2` (in-memory variant `NewMemOnly`); we do not invent a search engine. Query rewrite applies `name^3`, `keywords^2`, `description^1` boosts so literal-name lookups still beat semantic neighbors. Hits hydrate name/description/type/instance from a side `docs` map. 11 unit tests, including:
  - "bash" → top hit Bash (literal-name boost works)
  - "search file contents regular expression" → top hit Grep (semantic match)
  - "create issue github" → top hit github__create_issue (sourced tool ranks)
  - type filter (core/sourced/any), limit cap (default 8, hard 50), empty-query rejection, score monotonicity.
- **`internal/tools/core/toolsearch.go`**: tool surface. `query` (required), `limit` (default 8, max 50), `type` filter. Output: `{query, results[{name,score,description,type,instance}], total_indexed, engine:"bleve-bm25", duration_ms}`. `CoreToolDocs()` is the single source of truth for core-tool descriptions/keywords used both in tools/list and the search corpus.
- **`internal/tools/core/glob.go`**: doublestar wrap. `pattern` (required), `cwd`, `limit`. Streaming match via `doublestar.GlobWalk` so memory stays bounded for huge dirs. Forward-slash paths regardless of OS. 5 unit tests covering double-star recursion, single-level patterns, limit cap + truncation flag, no-match path, extension filter.
- **`internal/server/server.go`** refactored: load config+secrets → start sources.Manager → build search.Index from descriptors → register cores filtered by `config.IsEnabled` → register source tools with manager-routed handlers. `buildIndexDocs` is the single function that picks what gets indexed.
- **`KnownCoreTools`** = `[Bash, Glob, Grep, Read, ToolSearch]`. `clawtool tools list` now shows all five.
- **E2E**: 9 new assertions (37 total). tools/list registers Glob + ToolSearch; ToolSearch query for "search file contents regex" returns Grep on top via bleve-bm25; ToolSearch for "echo back input text" with stub source returns stub__echo on top (sourced tool); type=core filter excludes sourced tools; Glob `**/*.md` finds README.md, engine=doublestar, matches_count present.
- **Smoke (live binary)**:
  - `tools/list` → `Bash, Glob, Grep, Read, ToolSearch` ✓
  - `ToolSearch{query:"search file contents regex"}` → `Grep` (score 0.94) → `Read` (0.05) → `ToolSearch` (0.01)
  - `ToolSearch{query:"echo back input"}` with stub source live → `stub__echo` (score 1.24, type:sourced, instance:stub)
- **Test totals**: 81 Go unit + 38 e2e = **119 green**. New since v0.4 turn 2: search 11, tools/core +5 (Glob), e2e +8 (Glob 3 + ToolSearch 4 + tools/list 1 missed previously? actually +9: Glob and ToolSearch each 3-4 each).
- New deps: `github.com/blevesearch/bleve/v2`, `github.com/bmatcuk/doublestar/v4`. Both Apache-2.0 / MIT — license-compatible with clawtool MIT.
- Updated [[Hot]], [[Canonical Tool Implementations Survey 2026-04-26]] (Glob + ToolSearch rows now show "Adopted v0.5"), this log.

### V0.4 TURN 2 SHIPPED — MCP client/server proxy

ADR-008's runtime substance lives now: clawtool spawns configured sources as child MCP servers, aggregates their tools under wire-form `<instance>__<tool>` names per ADR-006, and routes tools/call to the right child.

- **`internal/sources` package**: Manager + Instance. Wraps `github.com/mark3labs/mcp-go/client.NewStdioMCPClient` (ADR-007 again — don't write a custom MCP client, use the same library that powers our server side). Each instance: spawn → Initialize → ListTools → cache. Status: Starting/Running/Down/Unauthenticated with reason strings preserved for surface. Manager.Start is non-fatal per source (others continue if one fails). Stop reaps everyone.
- **`SourceTool` aggregation**: each (running instance × tool) becomes one SourceTool. Tool name rewritten to `<instance>__<original>`. Handler closure routes calls to the right Instance with original (un-prefixed) name. Tools from non-Running instances silently omitted.
- **Server integration** (`internal/server/server.go`): ServeStdio loads config + secrets, builds a Manager, starts it, registers core tools (filtered by `config.IsEnabled`), then registers aggregated source tools. Manager.Stop on shutdown.
- **Stub-server test fixture** at `test/e2e/stub-server/`. Tiny Go MCP server with a single `echo` tool. Built via `make stub-server`; e2e and unit tests both spawn it as a real child process, so we exercise the full subprocess + stdio + protocol path with no external dependencies.
- **Unit tests** (`internal/sources/manager_test.go`, 7 tests + 6 SplitWireName subtests):
  - StartsRunningInstance — Status=Running, ToolCount≥1 after spawn
  - AggregatedTools_PrefixedWithInstance — `stub__echo` emerges
  - RouteCall_ReturnsChildResult — calling the closure routes to the child and returns its `echo:<text>` response
  - SplitWireName — bidirectional parsing roundtrips per ADR-006
  - MissingEnvMarksUnauthenticated — `${VAR}` template with no resolution stays Unauthenticated, doesn't try to spawn
  - BadCommandMarksDown — bogus command path → Down, manager continues
  - StopReapsAll — every instance Down, AggregatedTools empty
- **E2E** (+6 assertions, total 29): tools/list with stub source includes `stub__echo` alongside `Bash/Grep/Read`; verify two-underscore separator (not single); tools/call routes correctly; disabled-core-tool config gate verified by removing Bash from output while stub__echo stays.
- **Test totals**: **65 Go unit + 29 e2e = 94 green**. New: sources 7, e2e proxy 6.
- **Smoke**: `clawtool serve` with `[sources.stub] command=["…/stub-server"]` shows 4 tools (Bash, Grep, Read, stub__echo) in tools/list and routes a tools/call against `stub__echo` end-to-end. `claude mcp list` still reports `✓ Connected` after live binary swap via atomic `make install`.
- Updated [[Hot]], this log.

### V0.4 TURN 1 SHIPPED — catalog + secrets + source CLI

ADR-008's user-facing UX is now real on the CLI. Sources are still config-only (no proxy spawn yet — turn 2), but the entire add/list/remove/set-secret/check loop works against an embedded catalog.

- **Catalog** package at `internal/catalog/`:
  - `builtin.toml` embedded via `go:embed` with 12 entries: github, slack, postgres, sqlite, filesystem, fetch, brave-search, google-maps, memory, sequentialthinking, time, git.
  - Per-entry: description, runtime, package, args, required_env, auth_hint, homepage, maintained.
  - `ToSourceCommand()` maps runtime → argv: npx → `["npx", "-y", pkg, args…]`, python → `["uvx", pkg, args…]`, docker → `["docker", "run", "-i", "--rm", img, args…]`, node, binary.
  - `EnvTemplate()` builds `KEY → "${KEY}"` placeholders for required env.
  - `SuggestSimilar` is bidirectional — catches both "git" → "github" and "github-typo" → "github".
  - 11 unit tests.

- **Secrets** package at `internal/secrets/`:
  - TOML store at `~/.config/clawtool/secrets.toml` (mode 0600, separate file from config.toml).
  - Scope-based: `[scopes.<instance>]` and `[scopes.global]` (global = fallback).
  - `Set/Get/Delete/Resolve`. Resolve interpolates `${VAR}` against secrets first, then process env.
  - Atomic temp+rename save.
  - 7 unit tests.

- **CLI source subcommands** at `internal/cli/source.go`:
  - `clawtool source add <name>` — catalog lookup. Prints package + description + homepage + auth hint + actionable `set-secret` command if env missing. Stays config-only this turn (turn 2 wires actual spawn).
  - `--as <instance>` overrides bare name (ADR-006 multi-instance rule). Adding a second `github` without `--as` errors with copy-paste suggestions.
  - Unknown name → suggests similar catalog entries via `SuggestSimilar`.
  - `clawtool source list` — table of configured sources with auth status (✓ ready / ✗ missing) + package.
  - `clawtool source remove <instance>` — drop from config; secrets retained.
  - `clawtool source set-secret <instance> <KEY>` — `--value` flag or stdin fallback. Empty value rejected explicitly.
  - `clawtool source check` — verifies required env per source.
  - **Flag-after-positional fix**: stdlib `flag.Parse` only sees flags before the first positional. Added `reorderFlagsFirst` helper so `source add github --as github-work` and `set-secret github KEY --value ghp_...` work as users naturally type them. Required because the Go stdlib `flag` package doesn't intersperse.
  - 12 unit tests.

- **Test totals: 58 Go unit tests + 23 e2e = 81 green.**
  Per package: catalog 11, cli 21 (8 existing + 13 source-related new), config 11, secrets 7, tools/core 17 (Bash 5 + Grep 5 + Read 7).

- Updated [[Hot]], this log. ADR-008 already locked in earlier this session.

### CATALOG — ADR-008: curated source catalog with name-only ergonomics

- New ADR. `clawtool source add github` resolves bare names to canonical implementations from a built-in catalog (package name, runtime, required env vars, auth flow hints, homepage, maintenance status). Long-form `clawtool source add custom -- npx -y my/server` remains for unknown sources — catalog is a fast path, not a gate.
- Federation strategy with external catalogs (Docker MCP Catalog, MCP Registry, Smithery) per ADR-007 wrap-don't-reinvent.
- Secrets isolated: `~/.config/clawtool/secrets.toml` (mode 0600) separate from `config.toml`. Config can be safely committed; secrets file references via `${VAR}` interpolation.
- Per-runtime support (npx | node | python | docker | binary) with version pinning syntax `clawtool source add github@1.4.2`.
- Lands in v0.4 alongside the source-instance feature. Catalog file format: `internal/catalog/catalog.toml`.

### V0.3 SHIPPED — Grep (ripgrep) + Read (stdlib + pdftotext + ipynb)

- **Engine detection layer** at `internal/tools/core/engines.go` — sync.Once cache, `LookupEngine("rg"|"grep"|"pdftotext")`. Per ADR-007: detect, prefer best-in-class, fall back gracefully.
- **Grep tool** at `internal/tools/core/grep.go`. Wraps ripgrep `--json` event stream (path, line_number, submatches → uniform `GrepMatch{Path,Line,Column,Text}`); system grep fallback parses `path:line:text` format. Engine field exposed in result so users / tests can verify which ran. Knobs: pattern (required), path, glob, type alias, case_insensitive, max_matches.
- **Read tool** at `internal/tools/core/read.go`. Three engines:
  - stdlib bufio for text (single-pass, deterministic total_lines)
  - pdftotext shell-out (`-layout`) with helpful "not installed" error when poppler missing
  - native ipynb JSON parse (handles both legacy array-of-strings and modern single-string `source`)
- Format detection via extension + 4 KiB content sniff (PDF magic, NUL bytes). Binary content refused with structured error rather than dumped.
- **ripgrep installed**: `~/.local/bin/rg` 15.1.0 musl static binary (MIT-or-Unlicense, BurntSushi/ripgrep). No sudo. Without this clawtool's Grep would fall back to system grep (still works, just slower).
- **5 + 6 unit tests** for Grep and Read covering matches, glob filter, max_matches truncation, no-match path, case-insensitive, line range, beyond-EOF, binary rejection, ipynb parsing, PDF-without-engine error path, directory rejection.
- **5 + 5 e2e assertions** added: tools/list registration, Grep call against repo README returns ripgrep engine + matches, Read of README honors line_start/line_end + reports total_lines + format=text/engine=stdlib.
- Test totals after v0.3: **58 green** (16 tools/core + 11 config + 8 cli + 23 e2e).
- Updated `KnownCoreTools` to ["Bash","Grep","Read"]; server registers all three on startup.
- Updated [[Canonical Tool Implementations Survey 2026-04-26]] with adopted-engine markers.

### DISCIPLINE — ADR-007: leverage best-in-class, don't reinvent

- New ADR locks the engineering posture for core-tool work. Wrap mature engines (ripgrep, defuddle/Readability, OpenAI apply_patch, doublestar, bleve, …) and add the polish layer (timeout-safe, structured JSON, secret redaction, MCP correctness, uniform conventions across tools). Reimplement from scratch only when no upstream meets the bar.
- Engineering profile: distribution maintainer, not compiler author.
- License hygiene becomes load-bearing — clawtool is MIT; we shell out to GPL when needed (no linkage), avoid GPL Go imports, attribute every wrapped engine.
- Per-tool baseline table: Bash → /bin/bash (already in use), Grep → ripgrep + system grep fallback, Read → stdlib + pdftotext, Edit → OpenAI apply_patch format, Glob → bmatcuk/doublestar, WebFetch → defuddle/Readability, ToolSearch → bleve (the one thing we genuinely build).
- New running survey: [[Canonical Tool Implementations Survey 2026-04-26]] — grows with every core-tool deep-dive.
- Updated [[Index]], [[Overview]], [[decisions _index]], [[sources _index]], [[Hot]], this log.

### V0.2 PROTOTYPE — config + CLI + tests + standard project hygiene

- **LICENSE** (MIT, root) + **README.md** (install/use/development sections + repo layout map).
- **Makefile** with standard targets: `build`, `test`, `e2e`, `install` (atomic temp+rename — survives running binary), `lint`, `clean`, `dist` (cross-compile linux/darwin amd64/arm64).
- **Bash unit tests** (5): success path, non-zero exit propagation, **timeout preserves output and reaps process group** (ADR-005 headline quality bar — verified at 300ms returning ~300ms even with `sleep 5` child), default cwd → home dir, override cwd.
- **E2E MCP integration script** (`test/e2e/run.sh`, 13 assertions): initialize handshake, tools/list shows Bash + required:[command] schema, tools/call success/non-zero-exit/timeout paths each verified via grep on the escaped JSON wire form. Hooked into `make e2e`.
- **Config package** (`internal/config`): TOML schema mirroring ADR-006 (core_tools, sources, tools, tags, groups, profile). Resolution: tool > server precedence (full tag/group precedence in v0.3). `LoadOrDefault` for first-run-without-init. Default writable `0600` (env may carry secrets). 11 unit tests covering save/load round-trip, precedence, selector charset, missing-file fallback.
- **CLI package** (`internal/cli`): subcommands `init`, `tools list / enable / disable / status`. Selector validation enforces ADR-006 charsets up front; rejects `tag:` / `group:` selectors with explicit "v0.3" message. 8 unit tests + manual smoke run verified.
- **Atomic install** in Makefile: `cp X.new && mv X.new X` — survives "Text file busy" when CC already has the binary running.
- All 37 tests green (5 + 13 + 11 + 8). CC still `✓ Connected` after live binary swap.

### PROTOTYPE — v0.1 build, install, end-to-end verified

- **Working binary**: `bin/clawtool` (7MB Go binary, Go 1.25.5).
- **Module**: `github.com/cogitave/clawtool`. Layout: `cmd/clawtool/`, `internal/{server,version,tools/core}`.
- **MCP SDK chosen**: `github.com/mark3labs/mcp-go v0.49.0` (community; mature, used in production).
- **Single core tool registered**: `Bash` (PascalCase per [[006 Instance scoping and tool naming]]).
- **Quality bar verified**: timeout-safe via process-group SIGKILL (`exec_unix.go`). 500ms timeout actually fires at 501ms even when bash spawned a 3-second sleep. Stdout up to the timeout is preserved.
- **Installed** at `~/.local/bin/clawtool`. **Registered** with Claude Code at user scope. `claude mcp list` reports `clawtool: ... - ✓ Connected`.
- Documented full bringup in [[Prototype Bringup 2026-04-26]] including tests, install commands, tool surface JSON, and explicit v0.1 scope cuts.
- Cuts deferred to v0.2: other core tools, ToolSearch, config.toml, CLI subcommands, source instances, secret redaction.

### NAMING — ADR-006: instance scoping and tool naming convention

- New ADR locking naming for the wire (MCP) and CLI surfaces:
  - **Instance** layer between source and tool. Instance names: kebab-case (`github-personal`).
  - **Wire form** `<instance>__<tool>`; **CLI selector** `<instance>.<tool>`. Mechanical, reversible.
  - **Disjoint charsets**: instance `[a-z0-9-]`, tool `[a-z0-9_]`. `__`-split is unambiguous.
  - **Core tools** PascalCase (`Bash`, `Read`, `Edit`, `Write`, `Grep`, `Glob`, `WebFetch`, `ToolSearch`) matching Claude's native convention. Wire: `mcp__clawtool__Bash`.
  - **First-instance bare name** allowed (`github`); second instance forces explicit rename. Prevents silent collision.
  - **Pattern matching** in tags/groups uses glob against selector form for readability.
  - Full `config.toml` shape spec'd.
- **Language closed: Go.**
- Open question count down to 3 (license, ranking model, catalog format) — all unblocking the prototype.
- Updated [[Index]], [[Overview]], [[decisions _index]], [[Hot]], this log.

### POSITIONING — ADR-005: replace native agent tools

- New ADR. Locks the strategic ambition: clawtool is **the canonical tool layer**, not just an aggregator. Bash/Grep/Read/Edit/Write/Glob/WebFetch ship at quality higher than each agent's native built-in. Goal: agents prefer clawtool's implementations over their own.
- **Search-first reframed**: not a competing identity feature alongside core tools, but the **prerequisite** that lets a 50+ tool catalog scale. Without `tool_search`, the catalog drowns agents. With it, the canonical-layer ambition is operationally feasible.
- **Engineering priority flip**: aggregation is solved (1mcp-agent / docker-mcp-gateway); core-tool quality is the actual work. Implementation-language choice gains weight (Go / Rust > TypeScript for syscall reliability).
- **Quality bar table** in ADR: per-tool axis where clawtool must beat native (bash timeout-drops-output, ripgrep ignore-file behavior, read pagination cursors, edit atomic write, glob cross-platform, webfetch canonicalization).
- **Plugin packaging deferred to phase 2** — make binary usable end-to-end first; CC plugin is a wrapper, not a prerequisite.
- Updated [[Agent-Agnostic Toolset]], [[Overview]], [[Index]], [[decisions _index]], [[Hot]], this log.

### REFINE — ADR-004 Distribution & Usage Scenarios (section 6)

- Added new "Distribution & Usage Scenarios" section to ADR-004.
- **Two layers**: (1) standalone binary (the actual product, generic MCP server, npm/brew/curl install), (2) per-agent plugins (CC, Codex, ...) as thin install+registration wrappers with no state fork.
- **Three usage scenarios** — power-user (manual `mcp add`), CC-only (plugin), multi-agent (shared config).
- **Key invariant**: state lives in one place per device (`~/.config/clawtool/`). "Install once, use everywhere" = shared *config*, not just portable binary.
- Updated [[004 clawtool initial architecture direction]], [[Hot]], this log.

### REFINE — ADR-004 Configuration UX: multi-level tool selectors

- Added selector hierarchy to ADR-004: server (`github`), tool (`github.delete_repo`), tag (`tag:destructive`), group (`group:review-set`), profile (orthogonal).
- Precedence: tool > group > tag > server, with later layers overriding. Same-level conflict: **deny wins** (safety default).
- New CLI surface: `clawtool group create`, `clawtool tools status <selector>` for resolution debugging.
- Open: selector grammar finalization (negation `!`, wildcards `*`).
- Reasoning: enumerating tools one-by-one (docker-gateway weakness) and server-only toggling (1mcp-agent weakness) both hurt real workflows. Multi-level selectors cover the gap; tags exploit the manifest annotations already spec'd in ADR-004 decision 3.
- Updated [[004 clawtool initial architecture direction]], [[Hot]], this log.

### RESEARCH PHASE — universal-toolset landscape survey + initial architecture ADR

- Defined research scope: [[Research Scope 2026-04-26]] — selection criteria, universe of 11 projects surveyed, top 4 picked for deep-dive.
- Deep-dived 4 candidate projects in parallel via WebFetch on README + architecture docs:
  - [[mcp-router]] — desktop GUI manager
  - [[1mcp-agent]] — lean CLI aggregator (closest CLI ancestor to clawtool)
  - [[metamcp]] — Docker-based aggregator+orchestrator+middleware+gateway
  - [[docker-mcp-gateway]] — Docker official, ships in Docker Desktop 4.59+
- Wrote [[Universal Toolset Projects Comparison]] — 8-dimension matrix, coverage heatmap, gap analysis.
- **Key finding**: search-first / deferred tool loading is universally underdeveloped. metamcp roadmaps "Elasticsearch for MCP." nobody ships it. This is clawtool's identity-defining gap.
- Drafted [[004 clawtool initial architecture direction]]:
  - Distribution: MCP-native, single user-local binary, no Docker requirement
  - Search-first = deferred loading + semantic discovery
  - Tool manifest: extend MCP schema via `annotations.clawtool` namespace (no breaking changes)
  - Config UX: CLI dot-notation (docker-mcp-gateway-style) + declarative file + hot-reload
  - Build new (not fork 1mcp-agent), borrow shamelessly
- Updated [[Index]], _index.md files, [[Hot]] cache, this log.

### SCAFFOLD — initial vault scaffold
- Mode: Hybrid (standard + ADR-focused)
- Created folder structure: `wiki/{sources,entities,concepts,decisions,comparisons,questions,meta}`, `_templates/`, `.raw/`, `.obsidian/snippets/`
- Created [[Index]], [[Log]], [[Hot]], [[Overview]]
- Pre-seeded decisions: [[001 Choose claude-obsidian as brain layer]], [[002 Vault on Windows filesystem]], [[003 Multi-account git via direnv and gh]]
- Pre-seeded comparison: [[Memory Tools Evaluated]]
- Pre-seeded entities: [[Bahadır Arda]], [[claude-obsidian]]
- Pre-seeded concepts: [[Karpathy LLM Wiki Pattern]], [[Agent-Agnostic Toolset]], [[Hot Cache]]
- CSS snippet `vault-colors.css` written; needs manual enable in Obsidian Settings → Appearance
- CLAUDE.md written at vault root with project context
