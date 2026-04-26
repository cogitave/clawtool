---
type: source
title: "Prototype Bringup 2026-04-26"
aliases:
  - "Prototype Bringup 2026-04-26"
created: 2026-04-26
updated: 2026-04-26
tags:
  - source
  - prototype
  - bringup
status: mature
source_type: meta
author: "build session"
date_published: 2026-04-26
url: ""
confidence: high
key_claims:
  - "v0.1 prototype binary builds and serves MCP over stdio."
  - "Bash core tool registered with PascalCase name per ADR-006."
  - "Process-group SIGKILL on context cancel: timeout returns in ~500ms even when child sleep runs 3s. ADR-005 timeout-safe bar met for Bash."
  - "Installed at ~/.local/bin/clawtool; registered with Claude Code at user scope; CC reports Connected."
related:
  - "[[004 clawtool initial architecture direction]]"
  - "[[005 Positioning replace native agent tools]]"
  - "[[006 Instance scoping and tool naming]]"
sources: []
---

# Prototype Bringup — 2026-04-26

First end-to-end working state of clawtool. Single core tool (`Bash`), single MCP transport (stdio), no config / no source instances yet.

## What was built

```
clawtool/
├── go.mod                                  # module github.com/cogitave/clawtool
├── go.sum
├── cmd/clawtool/main.go                    # entrypoint with serve / version / help
└── internal/
    ├── version/version.go                  # name, version string
    ├── server/server.go                    # MCP server setup, registers core tools
    └── tools/core/
        ├── bash.go                         # Bash tool: timeout-safe, structured output
        ├── exec.go                         # split-output runner, homeDir helper
        ├── exec_unix.go                    # process-group SIGKILL on cancel
        └── exec_other.go                   # no-op for non-unix
```

Dependencies:
- `github.com/mark3labs/mcp-go v0.49.0` — community MCP SDK chosen for maturity
- transitive: `github.com/google/jsonschema-go`, `github.com/spf13/cast`, etc.

## Quality bar verification (Bash)

ADR-005 specifies Bash must "preserve output on timeout." Two tests:

| Test | Input | Expected | Got |
|---|---|---|---|
| Normal call | `echo hello && pwd && date +%s` | stdout populated, exit 0, `timed_out: false` | ✅ stdout="hello\n/home/arda\n…", exit 0, 4ms |
| Timeout | `echo before; sleep 3; echo after` with `timeout_ms: 500` | stdout has `before\n`, `timed_out: true`, returns near 500ms | ✅ stdout="before\n", `timed_out: true`, **501ms** |

Without the process-group fix (`internal/tools/core/exec_unix.go`), the timeout test returned at ~3005ms because bash's `sleep` child kept stdout/stderr pipes open after bash itself was killed. Setting `Setpgid: true` and overriding `cmd.Cancel` to `syscall.Kill(-pid, SIGKILL)` reaps the whole tree.

## How to install and use

```bash
# Build (in repo root)
cd /mnt/c/Users/Arda/workspaces/@cogitave/clawtool
/usr/local/go/bin/go build -o bin/clawtool ./cmd/clawtool

# Install for user
cp bin/clawtool ~/.local/bin/clawtool   # ~/.local/bin already on PATH

# Register with Claude Code (user-scope, persistent across sessions)
claude mcp add-json clawtool '{"type":"stdio","command":"/home/arda/.local/bin/clawtool","args":["serve"]}' --scope user

# Verify
claude mcp list
# expected: clawtool: /home/arda/.local/bin/clawtool serve - ✓ Connected
```

Open any Claude Code session anywhere; `mcp__clawtool__Bash` is available alongside the agent's native `Bash`. Both work; the user (or a per-project `.mcp.json` override) decides which is preferred.

## Tool surface

```jsonc
// what Claude sees in tools/list
{
  "name": "Bash",
  "description": "Run a shell command via /bin/bash. Returns structured JSON with stdout, stderr, exit_code, duration_ms, timed_out, and cwd. Output is preserved even when the command times out.",
  "inputSchema": {
    "type": "object",
    "required": ["command"],
    "properties": {
      "command":    { "type": "string", "description": "..." },
      "cwd":        { "type": "string", "description": "..." },
      "timeout_ms": { "type": "number", "description": "..." }
    }
  },
  "annotations": {
    "readOnlyHint": false,
    "destructiveHint": true,
    "idempotentHint": false,
    "openWorldHint": true
  }
}
```

Result is text-content JSON:

```json
{"stdout":"...","stderr":"...","exit_code":0,"duration_ms":4,"timed_out":false,"cwd":"/home/arda"}
```

## Not yet implemented (v0.1 scope cut)

- Other core tools (`Read`, `Edit`, `Write`, `Grep`, `Glob`, `WebFetch`)
- `ToolSearch` primitive — the search-first identity feature
- `~/.config/clawtool/config.toml` read/write + hot-reload
- CLI subcommands (`init`, `tools enable/disable/status`, `source add`, `profile`, `group`)
- Source instances (multi-account GitHub etc.)
- Secret redaction in Bash output
- Per-session command history

These are tracked as next-iteration work. The point of v0.1 is to **prove the loop works** end-to-end: build → install → register → call → respond.

## Build invocation reference

```bash
# Local dev: build to ./bin/
/usr/local/go/bin/go build -o bin/clawtool ./cmd/clawtool

# Module-style install (puts binary in $GOBIN, default ~/go/bin)
/usr/local/go/bin/go install ./cmd/clawtool

# Cross-compile (release work later)
GOOS=darwin GOARCH=arm64 /usr/local/go/bin/go build -o dist/clawtool-darwin-arm64 ./cmd/clawtool
GOOS=linux  GOARCH=amd64 /usr/local/go/bin/go build -o dist/clawtool-linux-amd64  ./cmd/clawtool
```
