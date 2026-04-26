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

2026-04-26. Initial vault scaffold completed. Project is in pre-spec research phase.

## Key Recent Facts

- **clawtool's vision**: a customizable, agent-agnostic toolset that installs once on the device and is usable across all AI coding agents (Claude Code, Codex, OpenCode, future IDEs). Single name, easy configuration. Out of scope: memory layers, brain-maps, agent-side features — those are about *how Claude works*, not what we build.
- **Brain layer chosen**: [[claude-obsidian]] (AgriciDaniel) — Karpathy LLM Wiki pattern, Obsidian-based, multi-agent support (Claude/Codex/Gemini/Cursor/Windsurf), 11 skills (wiki, ingest, query, lint, save, autoresearch, canvas, ...).
- **Vault location**: `/mnt/c/Users/Arda/workspaces/@cogitave/clawtool` (Windows-side). Reason: Obsidian's file watcher (chokidar/Node fs.watch) cannot operate over `\\wsl.localhost\` UNC paths — fails with `EISDIR illegal operation on a directory, watch`. Native Windows filesystem (visible from WSL via `/mnt/c`) is the only reliable option.
- **Multi-account git**: direnv + `GH_CONFIG_DIR` per-directory env + git `[includeIf]` for identity. No `gh auth switch` (global state breaks parallel terminals). Personal account `bahadirarda` already authenticated to `~/.config/gh-personal/`. Work account `caucasian01` pending user-side `gh auth login` with `GH_CONFIG_DIR=~/.config/gh-work`.

## Recent Changes

- Created: [[Index]], [[Log]], [[Hot]], [[Overview]], [[001 Choose claude-obsidian as brain layer]], [[002 Vault on Windows filesystem]], [[003 Multi-account git via direnv and gh]], [[Memory Tools Evaluated]], [[Bahadır Arda]], [[claude-obsidian]], [[Karpathy LLM Wiki Pattern]], [[Agent-Agnostic Toolset]]
- Updated: (none yet — fresh scaffold)

## Active Threads

- **Open**: clawtool research → spec phase. Need to define: tool manifest format, search/discovery protocol, MCP integration model, configuration UX.
- **Open**: work account (caucasian01) `gh auth login` — user runs when needed.
- **Open**: enable `vault-colors.css` snippet in Obsidian (Settings → Appearance → CSS Snippets → toggle on).
- **Pending**: install Obsidian community plugins (Templater, Obsidian Git) — recommended but optional.
