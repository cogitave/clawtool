---
type: concept
title: "Agent-Agnostic Toolset"
created: 2026-04-26
updated: 2026-04-26
tags:
  - concept
  - clawtool-core
status: developing
complexity: intermediate
domain: clawtool
aliases:
  - "Universal toolset"
  - "Cross-agent tool layer"
related:
  - "[[Overview]]"
---

# Agent-Agnostic Toolset

The core proposition of clawtool.

## Problem

Each AI coding agent (Claude Code, Codex, OpenCode, Cursor, Continue, Windsurf, ...) ships its own tool ecosystem: Bash, Read, Edit, Write, Search, etc. Same logical tools, redone per agent. Users picking a new agent re-learn, re-configure, re-trust the tools. Tool authors targeting "all agents" build N integrations.

## clawtool's Answer

A single, configurable toolset that:
- Installs once on the device under one name
- Exposes a stable interface to any AI coding agent / IDE
- Lets the user enable / disable / configure individual tools
- Ships **canonical-quality core tools** (bash, grep, read, edit, write, glob, webfetch) that aim to be **better than each agent's native built-in** — see [[005 Positioning replace native agent tools]]
- Treats search (`tool_search` primitive) as a first-class interaction primitive — and as the prerequisite that lets a 50+ tool catalog actually be usable

## What It Is NOT

- Not a memory system
- Not a brain-map system
- Not an agent runtime

These are out of scope. Agent-side concerns (memory, planning, prompt orchestration) belong to the agent. clawtool is the **shared tool layer underneath** — and ambitiously, the **canonical tool layer**: good enough that agents prefer clawtool's bash/grep/read/edit over their own native built-ins. See [[005 Positioning replace native agent tools]].

## Open Spec Questions

- **Distribution**: MCP server (most agents already speak MCP) vs. proprietary protocol vs. dual?
- **"Search-first" interpretation**:
  - (a) Deferred tool loading (à la Claude Code's `ToolSearch` — schemas fetched on demand, context stays slim)
  - (b) Unified search index over all tools' capabilities (semantic discovery)
  - (c) Search as the canonical primitive (every operation is a search)
- **Tool manifest format**: how does a tool declare itself? (MCP tool schema? richer manifest? versioning?)
- **Configuration UX**: CLI flags? declarative file? plugin manager?
- **Core tool list**: what's always-on? Just bash + grep, or fuller set?

These need answering during the spec phase. See [[Overview]] for project status.
