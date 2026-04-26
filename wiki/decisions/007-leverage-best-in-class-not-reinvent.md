---
type: decision
title: "007 Leverage best-in-class not reinvent"
aliases:
  - "007 Leverage best-in-class not reinvent"
  - "ADR-007"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - engineering-discipline
  - tools
status: developing
related:
  - "[[005 Positioning replace native agent tools]]"
  - "[[006 Instance scoping and tool naming]]"
sources:
  - "[[Canonical Tool Implementations Survey 2026-04-26]]"
---

# 007 — Leverage Best-in-Class, Don't Reinvent

> **Status: developing.** Engineering-discipline ADR. Locks how clawtool builds its core tools so we don't waste effort on solved problems.

## Context

ADR-005 set the quality bar: clawtool's core tools (Bash, Read, Edit, Write, Grep, Glob, WebFetch, …) must beat each agent's native built-in. The naive reading of that ADR is "reimplement everything from first principles, very carefully." That's wrong.

The user's correction:

> *"Webden bu default toolların en iyi halini bulabilirsin bazılarını sıfırdan yazmak zorunda değilsin kardeşim … muhtemelen birileri ki claude, openai falan yazmıştır bunları harika şekilde sende bunlara bakabilirsin."*
>
> You can find the best version of these default tools on the web — you don't have to write some from scratch. Probably someone — Claude, OpenAI, etc. — has written these excellently; you can study those.

There are mature, battle-tested implementations for almost every primitive a coding agent needs. Reimplementing a regex engine to compete with ripgrep is a multi-year project we won't win. Reimplementing PDF parsing to compete with pdfplumber is a five-year project nobody on this team will start. Reimplementing the OpenAI `apply_patch` semantics to compete with their published format is a guaranteed quality regression.

clawtool's edge is **curation + integration + polish**, not from-scratch implementation.

## Decision

For each core tool, the default approach is:

1. **Identify the best-in-class implementation.** Could be a CLI binary (ripgrep, fd, jq), a library, a published format (OpenAI `apply_patch`), or another agent's open-source tool source (Claude Code SDK, Codex CLI source).
2. **Wrap it.** clawtool spawns it as a subprocess or imports the library. Tool surface (MCP schema, structured output, timeout handling) is clawtool's own.
3. **Add the polish layer**: timeout-safe execution, structured JSON output, secret redaction, predictable cwd, license-correct attribution, MCP wire correctness, agent-friendly error messages.
4. **Reimplement only when** (a) no upstream exists at the quality bar, (b) license is incompatible (clawtool is MIT — see [[006 Instance scoping and tool naming|ADR-006]] / repo `LICENSE`), or (c) the wrap-cost exceeds the rewrite-cost (rare; specifically motivated).

clawtool's contribution is the **canonical wrapper**. The underlying engine is somebody else's masterpiece, used legally and credited.

## Per-tool baseline

| Core tool | Best-in-class engine | clawtool's polish |
|---|---|---|
| **Bash** | `/bin/bash` itself (already used in v0.1) | timeout-safe via process-group SIGKILL, structured JSON result, secret redaction (v0.3), predictable cwd |
| **Grep** | `ripgrep` (`rg`) — fastest text search, ignore-file semantics correct | unified `type:` filter aliases, structured matches, deterministic line numbering |
| **Read** | Study Claude Code's Read tool surface; for PDF use `pdftotext`/`pdfplumber`, for notebooks the JSON itself | stable cursor for paginated reads, deterministic line counts for context budgeting, transparent format detection |
| **Edit** | OpenAI's `apply_patch` format (well-specified, widely understood); for primitives `goimports`-style atomic write | atomic write, conflict detection, line-ending preserve, undo log |
| **Write** | Same atomic-write primitive as Edit | atomic temp + rename, line-ending preserve, BOM handling |
| **Glob** | `github.com/bmatcuk/doublestar` Go library (Go-native double-star glob) | ignore-file respect (`.gitignore` + `.clawtoolignore`), platform-stable separators |
| **WebFetch** | `defuddle` (used by claude-obsidian), Mozilla Readability port, or `trafilatura`-equivalent | URL canonicalization, citation metadata, cache, secret-redaction in URLs |
| **WebSearch** | Existing search APIs (Brave, Tavily, etc.) or `searxng` self-hosted | source allowlist/blocklist, transparent ranking, agent-friendly result shape |
| **ToolSearch** *(unique)* | BM25 baseline via `github.com/blevesearch/bleve`; embedding rerank with stock model | clawtool-specific schema-aware indexing across registered tools |

This table is illustrative. Each tool gets a deep-dive before code lands; the survey lives at [[Canonical Tool Implementations Survey 2026-04-26]] and grows as we evaluate.

## What "polish" actually buys us

The wrapper is not trivial. Concrete things clawtool's wrappers do that the underlying engines don't:

- **Uniform structured output**. ripgrep has a `--json` mode but its shape differs from `find`'s differs from `grep`'s. clawtool returns one shape across all search-like tools.
- **Cross-tool conventions.** A `cwd` argument on every tool that touches the filesystem, with the same default semantics ($HOME if empty). Native built-ins differ on this constantly.
- **Timeout safety**. Few CLIs care about being interrupted gracefully. ADR-005's process-group SIGKILL pattern applies to every spawn.
- **Secret redaction**. `KEY=value` in env or stdout gets redacted before reaching the agent. No upstream tool does this.
- **MCP correctness**. Schema, annotations (`destructiveHint`, `readOnlyHint`), error envelopes — all consistent across tools.
- **Cross-agent stability.** clawtool's tool surface doesn't change when ripgrep ships a flag rename. We absorb upstream churn.

These are real value. Together they justify why agents would prefer `mcp__clawtool__Grep` to their native `Grep` — not because we wrote a faster regex engine (we didn't), but because we wrapped the fastest one *correctly*, *uniformly*, and *safely*.

## Alternatives Rejected

- **Reimplement everything from scratch in Go.** Years of work, guaranteed to be worse than mature engines on every dimension. Not how the project earns its quality claim.
- **Pure pass-through with no polish.** Then we're a worse aggregator than [[1mcp-agent]]. The polish *is* the value.
- **Vendor in (copy) tool source from other projects without attribution.** Legal and ethical bug. Not a real option.
- **Hard dependency on a specific upstream binary always being installed (e.g. require `rg` on PATH).** OK as default, but clawtool ships a fallback (slower, simpler) so first-run works without prerequisites. Wrapper detects available engines and picks the best.

## Consequences

- **Engineering effort flips again.** ADR-005 said "core tool quality is the work." This ADR refines: the work is **wrapper quality + integration testing + polish**, not engine reinvention. The skill profile is closer to "Linux distribution maintainer" than "compiler author."
- **Build cost: optional dependency detection at runtime.** A `Grep` invocation should detect `rg` on PATH and use it; fall back to a Go-native search when missing. Detection logic is now table stakes for every tool.
- **License hygiene becomes load-bearing.** clawtool is MIT. Wrapping GPL tools (e.g., GNU grep) is fine when we shell out to them — no linkage, no license bleed. Importing GPL Go libraries is not. We track this per-tool.
- **Attribution must be visible.** README, `--version`, and per-tool descriptions credit the upstream engine. "Powered by ripgrep" appears in `Grep`'s description. This is right and also marketing.
- **The first move on every new core tool is a survey,** not a hack. [[Canonical Tool Implementations Survey 2026-04-26]] is the durable document we update each time.

## Status

Developing. Apply this discipline starting at v0.2 work on `Read` and `Grep`. Bash's v0.1 already conforms (we wrap `/bin/bash` and add timeout-safety + structured output).
