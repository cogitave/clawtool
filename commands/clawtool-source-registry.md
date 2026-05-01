---
description: Probe an MCP catalog and list the first N servers. Backends: registry.modelcontextprotocol.io (default), registry.smithery.ai, or both merged + deduped. Read-only; doesn't touch local config or secrets.
argument-hint: [--limit N] [--url URL] [--json] [--backend mcp|smithery|both]
allowed-tools: mcp__clawtool__Bash, mcp__clawtool__SourceRegistry
---

Wraps `clawtool source registry`. Discover MCP servers from
upstream catalogs without committing to install. The companion
to `clawtool source catalog` (which browses clawtool's *built-in*
hand-curated catalog).

## When to use

- "What's available out there?" — the operator wants to scan
  upstream catalogs (the official MCP registry + Smithery) for
  servers that fit a goal, without installing anything yet.
- Surfacing recent additions — a new MCP server shipped this week;
  the registry endpoint will list it before clawtool's built-in
  catalog absorbs it.
- Pipelining into install flow — `--json` lets a downstream script
  parse server names + versions and decide which ones to pass to
  `clawtool source add`.

## Example invocation

Default — first 10 servers from the official MCP registry:
```bash
clawtool source registry
```

Bigger sample, JSON output:
```bash
clawtool source registry --limit 50 --json
```

Smithery only:
```bash
clawtool source registry --backend smithery
```

Merge + dedupe both backends by server name (handy for "what's
the union of all known MCP servers?"):
```bash
clawtool source registry --backend both --limit 100
```

Custom registry URL — useful for self-hosted catalogs:
```bash
clawtool source registry --url https://registry.example.com/v1
```

## What happens

- Probes the chosen backend(s). Default endpoint:
  `https://registry.modelcontextprotocol.io`. Smithery:
  `https://registry.smithery.ai`. Both: parallel fetch + dedupe
  by name.
- Renders text output (one server per line, name + version +
  one-line description) or `--json` for pipelines.
- Read-only — no config or secrets touched. Pick names from the
  output and hand them to `clawtool source add` if you want to
  install.

## Common pitfalls

- The MCP equivalent for chat-driven flows is
  `mcp__clawtool__SourceRegistry`. Use that when an agent needs
  the catalog inline rather than shelling out.
- `--backend both` dedupes by server *name*, not version — two
  servers named the same in different registries collapse to one
  row. If you need version-resolution detail, query each backend
  separately.
- The endpoints are external services; transient HTTP errors are
  surfaced as a non-zero exit. Retry once before assuming the
  registry is down.
