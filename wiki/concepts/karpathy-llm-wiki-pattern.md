---
type: concept
title: "Karpathy LLM Wiki Pattern"
created: 2026-04-26
updated: 2026-04-26
tags:
  - concept
  - memory
  - pattern
status: developing
complexity: intermediate
domain: agent-memory
aliases:
  - "LLM Wiki"
  - "Compounding knowledge pattern"
related:
  - "[[claude-obsidian]]"
  - "[[Hot Cache]]"
---

# Karpathy LLM Wiki Pattern

A pattern for LLM-driven persistent memory proposed by Andrej Karpathy ([gist](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)). Implemented by [[claude-obsidian]] and several other Obsidian-based plugins.

## Core Idea

> Knowledge compounds like interest.

Build a persistent, structured wiki where:
- Every source ingested gets integrated into entities + concepts pages with cross-references
- Every query draws from the accumulated wiki, not the chat transcript
- Maintenance lint detects orphans, dead links, gaps
- The LLM never re-reads source files for already-extracted facts — it reads the wiki

The wiki is the product. Chat is the interface.

## Three Operations

| Operation | What it does |
|---|---|
| **INGEST** | Read source → extract entities/concepts → create or update wiki pages → cross-reference → update index + log |
| **QUERY** | Read [[Hot Cache]] → scan index → drill relevant pages → synthesize answer with citations to wiki pages (not training data) |
| **LINT** | Find orphans, dead wikilinks, stale claims, missing cross-references |

## Why It Beats Plain RAG

- **Pre-computed cross-references** — relationships are explicit in wikilinks, not inferred at query time.
- **Persistent synthesis** — once contradictions are flagged or comparisons drawn, they stay drawn. Don't re-do work.
- **Inspectable** — every claim traces to a `.raw/` source.
- **Token efficient** — query reads ~500 tokens of hot cache + ~1000 of index + 100-300 per drilled page. Re-reading 40 source files would cost ~20k.

## Implementations

- [[claude-obsidian]] (used here)
- coleam00/claude-memory-compiler (LLM compiler variant)
- AgriciDaniel/claude-seo, claude-ads, claude-blog (domain-specific applications by same author)
