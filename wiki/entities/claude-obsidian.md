---
type: entity
title: "claude-obsidian"
created: 2026-04-26
updated: 2026-04-26
tags:
  - entity
  - tool
  - plugin
status: mature
entity_type: product
role: "Brain layer for Claude when working on clawtool"
first_mentioned: "[[Memory Tools Evaluated]]"
related:
  - "[[001 Choose claude-obsidian as brain layer]]"
  - "[[Karpathy LLM Wiki Pattern]]"
---

# claude-obsidian

A Claude Code plugin by [AgriciDaniel](https://github.com/AgriciDaniel) that turns Obsidian into a self-organizing AI knowledge companion. Implements [[Karpathy LLM Wiki Pattern]] (ingest → query → lint → compound).

## Key Facts

- **Repo**: https://github.com/AgriciDaniel/claude-obsidian
- **License**: MIT
- **Version installed**: 1.6.0 (as of 2026-04-26)
- **Install method**: Claude Code plugin marketplace
  ```bash
  claude plugin marketplace add AgriciDaniel/claude-obsidian
  claude plugin install claude-obsidian@claude-obsidian-marketplace
  ```
- **Multi-agent**: ships AGENTS.md (Codex), CLAUDE.md (Claude Code), GEMINI.md (Gemini CLI). Vault works with all.

## Skills Provided

| Slash command | Purpose |
|---|---|
| `/wiki` | Setup / scaffold / resume |
| `/save` | File current conversation as wiki note |
| `/autoresearch` | 3-round web research loop, files to wiki |
| `/canvas` | Visual canvas operations (companion plugin: claude-canvas) |

Sub-skills (invoked via main wiki skill or directly): `wiki-ingest`, `wiki-query`, `wiki-lint`, `wiki-fold`, `obsidian-markdown`, `obsidian-bases`, `defuddle`.

## Architecture

Three layers:
- **`.raw/`** — immutable source documents (drop URLs, files, transcripts here)
- **`wiki/`** — LLM-generated structured knowledge (cross-referenced atomic notes)
- **`CLAUDE.md`** — schema and instructions

Memory persistence:
- **`wiki/hot.md`** — ~500-word recent context cache, updated at SessionStop hook
- **`wiki/index.md`** — master catalog
- **`wiki/log.md`** — append-only operation log

## Why Chosen

See [[001 Choose claude-obsidian as brain layer]] and [[Memory Tools Evaluated]].

## Notes for clawtool

- Vault scoped to clawtool only — not a general-purpose second brain.
- Vault location forced to Windows-native filesystem due to Obsidian's WSL UNC limitation. See [[002 Vault on Windows filesystem]].
- Companion plugin **claude-canvas** not yet installed; consider if visual layouts become useful.
