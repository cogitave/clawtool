---
description: Zero-click setup. Spawn a BIAM peer's CLI with its elevation flag, pipe a prompt that drives OnboardWizard + InitApply via MCP, and stream the agent's reply back. One verb, fresh repo to wired host + onboarded project.
argument-hint: [--agent <claude|codex|gemini|opencode|hermes|aider>] [--workdir <path>] [--dry-run]
allowed-tools: mcp__clawtool__Bash
---

Wraps `clawtool bootstrap`. The hands-off cousin of running
`clawtool onboard` then `clawtool init` separately — bootstrap
spawns the chosen agent's CLI, hands it a prompt that asks it to
run both via MCP, and streams the agent's stdout back to you.

## When to use

- Fresh-laptop setup — clawtool is installed, no host claimed, no
  repo initialized; one command should leave you with both done.
- Onboarding a new contributor — they run one verb in the cloned
  repo and the agent walks them through the rest with live output.
- CI smoke test — `--dry-run` prints the planned spawn argv +
  bootstrap prompt without executing, so you can assert against it.

## Example invocation

Default (Claude as the bootstrap driver, current dir as workdir):
```bash
clawtool bootstrap
```

Different driver — useful when Claude Code isn't installed yet but
Codex or Gemini is:
```bash
clawtool bootstrap --agent codex
```

Different workdir — the spawned agent will `cd` here before running:
```bash
clawtool bootstrap --workdir /path/to/fresh/repo
```

Print the planned spawn + prompt and exit (no agent invocation):
```bash
clawtool bootstrap --dry-run
```

## What happens

- Spawns the chosen peer's CLI with its elevation flag (e.g.
  `claude --dangerously-skip-permissions` for Claude Code) so the
  agent can run setup tools without a per-call consent prompt.
- Pipes a bootstrap prompt as the agent's first user message. The
  prompt asks the agent to run `mcp__clawtool__OnboardWizard` and
  `mcp__clawtool__InitApply`, read `mcp__clawtool__AutonomousRun`'s
  UsageHint, then print a 3-line summary.
- Streams the agent's stdout verbatim. The verb exits when the
  agent emits `DONE` or returns control.

## Common pitfalls

- The chosen `--agent` family must already have its CLI installed.
  `clawtool bootstrap --agent codex` errors loud if `codex` isn't
  on PATH; install it first or pick a different family.
- This is an elevation-flag flow — the spawned agent runs setup
  without per-call consent. Only invoke when the operator explicitly
  wants hands-off setup; otherwise prefer the interactive
  `clawtool onboard` + `clawtool init`.
- `--dry-run` is the safe default when wiring this into automation
  for the first time; verify the rendered prompt matches expectations
  before flipping it off.
