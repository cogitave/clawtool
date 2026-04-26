# clawtool: LLM Wiki

Mode: Hybrid (Standard + ADR-focused) — project-bound brain
Purpose: Project-bound knowledge base for clawtool — a customizable, agent-agnostic toolset that installs once and is usable everywhere across AI coding agents (Claude Code, Codex, OpenCode); captures design decisions, comparative research on memory/toolset projects, and architectural rationale.
Owner: Bahadır Arda
Created: 2026-04-26

## Structure

```
clawtool/
├── .raw/              # immutable source documents (web clips, transcripts, exports)
├── wiki/
│   ├── index.md       # master catalog
│   ├── log.md         # append-only operation log (newest at top)
│   ├── hot.md         # ~500-word recent context cache
│   ├── overview.md    # executive summary
│   ├── sources/       # one summary page per .raw/ source
│   ├── entities/      # people, orgs, products, repos, plugins
│   ├── concepts/      # ideas, patterns, frameworks
│   ├── decisions/     # ADRs — architectural decisions with rationale
│   ├── comparisons/   # side-by-side analyses (e.g. memory tools)
│   ├── questions/     # filed answers to user queries
│   └── meta/          # dashboards, lint reports, conventions
├── _templates/        # frontmatter templates per note type
├── .obsidian/         # Obsidian config (snippets, workspace, plugins)
└── CLAUDE.md          # this file
```

## Conventions

- All notes use YAML frontmatter: type, title, created, updated, tags, status (minimum)
- Wikilinks use `[[Note Name]]` format — filenames are unique, no paths needed
- `.raw/` contains source documents — never modify them
- `wiki/index.md` is the master catalog — update on every ingest
- `wiki/log.md` is append-only — never edit past entries; new entries go at the **top**
- `wiki/hot.md` is overwritten completely each session — keep under 500 words
- Date format: `YYYY-MM-DD` strings, not ISO datetime
- Lists use `- item` format, not inline `[a, b, c]`
- Wikilinks in YAML must be quoted: `"[[Page Name]]"`

## Operations

- **Ingest**: drop source in `.raw/`, say "ingest [filename]"
- **Query**: ask any question — Claude reads `hot.md` → `index.md` → relevant pages
- **Decision**: capture as `decisions/NNN-slug.md` — use ADR template
- **Lint**: say "lint the wiki" for orphans, dead links, gaps
- **Save**: say "/save" or "save this" to file the current conversation

## Project Context

clawtool is in **research / pre-spec phase**. Do not implement until research and specs are agreed. The user explicitly wants the foundation right because clawtool aims to be a standard across multiple agents — getting the abstraction wrong is expensive.

When the user gives a clawtool task, ask whether we are still in research/spec phase or have moved to implementation. Use parallel tools and web research before proposing designs.
