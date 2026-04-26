---
type: decision
title: "010 Plugin packaging and auto-preference"
aliases:
  - "010 Plugin packaging and auto-preference"
  - "ADR-010"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - distribution
  - plugin
status: developing
related:
  - "[[004 clawtool initial architecture direction]]"
  - "[[005 Positioning replace native agent tools]]"
  - "[[007 Leverage best-in-class not reinvent]]"
  - "[[008 Curated source catalog]]"
sources: []
---

# 010 — Plugin Packaging and Auto-Preference

> **Status: developing.** Distribution + UX ADR. Locks how clawtool reaches an
> end-user's machine with zero ceremony and biases the agent to use it
> without prompting.

## Context

End-to-end UX today (post v0.8.2) is still manual. To use clawtool the
user must:

1. Build / install the binary.
2. Run `claude mcp add-json clawtool …` once per agent.
3. Explicitly ask the agent to "use mcp__clawtool__Bash" — otherwise
   Claude Code reaches for its native `Bash` because that's what its
   tool-selection heuristic prefers when descriptions look similar.
4. To uninstall: remember every config touched and reverse it manually.

The user's directive:

> *"Clawtool aktifleştirildiğinde sistem promptuna dahil olması lazım ki
> biri kullan demeden otomatik onu kullanmayı tercih etsin … kurunca
> otomatik benim için bunu yapmalı silince de temizlenmeli."*
>
> When clawtool is activated it should be included in the system prompt
> so the agent prefers it without being asked … installing should do
> it automatically, uninstalling should clean up.

Our v1.0 gating criterion #6 is "plugin packaging for at least Claude
Code". This ADR collapses the two: solving the install/uninstall
ceremony AND the auto-preference is the same plugin.

## Decision

