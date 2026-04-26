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
