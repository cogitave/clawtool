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
page_count: 3
---

# Decisions (ADRs)

Architecture Decision Records. Numbered sequentially. Each captures: context, decision, rationale, alternatives, consequences, status.

## Active

- [[001 Choose claude-obsidian as brain layer]]
- [[002 Vault on Windows filesystem]]
- [[003 Multi-account git via direnv and gh]]

## Convention

- Filename: `NNN-short-slug.md` (zero-padded sequence)
- Title in frontmatter: `"NNN Short Slug"` (matches filename, used for wikilink)
- Status: `mature` (decided + implemented), `developing` (decided, not yet implemented), `superseded` (replaced by later ADR)
- Never edit body of `mature` ADRs in retrospect — write a new ADR that supersedes