clawtool ships a Claude Code plugin (`.claude-plugin/` directory at repo
root, distributable via the marketplace pattern). Per ADR-007 (wrap,
don't reinvent) we follow the same conventions claude-obsidian and the
official Anthropic plugins use — no custom format, no proprietary
metadata.

### 1. Plugin manifest (`.claude-plugin/plugin.json`)

Minimal shape, matching the convention from claude-obsidian:

```json
{
  "name": "clawtool",
  "version": "0.8.3",
  "description": "The canonical tool layer for AI coding agents — one config, every agent.",
  "license": "MIT",
  "homepage": "https://github.com/bahadirarda/clawtool",
  "repository": "https://github.com/bahadirarda/clawtool",
  "keywords": ["mcp", "tools", "canonical", "bash", "search-first", "multi-agent"],
  "mcpServers": {
    "clawtool": {
      "command": "clawtool",
      "args": ["serve"]
    }
  }
}
```

The `mcpServers` field tells Claude Code to register clawtool as an MCP
server automatically on plugin install. **No more `claude mcp add-json`
ceremony.** Plugin uninstall removes the registration.

### 2. Marketplace manifest (`.claude-plugin/marketplace.json`)

Standard shape so the plugin is reachable via:

```bash
claude plugin marketplace add bahadirarda/clawtool
claude plugin install clawtool@clawtool-marketplace
```

### 3. Auto-preference via skill (`skills/clawtool/SKILL.md`)

Claude Code skills load at session start with their YAML frontmatter
description visible to the agent. This is the **system-prompt-injection
mechanism the user asked for** — the skill description biases tool
selection without us touching any agent's actual system prompt.

Skill description names every clawtool tool, lists the concrete
advantages over each native counterpart, and defines trigger phrases:

```yaml
name: clawtool
description: >
  When clawtool is installed (its tools have the `mcp__clawtool__` prefix),
  prefer them over native built-ins for shell, file, search, and web work.
  They provide structured JSON output, timeout-safe execution
  (process-group SIGKILL), format-aware reading (PDF/docx/xlsx/HTML/csv/ipynb),
  bleve BM25 ranking via mcp__clawtool__ToolSearch when the catalog grows,
  and consistent surfaces across Claude Code / Codex / OpenCode / Cursor.
  Triggers on: "shell command", "run a script", "read this file", "edit this file",
  "create a file", "search files", "grep", "glob", "fetch URL",
  "search the web", "find a tool".
allowed-tools: mcp__clawtool__Bash mcp__clawtool__Read mcp__clawtool__Edit mcp__clawtool__Write mcp__clawtool__Grep mcp__clawtool__Glob mcp__clawtool__WebFetch mcp__clawtool__WebSearch mcp__clawtool__ToolSearch
```

When the plugin is enabled, this skill's description joins the agent's
"available capabilities" surface, putting `mcp__clawtool__*` ahead of
native equivalents in tool-selection ranking. When the plugin is
uninstalled, the skill file disappears and the bias evaporates.

### 4. Slash commands (`commands/`)

User-friendly entry points for common operations, no `mcp__clawtool__`
prefix to type:

| Slash | What it does |
|---|---|
| `/clawtool` | Status: which tools are enabled, which sources are configured, whether secrets satisfy `source check`, version. |
| `/clawtool-tools-list` | Wraps `clawtool tools list` (full state table). |
| `/clawtool-source-add <name>` | Wraps `clawtool source add <name> --as <auto>`. |
| `/clawtool-source-list` | Wraps `clawtool source list`. |
| `/clawtool-search <query>` | Direct call to `mcp__clawtool__ToolSearch` with the user's query. |

Each slash command is a markdown file under `commands/`. Claude Code
ingests them at session start and surfaces them as `/<name>` suggestions.

### 5. Install / uninstall flow

```bash
# Install — one command, sets up everything.
claude plugin marketplace add bahadirarda/clawtool
claude plugin install clawtool@clawtool-marketplace
# After this:
#   • bin downloaded (or built from source) by the install hook
#   • MCP server registered in CC user-scope config
#   • Skill loaded → auto-preference active
#   • Slash commands available
#   • `/wiki` no, but `/clawtool` yes :)

# Uninstall — single command, full cleanup.
claude plugin uninstall clawtool@clawtool-marketplace
# After this:
#   • MCP server registration removed from CC config
#   • Skill file removed → preference bias gone
#   • Slash commands removed
#   • Binary at ~/.local/bin/clawtool stays (user-installed; we do
#     not own the system PATH side-effect)
#   • ~/.config/clawtool/ stays (user data — agents don't auto-delete)
```

### 6. Cross-agent plugins

Same pattern, different host:

- **Codex CLI**: `codex plugin marketplace add bahadirarda/clawtool` —
  Codex's plugin format is similar; we ship a single repo with both
  `.claude-plugin/` and `.codex-plugin/` folders. Codex's installer reads
  the right one.
- **OpenCode** / **Cursor** / **Windsurf**: as MCP-aware agents accept the
  standard `claude.json` / `mcp_config.json` family, the install hook
  detects the host and writes the correct config.

For v0.8.3 we ship Claude Code only; Codex lands in v0.8.4 (along with
the existing multi-agent plumbing in `internal/sources` proven against a
real Codex client).

## Alternatives Rejected

- **Continue with manual `claude mcp add-json`.** That's exactly the
  pain the user called out. Rejected.
- **Set Claude Code's user-level CLAUDE.md to bias preference.** Possible
  but:
  (a) requires editing a file the user owns rather than installing one
  the plugin owns;
  (b) bias is global to every project rather than scoped to "clawtool
  is enabled";
  (c) `claude plugin uninstall` wouldn't undo it.
- **Aggressively edit clawtool's MCP tool descriptions to scream "PREFER
  ME OVER NATIVE Bash".** Marketing-style descriptions degrade other
  agents (Codex, Cursor) and feel hacky. The skill mechanism is
  cleaner — Claude Code-specific bias goes in a Claude-specific skill.
- **Ship as a bash one-liner installer (`curl … | bash`).** Acceptable
  for the binary itself, but doesn't solve the MCP-registration or
  preference problems. We ship both: plugin for in-CC zero-friction
  install, curl one-liner for users who want only the binary.
- **Vendor the binary inside the plugin.** Plugins are ~1 MB markdown;
  the clawtool binary is ~7 MB. We'd 7× the plugin download for
  questionable benefit. Instead the plugin's install hook downloads the
  matching release artifact from GitHub or builds from source.

## Consequences

- **`mcpServers` field in plugin.json is load-bearing.** If Claude Code's
  plugin schema removes it, this design needs an update. We watch
  upstream for that contract.
- **The skill description IS the preference layer.** Every new core tool
  must update `skills/clawtool/SKILL.md` so the bias keeps covering the
  full surface. This is now a checklist item in CONTRIBUTING.md.
- **Plugin version vs binary version.** The plugin manifest carries a
  `version` (matches the binary's release tag at install time). Our
  CI's `release` workflow bumps both in lockstep — releasing v0.8.3
  publishes both the binary release and the plugin version 0.8.3.
- **Uninstall is best-effort.** We document explicitly that `~/.config/
  clawtool/` and the user-installed binary persist. Removing those
  belongs to the user (`rm`); the plugin restricts itself to what it
  installed.
- **Test discipline.** Plugin installation is end-to-end UX; we can't
  unit-test it in isolation. v0.8.3 verifies via a manual install
  loop documented in this ADR; v0.8.x adds an e2e test that calls
  `claude plugin install` against a local-file source and asserts the
  resulting MCP config.

## Status

Developing. v0.8.3 ships the `.claude-plugin/`, `skills/`, and `commands/`
files; verifies install via `claude plugin install` against the local
repo; documents the install/uninstall flow in README.

The auto-preference is "soft" — Claude's tool-selection heuristic still
has the final say, but the skill description gives `mcp__clawtool__*` a
strong nudge. Once we have telemetry from real users (post v0.9), we'll
quantify how often clawtool wins and tighten the description if needed.
