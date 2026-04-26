---
type: entity
title: "metamcp"
created: 2026-04-26
updated: 2026-04-26
tags:
  - entity
  - tool
  - mcp-aggregator
  - middleware
  - gateway
status: developing
entity_type: product
role: "Comprehensive MCP aggregator + orchestrator + middleware + gateway"
first_mentioned: "[[Research Scope 2026-04-26]]"
related:
  - "[[Universal Toolset Projects Comparison]]"
sources:
  - "[[Research Scope 2026-04-26]]"
---

# metamcp (metatool-ai/metamcp)

An MCP proxy that aggregates multiple MCP servers into a unified endpoint, applies middlewares, and enables dynamic composition. Functions itself as an MCP server compatible with any MCP client.

## Key Facts

- **Repo**: https://github.com/metatool-ai/metamcp
- **License**: MIT
- **Stars**: 2.3k; 797 commits; very active
- **Latest release**: v2.4.22 (2025-12-19) — security updates, custom headers, tool sync caching
- **Distribution**: Docker / Docker Compose / Dev Containers (VSCode/Cursor). Published to GHCR.
- **MCP Clients supported**: Cursor, Claude Desktop (via local proxy like `mcp-proxy`), any client speaking SSE / Streamable HTTP. OpenAPI surface for Open WebUI and similar.

## Four Functions in One

| Function | Meaning |
|---|---|
| **Aggregator** | Combines multiple MCP servers under one endpoint |
| **Orchestrator** | Groups servers into namespaces with enable/disable per namespace |
| **Middleware** | Intercepts and transforms requests/responses |
| **Gateway** | Hosts endpoints with auth + rate limiting |

This single-tool-many-functions framing is the project's most distinctive feature.

## Per-Tool Granularity

**Yes** — Tools tab within each namespace allows inline editing: tool name / title / description editable, per-tool toggle overrides. Strongest per-tool UX of the four candidates.

## Tool Discovery / Search

Static listing from aggregated servers, with middleware filtering. **"Use as Elasticsearch for MCP tool selection"** is on the roadmap — not implemented yet. Same gap as the other candidates.

## Configuration

JSON for STDIO MCP servers, e.g.:
```json
"HackerNews": {
  "type": "STDIO",
  "command": "uvx",
  "args": ["mcp-hn"]
}
```
Endpoint creation through UI; connection via `http://localhost:12008/metamcp/<ENDPOINT_NAME>/sse`.

## Auth Model

Custom headers; rate limiting (in-memory counters; not cluster-aware).

## Limitations

- **Docker-only**, no native binary path documented.
- Cold-start delays mitigated via pre-allocated idle sessions (operational footprint).
- Rate limiting in-memory: not viable for clustered deployments.
- SSE / Streamable HTTP only — Claude Desktop requires `mcp-proxy` wrapper for STDIO.
- `?api_key=` query auth unsupported on SSE endpoints.

## Relevance to clawtool

- **Most feature-complete reference.** If we want to know "what does the maximum look like?", metamcp is the answer.
- **Per-tool override UX** is the model to study. clawtool's per-tool config should match or exceed this.
- **Middleware concept** is interesting (transform requests before reaching tool) — possible clawtool extension point, but raises complexity.
- **Docker dependency is the cost.** clawtool can win on lightness if we target single-binary distribution.
- **Search/discovery is on their roadmap, not built.** Confirms this is genuinely uncovered ground across the field.
