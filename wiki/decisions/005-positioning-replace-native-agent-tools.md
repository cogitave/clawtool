---
type: decision
title: "005 Positioning replace native agent tools"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - positioning
  - strategy
status: developing
related:
  - "[[004 clawtool initial architecture direction]]"
  - "[[Agent-Agnostic Toolset]]"
sources: []
---

# 005 — Positioning: replace native agent tools

> **Status: developing.** Strategic positioning. Locks the project's quality bar and the prioritization of core-tool work.

## Context

Earlier ADRs framed clawtool as an MCP aggregator with a search-first primitive. That's correct but understates the ambition.

Each AI coding agent ships its own native built-in tools:
- Claude Code: `Bash`, `Read`, `Edit`, `Write`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `NotebookEdit`, etc.
- Codex CLI: `shell` (bash-equivalent), `apply_patch`, `update_plan`, `web_search`, etc.
- Cursor / Windsurf / others: similar surfaces, each reimplemented separately.

Each implementation has subtle, agent-specific bugs and ergonomics quirks. The N-times-reinvented Bash. The slightly-different Grep wrapper. The web-fetch that drops headers. None of them is canonically "the" Bash a coding agent should use; they're each one-off best-effort wrappers.

The user's stated goal:

> *"claude'un openai'ın kendi toolsetleri yerine direkt bunları kullanmasına sebebiyet verebilecek noktada iyi olmamız lazım amaç buydu yani"*
>
> We need to be good enough that Claude / OpenAI / others would use **clawtool's** tools instead of their native built-ins. That was the goal.

## Decision

clawtool positions itself as **the canonical tool layer for AI coding agents**, not just as an aggregator that adds extra tools alongside built-ins.

The stretch outcome: agents bundle clawtool (or delegate to it) and disable their native versions. The minimum outcome: users prefer clawtool's tools and disable / shadow the native ones via configuration.

This positioning sets two operational implications:

1. **Core tools are flagship, not table stakes.** Bash, Grep, Read, Edit, Write, Glob, WebFetch — these get first-class engineering effort. Quality bar: each must be measurably better than the corresponding native built-in across the major agents.
2. **Search-first is what makes the strategy possible.** If clawtool ships 50+ high-quality tools, agents must be able to find them efficiently. Without `tool_search` (deferred loading + semantic discovery, see [[004 clawtool initial architecture direction]]), the catalog drowns the agent. Search-first is not a competing identity feature — it is the prerequisite that lets the canonical-tool-layer ambition scale.

## Quality bar — what "better than native" means concretely

Each clawtool core tool ships only when it beats native built-ins on a specific axis. Initial bar by tool:

| Tool | Native pain point | clawtool target |
|---|---|---|
| **bash** | Timeout drops output; cwd state inconsistent across calls; no structured output mode | Output preserved on timeout; predictable cwd-persistence model; opt-in JSON mode; secret redaction; per-session command history |
| **ripgrep / grep** | Different agents wrap differently; ignore-file behavior differs; no semantic file-type aliases | Single canonical wrapper; respects `.gitignore` + `.clawtoolignore`; named filetype aliases (`type:ts`, `type:py`) |
| **read** | Large-file pagination unstable; image / PDF support uneven; line counts unpredictable for budgeting | Stable cursors across calls; first-class image / PDF / notebook; line counts deterministic |
| **edit** | Partial writes on crash; no diff preview; line-ending hazards | Atomic write; built-in diff preview; line-ending + BOM preserve |
| **write** | Same as edit; conflicts with concurrent edits ignored | Atomic; conflict detection; checkpointed undo log |
| **glob** | Cross-platform path semantics inconsistent; ignore-files often skipped | Canonical glob with ignore-file respect; platform-stable separators |
| **webfetch** | URL canonicalization missing; markdown conversion quality varies | Canonicalize redirects; consistent markdown via known model; citation metadata |
| **websearch** | Result quality opaque; no source filters | Source allowlist / blocklist; transparent ranking |
| **tool_search** *(unique to clawtool)* | Doesn't exist anywhere | BM25 baseline + optional embedding rerank; deferred schema loading |

This list is illustrative. The actual launch set is decided when the prototype runs.

## Why this is achievable (and why now)

- **MCP standardization** — agents already accept external tool sources. They will compare native vs MCP results; if MCP wins, MCP wins. No vendor cooperation needed.
- **Each native built-in is a one-off** — no agent vendor has a team dedicated to Bash. Every implementation is a side project. clawtool's core tools have the project's full attention.
- **Search-first removes the "too many tools" objection** — agents won't accept a 50-tool flat list, but they will accept "search and the right tool surfaces."
- **clawtool can ship faster than agent vendors update built-ins** — independent release cycle.

## Risk: agent vendors push back

If clawtool actively replaces native tools, vendors may:
- Restrict MCP override (e.g., "Bash from MCP can't replace native Bash")
- Add UX friction to MCP tool selection
- Build their own multi-tool aggregator

Mitigations:
- Make clawtool's tools so demonstrably better that vendors prefer to bundle them rather than block.
- Open-source license (MIT or Apache 2.0) keeps the door open to upstream contributions / direct integration.
- Search-first is genuinely novel — even if vendors copy the Bash wrapper, the discovery primitive is harder to replicate.

## Alternatives Rejected

- **Stay in "additive" lane (just an aggregator + custom tools)** — doesn't justify the engineering cost vs existing options ([[1mcp-agent]], [[metamcp]] cover that case). The replace-native ambition is what makes clawtool worth building.
- **Target only a single agent first** — single-agent quality bar is too low; native built-in is also single-agent so the comparison is too easy. Cross-agent canonical-ness is the actual moat.
- **Skip the core tools, ship just `tool_search` as middleware** — `tool_search` without first-class tools is a feature in search of a product. The two reinforce each other.

## Consequences

- **Engineering priorities flip**: aggregation is solved (1mcp-agent / docker-mcp-gateway); core-tool quality is the actual work.
- **Prototype scope** ([[004 clawtool initial architecture direction]] decision 6 → "make it usable now") must include 3-5 core tools at quality, not just an MCP-server stub.
- **Implementation language choice gains weight** — syscall-level reliability and timeout handling argue for Go (or Rust). TypeScript drops in priority.
- **Documentation strategy**: every core tool ships with a "vs native" comparison page. Public benchmarks, not just docs.
- **Long-term**: if successful, Anthropic / OpenAI eventually upstream or bundle. If unsuccessful, clawtool is still a useful per-user replacement.

## Status

Developing. Locks the strategic direction; the concrete launch toolset and benchmarks become the next deliverable.
