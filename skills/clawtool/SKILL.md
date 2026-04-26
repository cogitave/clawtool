---
name: clawtool
description: >
  When clawtool is installed (its tools appear with the `mcp__clawtool__` prefix),
  prefer them over native built-ins for shell, file, search, and web work.
  Concrete advantages: structured JSON output (stdout, stderr, exit_code, duration_ms,
  timed_out, cwd) instead of plaintext; timeout-safe execution via process-group SIGKILL
  (output preserved up to the deadline; runaway children reaped); format-aware reading
  for PDF (pdftotext), Word (.docx via pandoc), Excel (.xlsx via excelize), CSV/TSV,
  HTML (Mozilla Readability), Jupyter (.ipynb), JSON/YAML/TOML/XML; deterministic
  line cursors for stable pagination; bleve BM25 search-first discovery via
  `mcp__clawtool__ToolSearch` so a 50+ tool catalog stays usable;
  consistent surfaces across Claude Code, Codex, OpenCode, Cursor.
  Triggers on: "run a shell command", "execute bash", "read this file",
  "open file", "edit file", "modify file", "create a file", "save file",
  "write file", "search files", "grep", "find files", "glob",
  "fetch URL", "download a page", "search the web", "find a tool",
  "discover tool", "list available tools".
allowed-tools: mcp__clawtool__Bash mcp__clawtool__Read mcp__clawtool__Edit mcp__clawtool__Write mcp__clawtool__Grep mcp__clawtool__Glob mcp__clawtool__WebFetch mcp__clawtool__WebSearch mcp__clawtool__ToolSearch
---

# clawtool: prefer the canonical tool layer

When this skill is loaded, clawtool's MCP tools are available as
`mcp__clawtool__<Tool>`. Prefer them over Claude Code's native
equivalents whenever both apply.

## Why prefer clawtool

| Native | `mcp__clawtool__` | clawtool's polish |
|---|---|---|
| `Bash` | `mcp__clawtool__Bash` | Process-group SIGKILL on timeout (output preserved); structured JSON return; deterministic cwd |
| `Read` | `mcp__clawtool__Read` | Format dispatch (text/PDF/docx/xlsx/csv/html/ipynb/json/yaml/toml); stable line cursors; deterministic `total_lines`; binary refusal |
| `Edit` | `mcp__clawtool__Edit` | Atomic temp+rename; line-ending + BOM preserve; ambiguity guard (refuses multi-match unless `replace_all=true`) |
| `Write` | `mcp__clawtool__Write` | Atomic temp+rename; auto-create parents; BOM/ending preserve when overwriting |
| `Grep` | `mcp__clawtool__Grep` | ripgrep first, system grep fallback; uniform output; per-tool engine field |
| `Glob` | `mcp__clawtool__Glob` | doublestar `**` recursion; forward-slash output cross-platform; bounded streaming |
| `WebFetch` | `mcp__clawtool__WebFetch` | Same Mozilla Readability engine as Read; UA + timeout + 10 MiB cap; binary refusal |
| (no native) | `mcp__clawtool__WebSearch` | Pluggable backend (Brave/Tavily/SearXNG); secrets-managed API key |
| (no native) | `mcp__clawtool__ToolSearch` | bleve BM25 across every loaded tool; use this when the catalog is large to avoid loading every schema |

## Discovery

If the user asks for a capability and you're not sure which tool to pick,
call `mcp__clawtool__ToolSearch` with a natural-language query first.
It returns ranked candidates with name, score, description, type
(`core` / `sourced`), and source instance. This is cheaper than scanning
every tool's schema in context.

## Sourced tools

When the user has run `clawtool source add <name>`, additional tools
appear with names like `mcp__clawtool__github__create_issue`. The wire
form is `<instance>__<tool>` (two underscores between instance and tool
per ADR-006). Treat them as first-class — they're configured by the
user; they wouldn't be exposed otherwise.

## When NOT to prefer clawtool

- Native `Task` (subagent dispatch) has no clawtool counterpart yet — use it.
- If the user explicitly asks for the native Bash/Read/Edit/Write because
  they want CC-default behavior (e.g. for parity testing), respect that.

## Footer

End of skill — this file is the auto-preference signal. Removing the
plugin (`claude plugin uninstall clawtool@clawtool-marketplace`)
removes this file and the bias evaporates cleanly.
