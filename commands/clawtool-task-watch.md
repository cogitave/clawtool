---
description: Stream BIAM task progress to the operator's chat as inline events. Pair with the Monitor tool so async dispatches become visible without polling TaskGet.
allowed-tools: mcp__clawtool__Bash, Monitor
argument-hint: <task_id> | --all
---

The operator wants to SEE background dispatches as they progress —
without polling `TaskGet` themselves. `clawtool task watch` emits
one stdout line per state transition; pair it with Claude Code's
native Monitor tool and every `active → done` (or `failed`,
`cancelled`) shows up as an inline chat event.

Two modes:

**Single task** — when the operator already has a task_id:
```bash
clawtool task watch <task_id>
```
Exits when the task hits a terminal state.

**All in-flight dispatches** — session-length watch:
```bash
clawtool task watch --all
```
Runs until cancelled. Right shape for `Monitor` with
`persistent: true`.

**Pairing with Monitor**:
Use the native `Monitor` tool with these args:
- `command`: `clawtool task watch --all`
- `description`: `BIAM task progress`
- `persistent`: `true` (so it survives across the operator's
  conversation turns)
- `timeout_ms`: irrelevant when persistent

Each stdout line becomes a chat-visible event:
```
[15:32:01] 8f9b41c3 · ACTIVE · agent=codex
[15:32:45] 8f9b41c3 · DONE · agent=codex · 2 msg · result tail capped at 80…
```

**Format flag** — `--json` switches to NDJSON for downstream
piping (jq, log shippers). Operators using Monitor stay on the
default human-readable form; bots / pipelines use `--json`.

**Polling cadence** — default 250ms. SQLite WAL keeps the cost
negligible. Tunable via `--poll-interval`; minimum 50ms (clamped).

**Hard rule**: NEVER advertise this as a way to retrieve full
task bodies. Watch lines cap `last_message` at 80 chars by
design; for the full body call `mcp__clawtool__TaskGet` or
`clawtool task get <task_id>`. Surfacing a megabyte completion
blob into the operator's chat is its own outage.
