---
type: source
title: "Canonical Tool Implementations Survey 2026-04-26"
aliases:
  - "Canonical Tool Implementations Survey 2026-04-26"
  - "Tool Engine Survey"
created: 2026-04-26
updated: 2026-04-26
tags:
  - source
  - research
  - tools
  - engines
status: developing
source_type: meta
author: "research session"
date_published: 2026-04-26
url: ""
confidence: medium
key_claims:
  - "ripgrep is the best-in-class regex search engine; clawtool's Grep wraps it."
  - "OpenAI apply_patch format is a published, well-specified edit format worth adopting."
  - "Mozilla Readability + defuddle dominate web-extraction quality."
  - "Each candidate engine needs license + maintenance check before adoption."
related:
  - "[[007 Leverage best-in-class not reinvent]]"
sources: []
---

# Canonical Tool Implementations Survey — 2026-04-26

Per [[007 Leverage best-in-class not reinvent|ADR-007]], the first move on every clawtool core tool is to identify the best-in-class engine to wrap. This page is the running survey. Each section grows when we deep-dive that tool.

## Bash

| Engine | Status | Notes |
|---|---|---|
| `/bin/bash` (POSIX shell) | **In use (v0.1)** | Already correct call. Polish layer (timeout-safe, structured output) is clawtool's own. |
| `pdsh`, `mosh`, distrobox | rejected | Wrong layer; remote execution is out of scope for v1. |

**Polish to add (v0.3+)**: secret redaction, per-session command history, optional structured-mode JSON streaming.

## Grep

| Engine | License | Notes |
|---|---|---|
| **ripgrep** (`rg`, BurntSushi) | MIT-or-Unlicense | Fastest, correct ignore-file handling, JSON output, type filters. **Default choice — adopted v0.3.** |
| GNU grep | GPL | Universal; OK to shell out (no linkage). Fallback when `rg` absent — **adopted v0.3 as fallback**. |
| `the_silver_searcher` (`ag`) | Apache 2.0 | Older, slower than rg; not worth maintaining. |
| Pure-Go (`github.com/grailbio/zaplog/regexp`-class) | Various | Slow vs ripgrep; only for "no rg available, no system grep" emergencies. |

**Status (v0.3)**: Wired at `internal/tools/core/grep.go`. Detects ripgrep first via `LookupEngine("rg")`, falls back to system grep. Uniform output shape: `{matches[], matches_count, truncated, engine, duration_ms, cwd, pattern}`. Engine field exposes which one ran. ripgrep `--json` event stream parsed for path/line_number/submatches; system grep parses `path:line:text` line format.

**Engine install path**: `~/.local/bin/rg` (15.1.0 musl static binary from BurntSushi/ripgrep releases). No sudo. clawtool README will link to a one-liner installer for users without rg already.

## Read

Multi-format dispatcher: format detected by extension first (cheap), then 4 KiB content sniff for ambiguous cases. Each format has its own wrapper file under `internal/tools/core/read_*.go`.

| Format | Engine | License | Notes |
|---|---|---|---|
| Plain text | **stdlib `bufio`** | — | Single-pass line walk; deterministic `total_lines`; 1-indexed inclusive cursors. **v0.3.** |
| `.pdf` | **`pdftotext` (poppler-utils)** shell-out | GPL (no Go linkage) | `-layout` preserves columns; absent-engine yields structured install-hint error. **v0.3.** |
| `.ipynb` | Native JSON cell parse | — | `# --- cell N (type) ---` markers; legacy + modern `source` shapes. **v0.3.** |
| `.docx` | **`pandoc`** shell-out | GPL (no Go linkage) | Universal office-format converter; covers tables/footnotes/lists/comments. Absent-engine error points at apt/brew install. **Adopted v0.6.** |
| `.xlsx` | **`github.com/xuri/excelize/v2`** | BSD-3 | Pure-Go, no CGO, Microsoft / Alibaba / Oracle production-tested. Per-sheet rendering with TSV-style rows + workbook sheet list. **Adopted v0.6.** |
| `.csv` / `.tsv` | stdlib `encoding/csv` | — | Header-aware preview, pipe-rendered data rows, total-rows footer. LazyQuotes + `FieldsPerRecord=-1` so ragged real-world files don't abort. **Adopted v0.6.** |
| `.html` / `.htm` | **`github.com/go-shiori/go-readability`** | Apache-2.0 | Mozilla Readability port (the same algorithm Firefox Reader View uses); strips nav / ads / footer chrome and surfaces title + byline + article body. **Adopted v0.6.** |
| `.json` / `.yaml` / `.toml` / `.xml` | text passthrough + format tag | — | Already human-readable; we only tag the format so the agent can branch. **v0.6.** |
| Unknown binary | refused | — | Structured error pointing at `Bash` + `xxd`/`hexdump` for raw-byte access. **v0.3.** |

