---
type: concept
title: "Hot Cache"
created: 2026-04-26
updated: 2026-04-26
tags:
  - concept
  - memory
status: developing
complexity: basic
domain: agent-memory
related:
  - "[[Karpathy LLM Wiki Pattern]]"
  - "[[claude-obsidian]]"
---

# Hot Cache

A ~500-word summary of the most recent context, lives at `wiki/hot.md`. Read first by Claude on every relevant query.

## Purpose

When a new session starts (or a different project points at the same vault), Claude needs to know "where were we?" without re-reading the entire wiki or full chat history. Hot cache answers that in ~500 tokens.

## Update Triggers

- After every ingest
- After significant query exchange
- At the **end** of every session (SessionStop hook)
- Manually via `update hot cache`

## Format

```markdown
---
type: meta
title: "Hot Cache"
updated: YYYY-MM-DD
---

# Recent Context

## Last Updated
YYYY-MM-DD. [what happened]

## Key Recent Facts
- [Most important takeaway]
- [Second most important]

## Recent Changes
- Created: [[New Page 1]], [[New Page 2]]
- Updated: [[Existing Page]]
- Flagged: [[Page A]] vs [[Page B]] contradiction

## Active Threads
- [Open question]
- [What user is investigating]
```

## Rules

- **Hard cap ~500 words.** It's a cache, not a journal.
- **Overwrite completely each update.** Don't accumulate.
- **Wikilinks for everything referenced.** Cross-references should still resolve.
