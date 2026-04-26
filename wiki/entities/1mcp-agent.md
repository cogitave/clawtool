---
type: entity
title: "1mcp-agent"
created: 2026-04-26
updated: 2026-04-26
tags:
  - entity
  - tool
  - mcp-aggregator
  - cli
status: developing
entity_type: product
role: "Lean CLI MCP aggregator — closest CLI analog to clawtool's vision"
first_mentioned: "[[Research Scope 2026-04-26]]"
related:
  - "[[Universal Toolset Projects Comparison]]"
sources:
  - "[[Research Scope 2026-04-26]]"
---

# 1mcp-agent (1mcp-app/agent)

A unified MCP server that aggregates multiple MCP servers into one, with both direct-MCP-server mode and CLI mode for agent workflows. Progressive tool disclosure via CLI inspection commands.

## Key Facts

- **Repo**: https://github.com/1mcp-app/agent
- **License**: Apache 2.0
- **Latest release**: v0.30.3 (2026-04-11); 65 releases total; 665 commits on main; very active (4 open issues, 6 PRs)
- **Distribution**: `npx -y @1mcp/agent` or platform binary (Linux/macOS/Windows)
- **Connect**: HTTP endpoint `http://127.0.0.1:3050/mcp?app=<client>`, OR CLI mode via `1mcp cli-setup --codex|--claude`
- **MCP Clients supported**: Cursor, VSCode, Claude Code, Codex, Claude, Cherry Studio, Roo Code

## Commands

- `1mcp mcp add <name> -- <command>` — register a server
- `1mcp serve` — start the aggregator
- `1mcp instructions` — inventory of all registered tools
- `1mcp inspect <server>` / `inspect <server>/<tool>` — tool schema introspection
- `1mcp run <server>/<tool>` — manual execution

## Per-Tool Granularity

**Server-level only.** Per-tool enable/disable is not documented. This is a clawtool-relevant gap.

## Tool Discovery / Search

CLI-driven, manual: `1mcp inspect`, `1mcp instructions`. No semantic search, no agent-facing discovery primitive.

## Configuration

JSON files: `mcp.json` or `.1mcprc` (example provided in repo). Env vars and CLI flags also supported. **Hot-reload supported** — config changes take effect without restart.

## Auth Model

OAuth 2.1, scope-based authorization. CLI mode supports `--scope repo --repo-root .` flags. Production-ready security posture is a stated feature.

## Limitations

- Per-agent mode lock-in: each agent uses either direct MCP OR CLI mode, not both simultaneously.
- Per-tool toggling missing (server-level only).
- No discovery / search primitive — relies on agent reading `instructions` output.

## Relevance to clawtool

- **Closest CLI ancestor.** If clawtool targeted "leaner, just-CLI 1mcp," it would essentially be 1mcp + per-tool granularity + search-first.
- **Distribution model is right** — npx + binary, no Docker dependency. clawtool should consider this same model.
- **OAuth 2.1 maturity** is a strong reference; clawtool can lean on this if/when remote tool serving matters.
- **Hot-reload config** is a UX standard worth keeping.
- **Per-tool gap** is the most concrete differentiation opportunity.
