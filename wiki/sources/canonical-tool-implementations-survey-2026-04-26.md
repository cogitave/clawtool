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
| **ripgrep** (`rg`, BurntSushi) | MIT-or-Unlicense | Fastest, correct ignore-file handling, JSON output, type filters. **Default choice.** |
| GNU grep | GPL | Universal; OK to shell out (no linkage). Fallback when `rg` absent. |
| `the_silver_searcher` (`ag`) | Apache 2.0 | Older, slower than rg; not worth maintaining. |
| Pure-Go (`github.com/grailbio/zaplog/regexp`-class) | Various | Slow vs ripgrep; only for "no rg available, no system grep" emergencies. |

**Plan**: wrap `rg` when present, fall back to `grep` (system) when not. Translate output through clawtool's uniform shape so the agent never sees the difference.

## Read

| Engine | Notes |
|---|---|
| **stdlib `os.Open` + bufio** | For plain text. clawtool owns line-counting and pagination cursors. |
| **`github.com/dslipak/pdf` / `pdftotext` shell-out** | PDF extraction. Library for embedded use; binary fallback for breadth. |
| **`gopkg.in/yaml.v3`, `encoding/json`** | Structured-text passthrough — render in original syntax with line numbers. |
| **Jupyter `.ipynb`** | JSON parse cells; render code/text/output blocks per ADR-005 quality bar. |

**Open question**: should `Read` ever decode binary formats it doesn't understand, or refuse with a structured "unsupported format" result? Lean toward refuse-with-helpful-error.

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
| **`github.com/bmatcuk/doublestar/v4`** | Go-native, double-star (`**`) glob, fast, MIT. **Default choice.** |
| `fd` (binary) | Excellent UX but binary dependency. Optional acceleration when present. |
| stdlib `path/filepath.Glob` | No double-star; not enough. |

**Polish**: `.gitignore` + `.clawtoolignore` honored by default; opt-out flag for full traversal.

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
| BM25 index | `github.com/blevesearch/bleve` | Mature, Go-native, BM25 + faceting. |
| Embedding rerank (v0.3+) | local sentence-transformers via ONNX Runtime, OR remote OpenAI/Anthropic embeddings | TBD by usage benchmarks. |
| Query language | clawtool-specific | Tags + free-text + selectors. |

[[Universal Toolset Projects Comparison]] confirmed nobody ships this; we are first.

## How to use this page

When a new core tool moves into implementation:
1. Add or refine its row above.
2. Pick the engine; document the rationale in the tool's source file (Go doc comment).
3. License-check the engine before adoption.
4. Surface the credit in the tool's MCP description (e.g. "Grep — text search powered by ripgrep").
5. Update [[007 Leverage best-in-class not reinvent]] if the principle gains a refinement.