**Polish layer (clawtool's own)**:
- 5 MiB content cap protects agent context.
- Stable line-based cursor (`line_start`, `line_end`) works across every format because each engine's output funnels through `applyLineRangeFromBuffer`.
- `sheets[]` field on the result for spreadsheet formats — agent pages workbook with subsequent calls.
- Engine field exposes which backend ran (`stdlib`, `pdftotext`, `pandoc`, `excelize`, `csv-stdlib`, `go-readability`, `ipynb-json`).

**Open (v0.7+)**:
- Markdown rendering with `yuin/goldmark` for callout / table-aware preview (today: passthrough as text).
- OCR via `tesseract` shell-out for scanned PDFs / image-based docs.
- Audio / video metadata via `ffprobe` shell-out.
- Office formats `.pptx` / `.odt` / `.ods` (already work via pandoc but not yet plumbed).
- Archive listing for `.zip` / `.tar.gz` (`archive/zip` + `archive/tar`).

## Edit

| Engine / format | Notes |
|---|---|
| **OpenAI `apply_patch` format** | Published, used in their tools; superior to raw `git apply` for AI editing because it tolerates context drift. **Strong candidate as input format.** |
| **`google/diff-match-patch`** | Library for text diffs. Useful internally for conflict detection. |
| **aider's edit blocks** | Studied; less general than `apply_patch`. |
| **Plain stdlib `os.WriteFile` + atomic temp+rename** | The actual write primitive. |

**Plan**: accept multiple input formats (apply_patch, raw replacement, search/replace), normalize internally, do atomic write + diff preview + conflict detection.

## Write

Same primitive as Edit. The interesting concerns are:

- Atomic temp + rename
- Line-ending detection + preserve (CRLF / LF / mixed)
- BOM detection + preserve
- Permission preserve (file already exists)
- Parent-directory creation (opt-in flag)

## Glob

| Engine | Notes |
|---|---|
| **`github.com/bmatcuk/doublestar/v4`** | Go-native, double-star (`**`) glob, fast, MIT. **Adopted v0.5.** |
| `fd` (binary) | Excellent UX but binary dependency. Optional acceleration when present. |
| stdlib `path/filepath.Glob` | No double-star; not enough. |

**Status (v0.5)**: Wired at `internal/tools/core/glob.go`. `doublestar.GlobWalk` for streaming match (memory-bounded for huge dirs). Forward-slash output paths (platform-stable). Hard cap (default 1000, max 10000) protects agent context. Engine field exposes which backend ran.

**Polish (v0.6)**: `.gitignore` + `.clawtoolignore` honored by default — `github.com/sabhiram/go-gitignore` library candidate. Tracked as a v0.6 add.

## WebFetch

| Engine | Notes |
|---|---|
| **`defuddle`** (used by claude-obsidian, see [[claude-obsidian]]) | Strips ads/nav/headers; clean markdown out. **Strong candidate.** |
| **Mozilla Readability port** (`go-readability`) | Classic engine; battle-tested over a decade. |
| **`trafilatura`** (Python) | Best academic benchmarks for content extraction; would shell out. |
| `chromedp` headless Chrome | For JS-rendered sites; heavyweight; opt-in only. |

**Plan**: default Readability/defuddle, opt-in headless Chrome behind a flag for stubborn sites. Output: cleaned markdown + citation metadata (title, author if extractable, canonical URL, fetch timestamp).

## WebSearch

| Engine | Notes |
|---|---|
| **Brave Search API** | Privacy-friendly, decent quality. |
| **Tavily** | Built for AI-agent use case. |
| **searxng (self-hosted)** | Aggregates many engines; full control. |
| Google CSE / Bing | Available; quality vs cost depends. |

**Plan**: pluggable backend, user supplies API key in config, default backend recommendation in docs but not bundled.

## ToolSearch (unique to clawtool)

This one we **do** build, because nothing equivalent exists.

| Component | Engine | Notes |
|---|---|---|
| BM25 index | **`github.com/blevesearch/bleve/v2`** | Adopted v0.5. Mature, Go-native, in-memory variant (`NewMemOnly`), BM25 by default, supports phrase + boosted-field queries via `NewQueryStringQuery`. |
| Embedding rerank (v0.7+) | local sentence-transformers via ONNX Runtime, OR remote OpenAI/Anthropic embeddings | TBD by usage benchmarks. |
| Query language | clawtool-specific | Free-text input rewritten with field boosts: `name^3`, `keywords^2`, `description^1`. |

**Status (v0.5)**: Wired at `internal/search/index.go` + `internal/tools/core/toolsearch.go`. Built once at `clawtool serve` startup from the union of (enabled core tools, aggregated source tools, ToolSearch itself). Query→ranked hits via bleve BM25; hits hydrate name/description/type/instance from a side `docs` map. Validated: "search file contents regex" → Grep top (score ≈ 0.94); "echo back input" with stub source registered → stub__echo top (score ≈ 1.24); type=core filter excludes sourced tools.

**Polish (v0.6+)**: hot-reload index on config change; embedding rerank for top-K candidates; faceted result presentation by tag.

[[Universal Toolset Projects Comparison]] confirmed nobody ships this; we are first.

## How to use this page

When a new core tool moves into implementation:
1. Add or refine its row above.
2. Pick the engine; document the rationale in the tool's source file (Go doc comment).
3. License-check the engine before adoption.
4. Surface the credit in the tool's MCP description (e.g. "Grep — text search powered by ripgrep").
5. Update [[007 Leverage best-in-class not reinvent]] if the principle gains a refinement.
