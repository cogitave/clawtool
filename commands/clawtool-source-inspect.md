---
description: Audit a configured source's exposed tool surface. Spawns the npm-published MCP Inspector against the source's stdio command and reports the tools it advertises. Read-only; doesn't touch local config or secrets.
argument-hint: <instance> [--dry-run] [--format text|json]
allowed-tools: mcp__clawtool__Bash
---

Wraps `clawtool source inspect`. Trust-but-verify: see exactly
which tools a configured source server actually advertises, so a
new source addition doesn't silently expose more (or less) than
its catalog entry promises.

## When to use

- After `clawtool source add <name>` — sanity-check what the npm
  package actually ships before an agent session starts addressing
  `<instance>__<tool>` calls.
- When a source bumps versions — re-inspect to catch tool surface
  drift between releases (new tools, removed tools, renamed args).
- Debugging "tool not found" errors during dispatch — confirm the
  tool you're addressing is actually advertised by the running
  server.
- Pipelines / dashboards — `--format json` passes the inspector's
  raw output through for downstream tooling.

## Example invocation

Inspect a configured instance using the default text format:
```bash
clawtool source inspect github-personal
```

Preview the npx invocation without spawning the inspector — useful
when the inspector itself is the part you're debugging:
```bash
clawtool source inspect github-personal --dry-run
```

Machine-readable output for pipelines:
```bash
clawtool source inspect postgres --format json | jq '.tools[].name'
```

## What happens

- Resolves `<instance>` against the local source config and reads
  its stdio command + args.
- Spawns the npm-published `@modelcontextprotocol/inspector` against
  that command via `npx`. The inspector lists tools, resources,
  and prompts the server advertises.
- Prints the tool surface in the chosen format. Text output is
  one tool per line with name + one-line description; JSON output
  passes the inspector's raw response through.

## Common pitfalls

- The instance must already be configured (`clawtool source list`
  to confirm). Inspect doesn't accept a bare catalog name — it
  checks the live config so you're auditing the actual installed
  command.
- The inspector spawns the source server in a real subprocess; if
  the server needs secrets that aren't set, it'll fail to start
  and the inspector will hang or error. Run `clawtool source check`
  first if `inspect` errors mysteriously.
- `--dry-run` previews the npx command but doesn't validate the
  source instance's secrets readiness. Use `clawtool source check`
  for that orthogonal concern.
