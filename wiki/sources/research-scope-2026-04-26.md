---
type: source
title: "Research Scope 2026-04-26"
created: 2026-04-26
updated: 2026-04-26
tags:
  - source
  - research-scope
  - meta
status: mature
source_type: meta
author: "wiki bootstrap"
date_published: 2026-04-26
url: ""
confidence: high
key_claims:
  - "Universal-toolset / MCP-aggregator landscape is dense in 2026."
  - "All serious candidates speak MCP — non-MCP distribution is no longer a real option."
  - "Per-tool granular control exists in most but with widely different UX."
  - "Search-first / deferred tool loading is the genuinely uncovered ground."
related:
  - "[[mcp-router]]"
  - "[[1mcp-agent]]"
  - "[[metamcp]]"
  - "[[docker-mcp-gateway]]"
  - "[[Universal Toolset Projects Comparison]]"
sources: []
---

# Research Scope — 2026-04-26

## Question

What existing projects already do (or attempt) what clawtool aims for: a **customizable, install-once, configurable, agent-agnostic toolset** usable across Claude Code, Codex, OpenCode, and other AI coding agents?

## Selection Criteria

Projects qualify for deep-dive only if they hit at least three of:

1. **Install-once, use-everywhere** — single deployment serves many MCP clients.
2. **Per-tool granular control** — user can enable / disable / configure individual tools, not just whole servers.
3. **Active maintenance** — released in 2025 or 2026; non-trivial commit cadence.
4. **Multi-agent reach** — explicitly compatible with at least 3 of: Claude Code, Cursor, Windsurf, VS Code, Codex CLI, Claude Desktop, Cherry Studio, Roo Code.

## Universe Surveyed

From earlier searches (2026-04-26):

- mcp-router/mcp-router (desktop GUI manager)
- 1mcp-app/agent (CLI aggregator)
- metatool-ai/metamcp (Docker-based aggregator + middleware + gateway)
- docker/mcp-gateway (Docker official, ships with Docker Desktop 4.59+)
- microsoft/mcp-gateway (k8s reverse proxy — too enterprise)
- VeriTeknik/pluggedin-mcp (proxy + web UI)
- agentic-community/mcp-gateway-registry (enterprise + OAuth/Keycloak)
- wanaku-ai/wanaku (Apache Camel-based router, JBang)
- MarimerLLC/mcp-aggregator (lazy discovery + dual MCP+REST)
- Miguell-J/mcp-one (central server unifying multiple MCP servers)
- Smithery / Composio / Glama / MintMCP (registries / hosted gateways — different category)

## Selected for Deep-Dive

Top four representing distinct architectural stances:

| Project | Stance |
|---|---|
| **[[mcp-router]]** | Desktop GUI; manual toggle as the primary UX |
| **[[1mcp-agent]]** | Lean CLI aggregator; OAuth 2.1, hot-reload config |
| **[[metamcp]]** | Comprehensive: aggregator + orchestrator + middleware + gateway in Docker |
| **[[docker-mcp-gateway]]** | Industry-shipped (Docker Desktop) reference; container-isolated MCP servers |

Excluded for now:
- microsoft/mcp-gateway — k8s-targeted, wrong layer
- pluggedin-mcp — feature overlap with metamcp; revisit if metamcp falls short
- mcp-gateway-registry — enterprise auth focus, not relevant for individual-developer UX
- wanaku — Java/JBang dependency too heavy
- registries (Smithery/Glama/etc.) — different product category (catalog vs runtime)

## Output

Each selected project gets a `wiki/entities/` page with extracted facts. Synthesis lives in [[Universal Toolset Projects Comparison]]. Decisions distilled to [[004 clawtool initial architecture direction]].
