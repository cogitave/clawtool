---
type: entity
title: "docker-mcp-gateway"
created: 2026-04-26
updated: 2026-04-26
tags:
  - entity
  - tool
  - mcp-aggregator
  - gateway
  - industry
status: developing
entity_type: product
role: "Docker official MCP gateway — industry reference, ships in Docker Desktop"
first_mentioned: "[[Research Scope 2026-04-26]]"
related:
  - "[[Universal Toolset Projects Comparison]]"
sources:
  - "[[Research Scope 2026-04-26]]"
---

# docker-mcp-gateway

Docker's official CLI plugin that manages lifecycle and deployment of MCP servers as Docker containers. Provides a unified interface for AI applications to access external data sources and tools securely. Ships pre-installed in Docker Desktop 4.59+ via the MCP Toolkit feature.

## Key Facts

- **Repo**: https://github.com/docker/mcp-gateway
- **License**: MIT
- **Stars**: 1.4k; 915 commits; 79 open issues; 32 PRs — active and at industrial scale
- **Latest release**: v0.41.0 (2026-03-18)
- **Distribution**: Bundled with Docker Desktop 4.59+. Manual install via `make docker-mcp` from clone. WSL2 / containerized environment supported with `DOCKER_MCP_IN_CONTAINER=1`.
- **MCP Clients supported**: VS Code, Cursor, Claude Desktop (more not exhaustively listed). `docker mcp client connect <client-name>` manages client connections.

## Per-Tool Granularity

**Yes**, allowlist-style via dot-notation. Strongest CLI ergonomics of the four:
```bash
docker mcp profile tools <profile-id> --enable github.create_issue
docker mcp profile tools <profile-id> --disable github.delete_repo
docker mcp profile tools <profile-id> --enable-all
```
Profiles bundle tool selections. `--enable-all` / `--disable-all` for bulk.

## Tool Discovery / Search

Most developed of the four candidates:
- `docker mcp tools ls` — plaintext or JSON output of available tools
- `docker mcp tools count`
- `docker mcp tools inspect <tool-name>`
- `docker mcp profile server ls --filter name=<pattern>` — pattern-based filtering

Still: it's listing + filter, not semantic search or deferred loading.

## Configuration

Profiles reference servers via:
- Catalog: `catalog://mcp/docker-mcp-catalog/github`
- OCI image: `docker://my-server:latest`
- MCP Registry URL
- Local file: `file://./server.yaml`

Dot-notation set: `docker mcp profile config <profile-id> --set github.timeout=30`. Persistence in local DB. Feature flags in `~/.docker/config.json`.

## Auth / Security

- Docker secrets manager handles API keys/credentials
- Built-in OAuth: `docker mcp oauth` commands
- Servers run in **isolated Docker containers** with minimal host privileges
- `CLAUDE_CONFIG_DIR` env var customizes client config storage

## Limitations

- **Requires Docker** (Desktop or daemon) — non-Docker environments can't use it
- Not a complete MCP spec implementation; deployment-focused
- Catalog / OCI / Registry resolution adds operational complexity
- No documented custom MCP server *development* tooling

## Relevance to clawtool

- **Industry validation** — Docker bet on this pattern; signals MCP-aggregator as a category, not a niche.
- **CLI ergonomics** are the gold standard to measure clawtool's UX against. The dot-notation tool selector (`--enable github.create_issue`) is excellent.
- **Profile concept** maps to per-project tool sets, similar to mcp-router's "Workspaces" but CLI-native.
- **Dependency cost is the trade** — Docker requirement is the very thing clawtool can win on. Many CC users on WSL/Linux don't want Docker overhead for this.
- **Catalog / OCI integration** is interesting; clawtool could read these as one of several source types, leveraging existing Docker catalog without requiring Docker for runtime.
- **Container isolation** is a security posture clawtool will not match without containers — call this out as a deliberate trade.
