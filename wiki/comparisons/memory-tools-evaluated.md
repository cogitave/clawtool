---
type: comparison
title: "Memory Tools Evaluated"
created: 2026-04-26
updated: 2026-04-26
tags:
  - comparison
  - research
  - memory
status: mature
subjects:
  - "[[claude-obsidian]]"
  - "[[claude-memory-compiler]]"
  - "[[mikeadolan claude-brain]]"
  - "[[memvid claude-brain]]"
  - "[[lucasrosati claude-code-memory-setup]]"
  - "[[mem0]]"
  - "[[Letta]]"
  - "[[Zep]]"
dimensions:
  - "ease of inspection"
  - "multi-agent support"
  - "automation level"
  - "lock-in risk"
  - "fit for project-bound brain"
verdict: "claude-obsidian wins for clawtool. Plain-markdown storage, multi-agent ready, plugin-installable, Karpathy wiki pattern."
related:
  - "[[001 Choose claude-obsidian as brain layer]]"
---

# Memory Tools Evaluated

Survey conducted 2026-04-26 to pick a brain layer for Claude when working on clawtool. Goal: persistent project-bound memory, not a feature *of* clawtool.

## Categories

### A. Claude-Code-specific brain plugins

| Project | Storage | Notable | Why considered |
|---|---|---|---|
| **AgriciDaniel/claude-obsidian** ⭐ | Obsidian markdown vault + hooks | Karpathy LLM Wiki pattern, 11 skills, multi-agent (CC/Codex/Gemini/Cursor/Windsurf) | Chosen — see [[001 Choose claude-obsidian as brain layer]] |
| **mikeadolan/claude-brain** | SQLite + MCP + hooks | Pragmatic, fuzzy/semantic/keyword search | Strong, but DB opaque |
| **memvid/claude-brain** | Single `.mv2` Rust binary | Sub-ms search, portable | Format opaque, lock-in risk |
| **toroleapinc/claude-brain** | Markdown + git sync, semantic merge | N-way machine sync | Sync nice but adds complexity early |
| **thedotmack/claude-mem** | SQLite + agent-sdk compression | Auto-captures and compresses sessions | Re-injection mechanism interesting |
| **coleam00/claude-memory-compiler** | Markdown + hooks + agent-sdk LLM compiler | Karpathy KB-style, capture → extract → organize | **Runner-up.** Compiler idea may complement claude-obsidian later. |
| **IlyaGorsky/memory-toolkit** | Pure markdown + workstreams | Minimalist, no DB | Too sparse for our needs |
| **cdeust/memory-monitor** | MCP + Three.js 3D viz | "Brain map" visualization | Pretty but doesn't improve memory quality |
| **lucasrosati/claude-code-memory-setup** | Obsidian + Graphify | 71.5x token reduction, codebase KG | Heavier deps; user noted Graphify overkill for our scope |

### B. Cross-agent MCP memory servers

| Project | Notable |
|---|---|
| **shaneholloman/mcp-knowledge-graph** | Local KG, entities/relations/observations |
| **doobidoo/mcp-memory-service** | REST + KG + autonomous consolidation |
| **DeusData/codebase-memory-mcp** | Code intel for 66 langs, persistent KG |
| **mnardit/agent-recall** | SQLite KG, LLM session-start summary |

Could be used in addition to claude-obsidian (e.g., write distilled vault outputs to a KG accessible via MCP from other agents). Out of scope for now.

### C. General-purpose agent memory frameworks

| Project | Strength | Weakness for our use |
|---|---|---|
| **mem0** | Hybrid vector+graph+kv, 3-tier scope | Claude-Code agnostic; needs glue |
| **Letta** (ex-MemGPT) | OS-inspired tiered memory, LLM manages tiers | Heavier framework; runtime |
| **Zep** | Temporal KG (Graphiti) | Server-side, less local-first |
| **Cognee** | Hybrid retrieval | Knowledge-graph-first, RAG-heavy |

Mature but framework-level; not what we need for a single-project Claude brain.

## Decision Drivers (in order of weight)

1. **Inspectability** — vault content must be human-readable and editable. → markdown wins, opaque DB loses.
2. **Multi-agent** — clawtool is itself multi-agent; the brain should mirror that. → claude-obsidian's AGENTS.md/GEMINI.md/CLAUDE.md ships native support.
3. **Automation level** — hooks should reduce discipline burden. → claude-obsidian has SessionStart/Stop + 11 skills.
4. **Lock-in risk** — should be removable without data loss. → markdown + git is portable; SQLite/`.mv2` are not.
5. **Pattern quality** — Karpathy's LLM Wiki (compound knowledge) > flat session log dumps.

## Verdict

**claude-obsidian** for clawtool. **claude-memory-compiler**'s LLM-compiler idea kept in reserve as a possible automation layer if Zettelkasten upkeep becomes a burden.

## Sources Reviewed

(Web research from 2026-04-26 — full URLs in the chat transcript that produced this note. Will be re-ingested as `.raw/` snapshots if needed for verification.)
