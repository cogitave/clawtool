---
description: Launch clawtool's runtime TUI dashboard — three-pane view of BIAM dispatches, agent registry, and stats. Updates live every second.
allowed-tools: mcp__clawtool__Bash
---

The operator wants a live overhead view of every active BIAM
dispatch + the agent registry + dispatch stats — the deferred
v0.19 multi-pane sketch. `clawtool dashboard` (or `clawtool tui`)
opens a Bubble Tea TUI on the operator's terminal.

```bash
clawtool dashboard
```

Three panes refresh on a 1-second poll over the BIAM SQLite store:

- **Pane 1 — Dispatches**: every recent task, active first.
  Status chip is colour-coded (active = orange, done = green,
  failed/cancelled = red).
- **Pane 2 — Agents**: supervisor's agent registry — instance,
  family, callable, status, sandbox profile (if configured).
- **Pane 3 — Stats**: totals + counters per status +
  callable-agent fraction.

Keybindings:
- `q` / `esc` / `ctrl+c` — quit
- `r` — force refresh
- `tab` — cycle focused pane
- `↑` / `↓` / `j` / `k` — navigate inside focused pane

Use this WHEN the operator says "what are all these agents doing"
or wants live visibility into background dispatches. Pair with
`clawtool send --async --bidi <prompt>` to fan out work and watch
it land in real time.

Hard rule: don't try to dump tasks bodies into chat from
dashboard output — the dashboard renders metadata only by design,
matching `clawtool task watch`'s 80-char preview cap. For full
task bodies use `mcp__clawtool__TaskGet` or `clawtool task get
<id>`.
