---
name: clawtool-autonomous-loop
description: "Skill for agents dispatched into clawtool's autonomous loop. Documents the tick.json contract: what to write, when to set done=true, what next_steps should describe."
---

# clawtool autonomous loop

You have been dispatched by `clawtool autonomous` (CLI verb) or the
`AutonomousRun` MCP tool. The loop driver re-dispatches the same goal
to you on each iteration; coordination between iterations happens
exclusively through `<workdir>/.clawtool/autonomous/tick-N.json`. If
you skip that file, the next iteration cannot tell what changed and
the loop runs blind until `--max-iterations`.

## When this skill applies

The session prompt opens with `You are operating in clawtool autonomous
mode.` and includes:

- a one-line `Goal:`
- `This is iteration N of M.` metadata
- the absolute path of the tick file you are expected to write

If those markers are absent, this skill does not apply — you are in a
normal interactive session.

## Per-iteration contract

You MUST write the tick file. Schema, exact field semantics:

```json
{
  "summary": "one-line, ≤200 chars, describes what THIS iteration did",
  "files_changed": ["repo/relative/path.go", "internal/foo/bar.go"],
  "next_steps": "specific actionable for the next iteration; empty when done=true",
  "done": false
}
```

- **summary** — past-tense, single line, ≤200 chars. Concrete: "added
  Tick struct + readTick helper + 3 unit tests" beats "made progress
  on the loop". The loop driver echoes this to the operator's stdout
  on every tick, so vague summaries waste a real human's attention.
- **files_changed** — repo-relative paths only. No absolute paths, no
  `./` prefix, no globs. Empty list is legal for read-only research
  ticks (rare; usually a sign you should have set `done: false` and
  written code).
- **next_steps** — one or two sentences naming a specific next action,
  not a vague theme. "Wire `Tick.Done` into the loop's break
  condition + cover with TestStopsOnDone" beats "continue work on the
  loop". Empty string when `done: true`.
- **done** — `true` only when the goal is fully met. Tests pass, code
  compiles, the operator's stated outcome is observable on disk. If
  any of those is false, `done` is false. Setting `done: true`
  prematurely is the worst failure mode of this loop because the
  operator walks away expecting a finished result.

## Termination signal

When the goal is fully met:

1. Write the tick file with `"done": true`, an accurate `summary`, the
   final `files_changed` list, and `next_steps: ""`.
2. Emit a single final assistant message of the form:
   `DONE: <one-line summary>`
   (same content as the tick `summary` is fine.)

The loop driver checks the tick file first; the `DONE:` line is a
human-readable echo for the operator's terminal. Both should agree.

## Anti-patterns

- **Do not ask for clarification.** The session prompt explicitly tells
  you to make the most reasonable interpretation and proceed. The
  operator is not at the keyboard.
- **Do not claim DONE in prose while writing `done: false`.** The loop
  trusts the JSON, not the message body. A mismatch means the loop
  keeps dispatching while the operator believes you finished.
- **Do not skip writing tick.json.** A missing file is treated as a
  no-signal in-progress tick (`done: false`, summary `(no tick file
  written)`). The loop will dispatch you again with the same goal and
  no memory of what you did. Always write the file, even on a
  partial-progress turn.
- **Do not stuff multi-line prose into `summary`.** The driver renders
  it on a single output line; long summaries get truncated. Use
  `next_steps` for detail.
- **Do not write absolute paths in `files_changed`.** They break
  cross-machine reproduction of `final.json`.

## Recovery

If a tick fails partway — a test broke, a refactor stalled, a tool
errored — DO NOT bail silently. Write the tick with:

- `done: false`
- `summary` describing what you attempted and where it stopped
- `files_changed` listing whatever DID land on disk (even if reverted
  later, the operator wants the audit trail)
- `next_steps` naming the specific recovery action: "revert
  internal/foo/bar.go and retry with the smaller refactor", or
  "TestX is red because of Y; fix Y first then re-run"

The next iteration receives the same goal. A clean `next_steps` lets
it pick up without re-deriving the state from scratch.
