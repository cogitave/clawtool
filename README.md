# clawtool

[![Latest release](https://img.shields.io/github/v/release/cogitave/clawtool?display_name=tag&sort=semver&color=blue)](https://github.com/cogitave/clawtool/releases/latest)
[![CI](https://github.com/cogitave/clawtool/actions/workflows/ci.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/ci.yml)
[![Release](https://github.com/cogitave/clawtool/actions/workflows/release.yml/badge.svg)](https://github.com/cogitave/clawtool/actions/workflows/release.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/cogitave/clawtool?logo=go)](go.mod)
[![License](https://img.shields.io/github/license/cogitave/clawtool?color=brightgreen)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/conventional--commits-1.0.0-yellow)](https://www.conventionalcommits.org)

> **Define a toolset once. Use it in every AI coding agent.**
> clawtool is the standard layer across Claude Code, Codex, and OpenCode.

A single Go binary that does three things:

1. **Canonical toolset.** Every MCP-aware agent gets the same
   higher-quality `Bash` / `Read` / `Edit` / `Write` / `Grep` / `Glob` /
   `WebFetch` / `WebSearch` / `ToolSearch` — wrapping ripgrep, pandoc,
   poppler, doublestar, Mozilla Readability and friends. No more "your
   agent's `Read` doesn't speak xlsx" or "Bash on this client times out
   on the wrong signal."
2. **Bridge layer.** `clawtool bridge add codex` installs the official
   Codex Claude Code plugin; `clawtool bridge add gemini` / `bridge add
   opencode` do the same for Gemini and OpenCode. `clawtool send
   --agent <instance> "<prompt>"` then routes a single prompt to the
   right CLI from inside Claude Code, from a CI hook, or over an HTTP
   relay (v0.10 → v0.12; see [ADR-014](wiki/decisions/014-clawtool-relay-and-cli-multiplexer.md)).
3. **Project-setup wizard.** `clawtool init` injects the canonical
   project-setup tools (release-please, GoReleaser, Conventional
   Commits CI, Dependabot, CODEOWNERS, an SPDX-licensed `LICENSE`, an
   Obsidian-backed memory layer) — running each upstream's own init,
   never re-implementing them.

```sh
curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh
clawtool init
```

That's it. Pick what you want set up; clawtool runs each upstream's
own init and drops the canonical glue config. **No reinvention** —
release-please is googleapis/release-please, brain is claude-obsidian,
license texts are SPDX. clawtool is the wizard, not a fork.

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

That writes the native `Bash`/`Read`/`Edit`/`Write`/`Grep`/`Glob`/`WebFetch`/
`WebSearch` tool names into `~/.claude/settings.json`'s
`permissions.deny` list — Claude Code refuses to invoke them, the
model sees only `mcp__clawtool__*`. Reverse with `clawtool agents
release claude-code`. Idempotent + atomic + `--dry-run` available.

## Set up a repo in 30 seconds

```sh
cd my-repo
clawtool init
```

The wizard asks what scope to set up — your repo, your global
clawtool, both, or just preview — then walks 9 categories
(governance, commits, release, ci, quality, supply-chain, knowledge,
agents, runtime). Pick what you want; everything else is skipped.

Recipes shipped today:

| Category | Recipe | Wraps |
|---|---|---|
| governance | `license` | SPDX (MIT · Apache-2.0 · BSD-3-Clause · AGPL-3.0) |
| governance | `codeowners` | GitHub CODEOWNERS spec |
| commits | `conventional-commits-ci` | `amannn/action-semantic-pull-request` |
| release | `release-please` | googleapis/release-please |
| release | `goreleaser` | GoReleaser v2 |
| ci | `gh-actions-test` | GitHub Actions (Go / Node / Python / Rust auto-detect) |
| quality | `prettier` | prettier.io (cross-language formatter) |
| quality | `golangci-lint` | golangci-lint v2 (errcheck/govet/staticcheck/gosec/…) |
| supply-chain | `dependabot` | GitHub Dependabot |
| knowledge | `brain` | claude-obsidian + Obsidian app |
| agents | `agent-claim` | `clawtool agents claim` per-agent |
| agents | `caveman` | lackeyjb/caveman Claude Code skill (Beta) |
| agents | `superclaude` | SuperClaude framework (slash commands + personas, Beta) |
| agents | `claude-flow` | ruvnet/claude-flow multi-agent orchestration (Beta) |
| runtime | `devcontainer` | containers.dev (Codespaces / Remote-SSH) |

Every recipe **detects** before it touches anything, **refuses** to
overwrite a file you wrote yourself, and **records** what it touched
in `.clawtool.toml` so you can re-run safely. Each one wraps a
maintained upstream — clawtool is the wizard, never the
implementation.

Prefer one shot? `clawtool recipe apply license holder="Jane Doe"`.
Need to overwrite a file you wrote yourself? `--force` is the
explicit knob; the wizard prompts for it interactively.

Want Claude to set things up from inside a chat? Just say "set me
up" — the `/clawtool` skill teaches the model to walk the same
recipes via `mcp__clawtool__RecipeApply`.

## Author your own skills (agentskills.io standard)

```sh
clawtool skill new my-skill --description "What this skill does and when to load it." \
                            --triggers "save this, file this, log this"
```

Scaffolds a folder under `~/.claude/skills/my-skill/` (or
`./.claude/skills/my-skill/` with `--local`) containing a
spec-compliant `SKILL.md` plus the optional `scripts/`,
`references/`, `assets/` subdirectories from the
[agentskills.io](https://agentskills.io) standard. The model can
also do this from inside a chat — same template — via
`mcp__clawtool__SkillNew`.

`clawtool skill list` enumerates installed skills; `clawtool skill
path <name>` prints the directory.

## Diagnose your setup

```sh
clawtool doctor
```

One command that surveys the binary, agent claims, source
credentials, and recipe statuses for the current repo. Each row
ends in ✓ / ⚠ / ✗ with a suggested fix command for everything that
isn't healthy. Exit code is non-zero only on critical issues, so it
fits into CI / shell guards too.

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
prints the auth hint, registers it. Eighteen entries in the catalog
out of the box:

```
github · slack · postgres · sqlite · filesystem · fetch
brave-search · google-maps · memory · sequentialthinking · time · git
context7 · playwright · desktop-commander · exa · notion · atlassian
```

Pick what you need; clawtool installs none by default.

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
clawtool init [--yes]                 Interactive setup wizard. --yes for
                                      non-interactive Stable defaults.
clawtool version                      Print the build version.

clawtool recipe list [--category <c>] List project-setup recipes by category.
clawtool recipe status [<name>]       Detect status for one or all recipes.
clawtool recipe apply  <name> [--force] [k=v…]
                                      Apply a single recipe. --force lets it
                                      overwrite an unmanaged user file.

clawtool doctor                       Survey the local install + suggest fixes.

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

Test totals at v0.9: **~200 Go unit + 68 e2e green** across
12 packages, race-clean.

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
