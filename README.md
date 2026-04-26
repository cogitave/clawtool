# clawtool

The canonical tool layer for AI coding agents. One binary, one config, every agent.

> **Status:** v0.1 — single tool (Bash), single transport (stdio), end-to-end working. v0.2 in progress.
> **License:** [MIT](LICENSE).
> **Design rationale:** see `wiki/decisions/` (ADR-001 through ADR-006).

clawtool is an MCP server you install once and connect every AI coding agent to. It ships canonical implementations of the tools agents already use (Bash, Read, Edit, Write, Grep, Glob, WebFetch) at a higher quality bar than each agent's native built-in, and provides a `ToolSearch` primitive so a 50+ tool catalog actually scales (deferred loading + semantic discovery).

## Why

- **One source of truth.** Configuration lives at `~/.config/clawtool/config.toml`. Switching agents does not switch toolsets.
- **Multi-instance.** Two GitHub accounts? Add `github-personal` and `github-work` — wire form `github-personal__create_issue` is collision-free by construction (ADR-006).
- **Selectors that compose.** `clawtool tools disable github` then `clawtool tools enable github.create_issue` leaves only `create_issue` enabled. Tags and groups follow the same model. Tool-level wins same-level (`deny wins`).
- **No Docker requirement.** Single Go binary, ~7 MB, install via npm / brew / curl.

## Install

```bash
go build -o bin/clawtool ./cmd/clawtool
cp bin/clawtool ~/.local/bin/clawtool
clawtool init
claude mcp add-json clawtool '{"type":"stdio","command":"'"$HOME"'/.local/bin/clawtool","args":["serve"]}' --scope user
```

## Use

```bash
clawtool tools list
clawtool tools disable Bash                       # use the agent's native Bash
clawtool tools enable Bash                        # use clawtool's
clawtool tools status Bash                        # show resolved state
```

In any MCP-aware agent (Claude Code, Codex, Cursor, …) clawtool's tools appear as `mcp__clawtool__Bash`, `mcp__clawtool__Read`, etc.

## Repo layout

```
clawtool/
├── cmd/clawtool/         # entrypoint
├── internal/
│   ├── server/           # MCP server bootstrap
│   ├── tools/core/       # canonical tools (Bash, ...)
│   ├── config/           # config.toml read/write + resolution
│   ├── cli/              # subcommand handlers
│   └── version/
├── test/e2e/             # end-to-end MCP integration test
├── wiki/                 # project brain — decisions, comparisons, entities
├── _templates/           # Obsidian note templates
├── Makefile
└── go.mod
```

## Development

```bash
make build       # build to ./bin/clawtool
make test        # go test ./...
make e2e         # spawn binary, send MCP messages, assert responses
make install     # copy to ~/.local/bin
make clean       # remove ./bin and ./dist
```

## Architecture in one paragraph

clawtool is one MCP server (`mcp__clawtool__*`) that aggregates canonical core tools (PascalCase: `Bash`, `Read`, `Edit`, …) plus user-attached source instances (kebab-case + tool: `github-personal__create_issue`). Wire and CLI surfaces use distinct separators (`__` vs `.`) so parsing is unambiguous. Config is a single TOML file watched for hot-reload. Search is the prerequisite that makes a 50+ tool catalog usable — `ToolSearch` (deferred schema loading + semantic ranking) is clawtool's identity feature; everything else (aggregation, per-tool toggle, single binary) is table stakes copied from the best-in-class projects (`mcp-router`, `1mcp-agent`, `metamcp`, `docker mcp-gateway`) — see `wiki/comparisons/universal-toolset-projects.md`.
