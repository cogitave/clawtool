---
description: "Apply project-setup recipes to the current repo via the clawtool init wizard. Interactive by default; --all for non-interactive Stable defaults; --summary-json for machine-readable output."
argument-hint: "[--all] [--summary-json] [--yes]"
allowed-tools: mcp__clawtool__Bash, mcp__clawtool__InitApply
---

Wraps `clawtool init`. Inject project-setup recipes (governance,
commits, release, ci, quality, supply-chain, knowledge, agents,
runtime) into the current repo.

## When to use

- Bootstrapping a fresh repo — the operator wants release-please,
  dependabot, conventional-commits CI, AGENTS.md, devcontainer, etc.
  in one pass without hunting through individual `recipe apply`
  commands.
- Re-running on an existing repo — recipes are idempotent; rerunning
  patches up new ones the team has shipped since.
- Driving the same wizard from CI / Dockerfile — pass `--all` (or
  the alias `--yes`) to apply Stable-tier defaults non-interactively.

## Example invocation

Interactive picker (TTY required):
```bash
clawtool init
```

Non-interactive — apply every Stable-tier recipe whose prereqs are
satisfied:
```bash
clawtool init --all
```

Machine-readable summary — same as `--all` but suppresses the human
banner and emits one JSON object per recipe to stdout (one per line),
suitable for CI dashboards or downstream tooling:
```bash
clawtool init --all --summary-json
```

The MCP equivalent for chat-driven flows is `mcp__clawtool__InitApply`
— same engine, structured input, no TTY.

## What happens

- Each recipe declares prerequisites (binary on PATH, file presence,
  etc.). Unmet prereqs cause a `↷ skipped <recipe>` line; the recipe
  is otherwise a no-op.
- Already-applied recipes are detected and re-run idempotently —
  config files get patched, not overwritten, when the recipe knows
  how to merge.
- Output schema (one line per recipe): `✓ applied <name>` /
  `↷ skipped <name> — <reason>` / `✗ failed <name> — <error>`.

## Common pitfalls

- Running `clawtool init` (no `--all`) over SSH / non-TTY hangs
  waiting for the picker. Use `--all` or `--yes` whenever stdin
  isn't a terminal.
- `--summary-json` implies `--all`; the JSON line stream replaces
  the banner. Don't pass `--summary-json` without expecting the
  full default set to apply.
- For a single recipe, prefer `clawtool recipe apply <name>` — `init`
  is the bulk path.
