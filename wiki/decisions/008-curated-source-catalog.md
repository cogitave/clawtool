---
type: decision
title: "008 Curated source catalog with name-only ergonomics"
aliases:
  - "008 Curated source catalog with name-only ergonomics"
  - "ADR-008"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - ux
  - catalog
status: developing
related:
  - "[[004 clawtool initial architecture direction]]"
  - "[[006 Instance scoping and tool naming]]"
  - "[[007 Leverage best-in-class not reinvent]]"
sources:
  - "[[Canonical Tool Implementations Survey 2026-04-26]]"
---

# 008 — Curated Source Catalog with Name-Only Ergonomics

> **Status: developing.** UX-defining ADR. Locks the "user types a name, clawtool does the rest" promise for sourced tools.

## Context

ADR-006 specified the wire shape and CLI selector grammar for instances. ADR-004 sketched a CLI like:

```
clawtool source add github -- npx -y @modelcontextprotocol/server-github
```

The user pushed back:

> *"Kullanıcılar bu kadar uzun şeyler yazmaz github vs. standarttır standartları biz direkt biliyor olmalıyız. clawtool source add github dediğimizde zaten onun doğru şekilde eklenmesi lazım."*
>
> Users won't type that long thing. github etc. are standards — we should know the standards directly. When the user says `clawtool source add github`, it should just install correctly.

This is the right product instinct. The npx invocation, env-var requirements, auth flow, OAuth hints, package version pinning — none of these are the user's problem. They are a curation problem. clawtool owns the curation.

## Decision

clawtool ships a **built-in source catalog**. `clawtool source add <name>` resolves the name against the catalog, picks the canonical implementation, and installs it without further user input — except a single helpful prompt for credentials when the source needs them.

```bash
clawtool source add github
# → resolves to @modelcontextprotocol/server-github, default name 'github',
#   detects $GITHUB_TOKEN, prints:
#     "✓ added source 'github' (powered by @modelcontextprotocol/server-github v...)
#      ! GITHUB_TOKEN not set. Run:
#          clawtool source set-secret github GITHUB_TOKEN
#        Get a token at https://github.com/settings/tokens?scopes=repo,read:org"

clawtool source add github --as github-personal --token $TOKEN
# → same canonical command, instance name forced (per ADR-006 second-instance rule),
#   token captured into config 0600

clawtool source add slack
clawtool source add notion
clawtool source add postgres --url postgres://...

clawtool source list
# → instance         status    powered_by                     last_seen
#   github           ✓ healthy @modelcontextprotocol/server-github@1.4.2  2s ago
#   slack            ✗ no auth @modelcontextprotocol/server-slack@…       —
```

The catalog entry for each known source captures:

```toml
[catalog.github]
package      = "@modelcontextprotocol/server-github"
runtime      = "npx"   # npx | node | python | docker | binary
description  = "GitHub: issues, PRs, code search, repository operations"
required_env = ["GITHUB_TOKEN"]
auth_hint    = "Generate a token at https://github.com/settings/tokens (scopes: repo, read:org)"
homepage     = "https://github.com/modelcontextprotocol/servers/tree/main/src/github"
maintained   = "anthropic"   # informational; for trust signaling
```

Any source not in the catalog can still be added the long way:

```bash
clawtool source add custom-thing -- npx -y my-org/my-mcp-server
```

So the catalog is **a fast path, not a gate**.

## Catalog source — built-in vs read-from-registry

Per ADR-007, we prefer wrapping mature work over reinventing. The MCP ecosystem already has catalogs:

- **Docker MCP Catalog** — curated, container-focused; reachable via `catalog://mcp/docker-mcp-catalog/<name>` (see [[docker-mcp-gateway]])
- **MCP Registry** — the upstream MCP project's directory
- **Smithery** — 7000+ servers, registry-focused (see [[Universal Toolset Projects Comparison]])

clawtool's catalog plan is the same hybrid as ADR-007 says for tools:

