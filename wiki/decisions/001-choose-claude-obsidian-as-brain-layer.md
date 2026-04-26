---
type: decision
title: "001 Choose claude-obsidian as brain layer"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - tooling
status: mature
related:
  - "[[Memory Tools Evaluated]]"
  - "[[claude-obsidian]]"
  - "[[Karpathy LLM Wiki Pattern]]"
---

# 001 — Choose claude-obsidian as brain layer

## Context

The user wanted Claude to have a persistent, project-bound brain for clawtool — captured design decisions, comparative research, and architectural rationale that survive across sessions. This is *Claude's working memory for this project*, not a feature of clawtool itself.

Claude's session memory (CLAUDE.md, /memory) covers some of this but doesn't compose well across long-running research-heavy projects. We surveyed external options.

See [[Memory Tools Evaluated]] for the full comparison.

## Decision

Use **AgriciDaniel/claude-obsidian** as the brain layer. Install as a Claude Code plugin (not as a cloned vault).

## Rationale

- **Karpathy LLM Wiki pattern** — INGEST → QUERY → LINT loop, knowledge compounds like interest. Cross-referenced atomic notes, not flat logs.
- **Plain markdown storage** — inspectable, git-versioned, no opaque DB lock-in. If Claude remembers something wrong, you open the file and fix it.
- **Multi-agent ready** — repo ships AGENTS.md and GEMINI.md alongside CLAUDE.md. Same vault works with Codex CLI and Gemini CLI without migration.
- **Skill-based architecture** — 11 skills (wiki, ingest, query, lint, save, autoresearch, canvas, defuddle, fold, ...) each with its own SKILL.md. Composable, scope-limited.
- **Hooks for determinism** — SessionStart/Stop hooks can update hot.md automatically; CLAUDE.md instructs Claude to read hot.md first.
- **Plugin install over clone** — `claude plugin marketplace add` + `claude plugin install` is cleaner than cloning a vault repo. Plugin lives in `~/.claude/plugins/`, vault content lives separately wherever the user wants.

## Alternatives Rejected

- **mikeadolan/claude-brain** — pragmatic SQLite + MCP + hooks, but data is opaque to manual inspection.
- **memvid/claude-brain** — single `.mv2` portable file is elegant, but proprietary format = lock-in risk.
- **coleam00/claude-memory-compiler** — sophisticated capture+extract+organize via agent-sdk; was strong runner-up. Reconsider later for the *automation layer* on top of claude-obsidian.
- **mem0 / Letta / Zep / Cognee** — mature general-purpose agent memory frameworks, but Claude-Code-agnostic. Would require custom glue.
- **lucasrosati/claude-code-memory-setup (Obsidian + Graphify)** — strong combination but heavier dependency footprint (Graphify for codebase graphs is overkill for current scope; the user pointed out Obsidian here is mainly the graph view, no deep extras).

## Consequences

- All clawtool design notes, decisions, and research live in this Obsidian vault.
- Hot cache (`wiki/hot.md`) is the single source of truth for "where were we last time?"
- Cross-project referencing is possible — other Claude Code projects can point to this vault from their CLAUDE.md.
- Scope discipline required: this vault is **only** for clawtool. Personal/general second-brain content goes elsewhere.

## Status

Implemented 2026-04-26. Vault scaffolded under `/mnt/c/Users/Arda/workspaces/@cogitave/clawtool/wiki/`.
