---
description: "Self-paced single-message dev loop. Dispatches a goal to a BIAM peer until DONE, max-iterations, or SIGINT. Resume + watch modes for long runs."
argument-hint: "<goal> [flags] | --resume <final.json> | --watch <workdir>"
allowed-tools: mcp__clawtool__Bash, mcp__clawtool__AutonomousRun
---

Wraps `clawtool autonomous`. The CLI builds a session prompt from
the goal + iteration metadata, dispatches it to the chosen BIAM
peer, and waits for the agent to write `tick-N.json` per
iteration until it sets `done: true`.

## When to use

- Long-running goal — "land all the failing tests under
  `internal/cli/`" — that benefits from the agent self-pacing
  across many cooldown-separated iterations rather than one
  marathon dispatch.
- Unattended overnight runs — the operator sets `--max-iterations`
  high and a generous `--cooldown` and lets it grind.
- Resume after interruption — the prior run's `final.json` carries
  the goal + history; pass it to `--resume` to continue.
- External monitoring — pair `--watch <workdir>` with the Monitor
  tool to surface ticks as inline events.

## Example invocation

Single-shot, default agent (claude), default 10 iterations,
default 5m cooldown:
```bash
clawtool autonomous "land all failing tests under internal/cli/"
```

Specify peer + cap iterations + tighten the cooldown for a fast
feedback loop:
```bash
clawtool autonomous "refactor BIAM dispatcher to remove dead paths" \
  --agent codex --max-iterations 4 --cooldown 30s
```

Tests pass `--cooldown 0s` to dispatch back-to-back without sleep.

Dry-run — print the rendered prompt + flags and exit without
dispatching:
```bash
clawtool autonomous "<goal>" --dry-run
```

Resume a prior run from its `final.json` (mutually exclusive with
positional `<goal>`):
```bash
clawtool autonomous --resume .clawtool/autonomous/final.json
```

Watch an in-progress loop and print one human-friendly line per
new tick:
```bash
clawtool autonomous --watch . --watch-timeout 30m
```

## What happens

- Each iteration the agent is expected to write
  `<workdir>/.clawtool/autonomous/tick-N.json` with `{summary,
  files_changed, next_steps, done}`.
- The loop ends when `done == true`, `--max-iterations` is hit,
  or the operator sends SIGINT.
- A terminal `final.json` records the goal, iteration count, and
  full tick history — that's what `--resume` reads.

## Common pitfalls

- The agent driving the loop MUST follow the tick contract. The
  recipe `clawtool-autonomous-loop` plants a SKILL.md teaching it;
  apply that recipe before you run autonomous against a fresh repo.
- Default cooldown is 5m to let CI breathe between commits in
  autodev mode. Don't shorten without confirming CI keeps up.
- `--resume` and `--watch` are mutually exclusive with `<goal>` and
  with each other — pick one mode per invocation.
