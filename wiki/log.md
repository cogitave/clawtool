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
