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
- **Treats search as a primary interaction paradigm** — see [[004 clawtool initial architecture direction]]

## What it is NOT

- Not a memory system
- Not a brain-map system
- Not an agent runtime

The project's value is being the shared, configurable tool layer underneath. Tool ecosystems are currently re-implemented per agent (each IDE bundles its own Bash/Read/Edit/etc.). clawtool inverts that: install once, configure once, use everywhere.

## Status

**Pre-spec / research phase round 1 done.** [[004 clawtool initial architecture direction]] captures the locked direction; some questions deliberately deferred for prototyping. Next deliverable: prototype of `tool_search` primitive.

## Decisions

### Brain layer + infra
- [[001 Choose claude-obsidian as brain layer]]
- [[002 Vault on Windows filesystem]]
- [[003 Multi-account git via direnv and gh]]

### Architecture (developing)
- [[004 clawtool initial architecture direction]]
- [[005 Positioning replace native agent tools]]

## Distinguishing identity

**Two pillars, mutually reinforcing:**

1. **Canonical core tools** — bash, grep, read, edit, write, glob, webfetch shipped at quality higher than each agent's native built-in. Goal: agents prefer clawtool's tools to their own. See [[005 Positioning replace native agent tools]].
2. **Search-first** — `tool_search` primitive (deferred loading + semantic discovery). Without it, a 50+ tool catalog drowns the agent. Search-first is the prerequisite that lets the canonical-tool ambition scale.

Aggregation, per-tool toggle, single-binary, multi-agent are table stakes copied from [[Universal Toolset Projects Comparison|the best of class]] — they are not what clawtool is *for*.

## Open spec questions (deferred to prototype)

- Implementation language (Go / Rust / TypeScript)
- License (Apache 2.0 / MIT)
- Tool-search ranking model (BM25 / embedding / hybrid)
- Catalog source (define new vs read existing Docker MCP Catalog / MCP Registry / Smithery)
- Container isolation as optional layer (v2)
- Middleware support (v2)

## Owner

[[Bahadır Arda]]

## Research inputs

- [[Memory Tools Evaluated]] — brain layer survey
- [[Universal Toolset Projects Comparison]] — MCP-aggregator landscape survey
- [[Research Scope 2026-04-26]] — selection criteria for above
