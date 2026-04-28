---
description: One-screen status of the running clawtool system — daemon, sandbox-worker, and detected agents. Lighter than `clawtool doctor` (deep diagnostic) and not live like `clawtool dashboard` (Bubble Tea tick). Use this when you just want to know "is everything wired?".
allowed-tools: mcp__clawtool__Bash
---

The operator wants a quick "is everything wired?" answer without
reading the full doctor checklist or opening the dashboard. Run
`clawtool overview` — it returns a compact, single-screen status
of the daemon, sandbox-worker config + reachability, and the
agent registry.

```bash
clawtool overview
```

Output shape:

```
clawtool 0.21.6

daemon          ✓  pid 4895    at http://127.0.0.1:41517/mcp
sandbox-worker  ·  mode=off          (host execution; flip [sandbox_worker] mode to opt in)

agents:
  ✓  claude-code    Bash,Edit,Glob,Grep,Read,WebF…
  ✓  codex          mcp:clawtool (shared-http)
  ✓  gemini         mcp:clawtool (shared-http)
  ·  opencode       detected, NOT claimed (clawtool agents claim opencode)

(use 'clawtool doctor' for the full diagnostic, 'clawtool dashboard' for a live tick)
```

## When to use which surface

| Surface | When |
|---|---|
| `clawtool overview` | Quick check — "is daemon up? are hosts claimed?" |
| `clawtool doctor` | Deep diagnostic with fix hints per finding (config, daemon, sandbox-worker, agents, sources, recipes). Runs the upstream-release check too. |
| `clawtool dashboard` | Live Bubble Tea TUI, 1s tick, three panes. Use during a multi-agent dispatch. |

## Hard rules

- This is a read-only verb — never modifies state. Operator can
  re-run it freely.
- Stays compact: don't grow it past one terminal screen. Anything
  longer belongs in `doctor`.
