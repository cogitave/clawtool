---
type: comparison
title: "Universal Toolset Projects Comparison"
created: 2026-04-26
updated: 2026-04-26
tags:
  - comparison
  - research
  - mcp
  - aggregator
status: mature
subjects:
  - "[[mcp-router]]"
  - "[[1mcp-agent]]"
  - "[[metamcp]]"
  - "[[docker-mcp-gateway]]"
dimensions:
  - "distribution"
  - "configuration UX"
  - "per-tool granularity"
  - "tool discovery / search"
  - "multi-agent reach"
  - "auth"
  - "lock-in / dependency cost"
  - "license"
verdict: "All four cover 60–80% of clawtool's vision via MCP aggregation. None implements search-first / deferred tool loading. Lean single-binary distribution + first-class per-tool ops + search-first discovery is the real gap clawtool can fill."
related:
  - "[[Research Scope 2026-04-26]]"
  - "[[004 clawtool initial architecture direction]]"
sources:
  - "[[Research Scope 2026-04-26]]"
---

# Universal Toolset Projects Comparison

## Subjects

| Subject | Stance |
|---|---|
| [[mcp-router]] | Desktop GUI, manual toggle, project/workspace metaphor |
| [[1mcp-agent]] | Lean CLI aggregator, OAuth 2.1, hot-reload config |
| [[metamcp]] | Aggregator + Orchestrator + Middleware + Gateway in Docker |
| [[docker-mcp-gateway]] | Docker official, container-isolated MCP servers, ships in Docker Desktop |

## Dimensions

| Dimension | mcp-router | 1mcp-agent | metamcp | docker-mcp-gateway |
|---|---|---|---|---|
| **Distribution** | Desktop binary | npx / native binary | Docker only | Docker (Desktop bundled) |
| **Config UX** | UI only | `mcp.json`/`.1mcprc`, hot-reload | UI + JSON | CLI dot-notation + profiles |
| **Per-tool toggle** | UI per-tool | ❌ server-level only | UI per-tool with overrides | ✅ `--enable server.tool` |
| **Tool discovery** | None | CLI inspect (manual) | Roadmap ("Elasticsearch") | `tools ls` + filter |
| **Multi-agent** | CC, Cline, Windsurf, Cursor, custom | Cursor, VSCode, CC, Codex, Cherry, Roo | Cursor, Claude Desktop (via proxy) | VS Code, Cursor, Claude Desktop |
| **Auth** | Local token (`MCPR_TOKEN`) | OAuth 2.1 scoped | Custom headers + rate limit | Docker secrets + OAuth |
| **Dependency cost** | Desktop GUI runtime | Node + binary | Docker | Docker Desktop (or daemon) |
| **License** | Sustainable Use (restrictive) | Apache 2.0 | MIT | MIT |
| **Active 2026** | v0.6.2 Jan 2026 | v0.30.3 Apr 2026 | v2.4.22 Dec 2025 | v0.41.0 Mar 2026 |

## Coverage Heatmap (against clawtool's claims)

clawtool's stated value props vs candidates:

| clawtool prop | Best existing match |
|---|---|
| Install once, use everywhere | All four (different cost profiles) |
| Configurable per-tool enable/disable | docker-mcp-gateway (CLI) and metamcp (UI) tied |
| Configurable / easy ops | 1mcp-agent (lightest), docker-mcp-gateway (most polished CLI) |
| Multi-agent reach | 1mcp-agent (broadest list explicitly named) |
| Search as primary primitive | **None** — universal gap |
| Lean single-binary install | 1mcp-agent only |
| No Docker dependency | mcp-router, 1mcp-agent |

## Industry Direction

- **MCP is the lock-in.** Every credible candidate speaks MCP. clawtool deciding "MCP server is the distribution" is no longer debatable; it's the standard.
- **Aggregator ↔ Gateway boundary is blurring.** metamcp framed itself as all four (aggregator/orchestrator/middleware/gateway). Docker doubled down on container isolation as the security boundary.
- **Per-tool ops is being normalized.** Three of four candidates already expose it; users will expect it.
- **Search is universally underdeveloped.** metamcp has it on roadmap as "Elasticsearch for MCP tool selection." docker-mcp-gateway has list+filter. mcp-router has nothing. 1mcp-agent has manual inspect. **This is where clawtool can lead.**

## Verdict

The gap clawtool fills is real but narrow:

> **Lean single-binary install + first-class CLI ergonomics for per-tool config + search-first tool discovery (semantic + deferred loading) — none of the four candidates does all three.**

If we drop "search-first," clawtool overlaps heavily with 1mcp-agent + per-tool toggle. That overlap suggests a fork-or-contribute decision before a from-scratch decision: would extending 1mcp-agent with per-tool toggle and search-first deliver clawtool's value with less effort?

Alternative framing: clawtool's distinctive identity is **search-first**, and everything else (aggregation, per-tool toggle, lean install, multi-agent) is table stakes that we copy from the best-in-class examples above.

## Open Questions Distilled

These feed into [[004 clawtool initial architecture direction]]:

1. **Build new or extend 1mcp-agent?** Lean reuse vs clean architecture.
2. **What does "search-first" concretely mean?** Three competing interpretations live in [[Agent-Agnostic Toolset]].
3. **Does clawtool need middleware (à la metamcp)?** Or is that scope creep?
4. **Container isolation: out of scope, or add as optional?** Docker MCP Gateway raises the security bar.
5. **Catalog / Registry support: read existing (Docker MCP Catalog, MCP Registry, Smithery) or define our own?**