1. **Ship a built-in baseline** (~30–50 hand-curated entries — github, slack, notion, postgres, sqlite, fetch, sequential-thinking, …) so first-run is offline-capable and trust is earned.
2. **Optionally federate** with external registries at runtime via `clawtool source add github --from smithery` or auto-fall-through if the local catalog misses a name.
3. **Trust signaling**: the `maintained` field shows whether an entry is from us, Anthropic, OpenAI, Docker, or community. Enables future `clawtool source add github --trust anthropic` filtering.

## Authentication UX

Most sources need credentials. The catalog says which env vars are required; clawtool's surface is consistent:

```bash
clawtool source set-secret github GITHUB_TOKEN
# → reads from prompt (terminal, masked) OR from --file or stdin
# → writes to ~/.config/clawtool/secrets.toml (mode 0600), separate from
#   config.toml so the latter can be safely committed

clawtool source check
# → resolves each source's required_env and reports which are missing
```

Secrets file is **not** part of `config.toml` to keep the public-shareable config clean. References from config.toml use `${vars}` interpolation that consults the secrets file then the process env.

## Tool name convention reminder (from ADR-006)

When a sourced instance ships, its tools appear as `<instance>__<tool>`. With `clawtool source add github` the default instance is `github`, so tools are `github__create_issue`. With `clawtool source add github --as github-personal`, tools are `github-personal__create_issue`.

Adding a second `clawtool source add github` is rejected with an explicit error pointing at `--as`:

```
✘ instance name 'github' already in use. Use --as <name> (e.g. --as github-work)
  to add a second instance, and consider renaming the existing instance:
    clawtool source rename github github-personal
```

This implements ADR-006's "first-instance bare name allowed; second forces explicit rename" rule.

## Per-runtime support

| `runtime` value | Meaning |
|---|---|
| `npx`  | Node-based MCP server fetched via `npx -y <package>`. The most common case in 2026. |
| `node` | Same package but assumes already installed (`node node_modules/<...>`). For air-gapped setups. |
| `python` | `uvx <package>` or `pipx run <package>`. |
| `docker` | Defer to the Docker MCP Gateway-style catalog. clawtool spawns `docker run …`. |
| `binary` | Pre-built native binary. clawtool downloads + verifies checksum on `add`. |

`clawtool source add github` does not pin a version by default (always-latest); pinning is opt-in:

```bash
clawtool source add github@1.4.2
```

## Alternatives Rejected

- **Force users to write the long npx command.** Defeats the entire UX premise. Rejected.
- **Make every source a separate clawtool plugin.** Plugin-per-source explodes maintenance and obscures discoverability. The catalog is one file we update.
- **Defer everything to MCP Registry / Smithery, ship no built-in catalog.** Network dependency on first-run; slow; fragile when registries move; less control over trust signaling. Built-in baseline is essential.
- **Ship the catalog only as remote JSON downloaded on first run.** OK as a refresh path, not as the primary path.
- **Per-source CLI verbs (`clawtool github add`).** Doesn't scale; users would have to learn a new verb per source.

## Consequences

- The next milestone before shipping multi-instance is **the catalog file format** + a starter set of ~10 entries (github, slack, postgres, sqlite, notion, fetch, brave-search, time, sequential-thinking, filesystem). These come from [[Canonical Tool Implementations Survey 2026-04-26]] which extends to source servers in v0.4.
- `clawtool source list` becomes a key UX surface — health checks per source ("authenticated?", "responding?", "version") become table stakes.
- `clawtool source set-secret` is the only safe path for credentials. Reading from env-only is fine for development; committing tokens to config.toml is forbidden by file layout, not just by policy.
- Documentation: every catalog entry has a one-line description that becomes the source's MCP discovery surface (helps `ToolSearch` rank it).
- Security: catalog entries with `runtime = "npx"` execute upstream code. We document this clearly. v0.5+ adds optional package-signature verification (`npm-audit-signatures`-style).

## Status

Developing. Lands in v0.4 alongside the source-instance feature. The catalog file format will be published as `internal/catalog/catalog.toml` and made discoverable in [[Canonical Tool Implementations Survey 2026-04-26]].
