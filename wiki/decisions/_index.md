---
type: domain
title: "Decisions"
created: 2026-04-26
updated: 2026-04-26
tags:
  - meta
  - index
  - adr
status: developing
subdomain_of: ""
page_count: 7
---

# Decisions (ADRs)

Architecture Decision Records. Numbered sequentially. Each captures: context, decision, rationale, alternatives, consequences, status.

## Active

### Brain / infra (mature)
- [[001 Choose claude-obsidian as brain layer]]
- [[002 Vault on Windows filesystem]]
- [[003 Multi-account git via direnv and gh]]

### clawtool architecture (developing)
- [[004 clawtool initial architecture direction]] — initial spec direction. Locks in: MCP distribution, single binary, search-first as identity, manifest extension via annotations, CLI dot-notation, build-new-not-fork. Open: ranking model, catalog source.
- [[005 Positioning replace native agent tools]] — strategic positioning: clawtool's core tools (bash, grep, read, edit, write, glob, webfetch) ship at quality higher than each agent's native built-in. Search-first is the prerequisite. Aggregation is solved; core-tool quality is the actual work.
- [[006 Instance scoping and tool naming]] — multi-instance support via kebab-case instance names (`github-personal`, `github-work`); wire form `<instance>__<tool>`, CLI selector `<instance>.<tool>`. Core tools use PascalCase (`Bash`, `Read`, `Edit`) matching Claude's native convention. No collision possible: disjoint charsets for instance vs tool.
- [[007 Leverage best-in-class not reinvent]] — clawtool wraps mature engines (ripgrep, defuddle, OpenAI apply_patch format, doublestar, etc.) and adds the polish layer (timeout-safe, structured output, secret redaction, MCP correctness). Reimplement from scratch only when no upstream meets the bar. Engineering profile: distribution maintainer, not compiler author.

## Convention

- Filename: `NNN-short-slug.md` (zero-padded sequence)
- Title in frontmatter: `"NNN Short Slug"` (matches filename, used for wikilink)
- Status: `mature` (decided + implemented), `developing` (decided, not yet implemented or still being refined), `superseded` (replaced by later ADR)
- Never edit body of `mature` ADRs in retrospect — write a new ADR that supersedes
