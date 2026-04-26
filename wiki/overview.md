---
type: overview
title: "Overview"
created: 2026-04-26
updated: 2026-04-26
tags:
  - meta
  - overview
status: developing
---

# clawtool — Project Overview

## What

clawtool is a **customizable toolset** that:
- Installs once on the device under a single name
- Is usable across any AI coding agent / IDE (Claude Code, Codex, OpenCode, future ones)
- Lets the user easily configure / enable / disable which tools are exposed
- Ships some core tools (bash, grep, etc.) that are commonly needed
- Treats search as a primary interaction paradigm

## What it is NOT

- Not a memory system
- Not a brain-map system
- Not an agent runtime

The project's value is being the shared, configurable tool layer underneath. Tool ecosystems are currently re-implemented per agent (each IDE bundles its own Bash/Read/Edit/etc.). clawtool inverts that: install once, configure once, use everywhere.

## Status

**Pre-spec / research phase.** Specs come before implementation. The foundation must be right because clawtool aims to be a standard across multiple agents.

## Key Decisions So Far

- [[001 Choose claude-obsidian as brain layer]] — Claude's working memory for this project lives in this Obsidian vault
- [[002 Vault on Windows filesystem]] — vault must be on native NTFS for Obsidian's watcher
- [[003 Multi-account git via direnv and gh]] — per-directory identity, no global switching

## Next Open Questions

- Distribution mechanism: MCP server (likely) vs. custom protocol?
- "Search-first" interpretation: deferred tool loading vs. unified tool search index?
- Tool manifest / registry format
- Configuration UX (CLI? declarative? plugin-based?)

## Owner

[[Bahadır Arda]]

## Related

- [[Memory Tools Evaluated]] — research on agent memory frameworks (separate from clawtool's scope)
- [[claude-obsidian]] — chosen brain layer plugin
