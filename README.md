# clawtool

[![Latest release](https://img.shields.io/github/v/release/cogitave/clawtool?display_name=tag&sort=semver&color=blue)](https://github.com/cogitave/clawtool/releases/latest)
[![CI](https://github.com/cogitave/clawtool/actions/workflows/ci.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/ci.yml)
[![Release](https://github.com/cogitave/clawtool/actions/workflows/release.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/cogitave/clawtool?logo=go)](go.mod)
[![License](https://img.shields.io/github/license/cogitave/clawtool?color=brightgreen)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/conventional--commits-1.0.0-yellow)](https://www.conventionalcommits.org)

> **Define a toolset once. Use it in every AI coding agent.**

clawtool is the standard for shipping a single, configurable toolset
across Claude Code, Codex, OpenCode and any other MCP-aware agent. One
install, one config file, every agent — same `mcp__clawtool__*` surface
everywhere.

---

## Install

```sh
curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh
```

The installer downloads the latest release tarball for your OS / arch,
verifies its SHA-256 against `checksums.txt`, and atomically installs
to `~/.local/bin/clawtool`.

<details>
<summary>Other install paths</summary>

```sh
# Pin a version
curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh -s -- --version=v0.8.6

# Or use env vars
CLAWTOOL_VERSION=v0.8.6 CLAWTOOL_INSTALL_DIR=$HOME/bin \
  curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh

# Or build from source
git clone https://github.com/cogitave/clawtool && cd clawtool
make install
```

</details>

## Plug it into Claude Code (zero ceremony)

```sh
claude plugin marketplace add cogitave/clawtool
claude plugin install clawtool@clawtool-marketplace
```

This auto-registers the MCP server and exposes `/clawtool*` slash
commands. Want Claude to **only** see clawtool's tools (no native
fallback)? Run:

```sh
clawtool agents claim claude-code
```

That adds the native `Bash`/`Read`/`Edit`/`Write`/`Grep`/`Glob`/`WebFetch`/
`WebSearch` tool names to `~/.claude/settings.json`'s `disabledTools`
array. Reverse with `clawtool agents release claude-code`. Idempotent
+ atomic + `--dry-run` available.

## What's a toolset?

A toolset is the named surface of capabilities you want your AI coding
agent to expose. Today every agent ships its own — and they're all
subtly different. clawtool replaces them with one canonical layer:

### Native-grade core tools

Wrapped at a higher quality bar than every agent's built-in equivalent.

| Tool          | Engine clawtool wraps                                | Polish (clawtool's own)                              |
|---------------|------------------------------------------------------|------------------------------------------------------|
| `Bash`        | `/bin/bash`                                          | timeout-safe (process-group SIGKILL), structured JSON |
| `Read`        | stdlib + `pdftotext` + `pandoc` + `excelize` + `go-readability` | text · PDF · Word · Excel · CSV · HTML · ipynb · json/yaml/toml/xml; stable line cursors |
| `Edit`        | stdlib (`atomic.go`)                                 | atomic temp+rename · line-ending + BOM preserve · ambiguity guard |
| `Write`       | stdlib (`atomic.go`)                                 | atomic temp+rename · parent-dir auto-create · BOM preserve |
| `Grep`        | `ripgrep` (system grep fallback)                     | uniform output across engines                        |
| `Glob`        | `bmatcuk/doublestar`                                 | bounded streaming · forward-slash output cross-platform |
| `WebFetch`    | `net/http` + `go-readability` (Mozilla port)         | UA · timeout · 10 MiB body cap · binary refusal       |
| `WebSearch`   | pluggable backend (Brave today, Tavily/SearXNG planned) | API key via secrets store · HTML markup stripped     |
| `ToolSearch`  | `bleve` (BM25)                                       | name^3 · keywords^2 · description^1 boosts; type/limit filters |

Every engine is **wrapped, never reinvented**. The polish layer
(uniform structured output, timeout-safety, BOM preserve, atomic
writes, secret redaction) is what clawtool brings.

### Source aggregation

`clawtool source add github` resolves to the canonical MCP server,
prints the auth hint, registers it. Twelve entries in the catalog out
of the box:

```
github · slack · postgres · sqlite · filesystem · fetch
brave-search · google-maps · memory · sequentialthinking · time · git
```

Sources spawn as child MCP processes; their tools are aggregated under
the wire-form name `<instance>__<tool>` (e.g.
`github-personal__create_issue`). Two GitHub accounts? Add
`github-personal` and `github-work` — collision-free by construction.

### Search-first discovery

When the catalog grows past a few dozen tools, the agent can't hold
every schema in context. `mcp__clawtool__ToolSearch` ranks candidates
by query so the agent picks the right tool without seeing every
schema:

```jsonc
ToolSearch{ query: "search file contents regex", limit: 3 }
// → {"results":[
//     {"name":"Grep",       "score":0.94, "type":"core"},
//     {"name":"Read",       "score":0.05, "type":"core"},
//     {"name":"ToolSearch", "score":0.01, "type":"core"}
//   ], "engine":"bleve-bm25", "duration_ms":1}
```

## Common workflows

```sh
# See your toolset
clawtool tools list

# Toggle a core tool
clawtool tools disable Bash       # use the agent's native Bash
clawtool tools enable  Bash       # back to clawtool's
clawtool tools status  Bash       # show which rule resolved this state

# Add a source from the catalog
clawtool source add github
clawtool source set-secret github GITHUB_TOKEN
clawtool source check

# Make Claude Code prefer clawtool exclusively
clawtool agents claim claude-code

# Dry-run any mutation first
clawtool agents claim claude-code --dry-run
clawtool tools disable github.delete_repo
```

## Configuration

A single TOML file at `~/.config/clawtool/config.toml`:

```toml
[core_tools]
[core_tools.Bash]
enabled = true

[sources.github]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
[sources.github.env]
GITHUB_TOKEN = "${GITHUB_TOKEN}"

[tools."github.delete_repo"]
enabled = false

[profile]
active = "default"
```

Secrets live separately at `~/.config/clawtool/secrets.toml` (mode
`0600`) so `config.toml` can be safely committed to dotfiles repos.
`${VAR}` references in env maps are resolved against secrets first,
then the process env.

## CLI reference

```
clawtool serve                        Run as an MCP server (stdio).
clawtool init                         Create ~/.config/clawtool/config.toml.
clawtool version                      Print the build version.

clawtool tools list                   List core tools and resolved enabled state.
clawtool tools enable  <selector>     Enable a tool.
clawtool tools disable <selector>     Disable a tool (refuses ambiguous selectors).
clawtool tools status  <selector>     Show resolved state + rule that won.

clawtool source add <name> [--as <instance>]
                                      Resolve <name> from the built-in catalog.
clawtool source list                  Configured sources + auth status.
clawtool source remove <instance>     Drop from config (secrets retained).
clawtool source set-secret <instance> <KEY> [--value <v>]
                                      Store a credential (stdin fallback).
clawtool source check                 Verify required env per source.

clawtool agents list                  Show registered agent adapters.
clawtool agents claim   <agent> [--dry-run]
                                      Disable native equivalents in <agent>.
clawtool agents release <agent> [--dry-run]
                                      Reverse a previous claim.
clawtool agents status  [<agent>]     Per-agent claim state.
```

## Development

```sh
make build              # → ./bin/clawtool
make test               # go test -race ./...
make e2e                # spawn binary, drive MCP over stdio, assert
make install            # atomic copy to ~/.local/bin/clawtool
make changelog          # regenerate CHANGELOG.md from git history
make release-snapshot   # GoReleaser dry-run (no publish)
```

Test totals at v0.8.6: **134 Go unit + 57 e2e = 191 green** across
8 packages.

The release pipeline is fully automated:
[Conventional Commits](https://www.conventionalcommits.org) on `main`
→ [release-please](https://github.com/googleapis/release-please) opens
a "release PR" → merging the PR cuts the tag → [GoReleaser](https://goreleaser.com)
publishes signed tarballs to GitHub Releases. Manual `git tag` is
deprecated.

## Status

Path to v1.0 is gated by six criteria:

|                                          | Status                  |
|------------------------------------------|-------------------------|
| Real-world soak (≥ 1 week)               | ⏳ pending               |
| Canonical core list shipped              | ✅ v0.8.6                |
| CI matrix on linux + macOS               | ✅ v0.8.6                |
| Signed binary release pipeline           | 🟢 GoReleaser + Releases |
| Versioned API stability promise          | ⏳ pending               |
| Multi-instance against ≥ 3 real upstreams | ⏳ pending               |
| Plugin packaging for Claude Code         | ✅ v0.8.6                |

Until all are green, every increment is a patch (`v0.8.x`).

## Contributing

PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow
(Conventional Commits required, test discipline) and
[SECURITY.md](SECURITY.md) for vulnerability disclosure.

## License

[MIT](LICENSE)
