---
type: entity
title: "mcp-router"
created: 2026-04-26
updated: 2026-04-26
tags:
  - entity
  - tool
  - mcp-aggregator
  - gui
status: developing
entity_type: product
role: "Desktop GUI manager for MCP servers — closest GUI analog to clawtool's vision"
first_mentioned: "[[Research Scope 2026-04-26]]"
related:
  - "[[Universal Toolset Projects Comparison]]"
sources:
  - "[[Research Scope 2026-04-26]]"
---

# mcp-router

A desktop application that simplifies management of MCP servers — toggle servers on/off, enable/disable individual tools, organize into Projects and Workspaces, all from a single dashboard.

## Key Facts

- **Repo**: https://github.com/mcp-router/mcp-router
- **License**: Sustainable Use License (not a standard OSI license)
- **Latest release**: v0.6.2 (2026-01-19); 34 releases total; ~160 commits on main
- **Distribution**: GitHub release downloads (desktop binary). Connect agents via `npx -y @mcp_router/cli connect` after exporting `MCPR_TOKEN`.
- **MCP Clients supported**: Claude, Cline, Windsurf, Cursor, custom — one-click integration.

## Per-Tool Granularity

Yes — UI exposes toggle switches at both server and individual-tool levels. **No documented config-file mechanism** for the same; UI is the only path.

## Tool Discovery / Search

None documented. Tools are listed in the dashboard, browsed manually, toggled by hand.

## Configuration

UI-driven. Project structure shows `.npmrc`, `.env`, `pnpm-workspace.yaml` but no MCP-specific manifest format documented externally.

## Auth Model

Token-based: `MCPR_TOKEN` issued from the UI, stored locally, never transmitted.

## Limitations

- 25 open issues (specifics not surfaced)
- License is "Sustainable Use" — restricts commercial use cases vs MIT/Apache
- No CI-friendly headless mode documented
- No tool-level search or discovery surface for agents

## Relevance to clawtool

- **Validates** the "single dashboard managing multiple MCP servers" UX pattern.
- **Differs** from clawtool by being GUI-first (clawtool would presumably be CLI/headless-first to fit terminal-driven AI coding workflows).
- **Inspiration**: Projects/Workspaces concept maps cleanly to clawtool's per-project tool profile.
- **Caution**: license restricts forking for commercial alternatives.
