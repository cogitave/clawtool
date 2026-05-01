# clawtool autonomous

Self-paced single-message dev loop. The operator types ONE
prompt and clawtool keeps dispatching it back to a BIAM peer
until the agent emits `DONE: <summary>` (or writes a `tick.json`
with `done: true`), the max-iterations cap is hit, or the
operator hits Ctrl-C.

Two surfaces:

- **CLI**: `clawtool autonomous "<goal>"` (v0.22.71). The
  terminal entry point. Lives in `internal/cli/autonomous.go`.
- **MCP tool**: `AutonomousRun` (v0.22.72). The chat-driven
  entry point — any MCP-aware agent can drive the loop without
  the operator dropping to a shell. Lives in
  `internal/tools/setup/autonomous_run.go`.

Both surfaces share the same `tick.json` contract on disk so
they can interoperate (one host kicks off via MCP, another
inspects via `--watch`).

## CLI

```text
clawtool autonomous "<goal>" [--agent <instance>] [--max-iterations <N>]
                             [--cooldown <duration>] [--workdir <path>]
                             [--dry-run]
clawtool autonomous --resume <final.json> [flags]
clawtool autonomous --watch  <workdir>    [--watch-timeout <duration>]
```

Flags:

| Flag | Default | Notes |
| --- | --- | --- |
| `--agent <instance>` | `claude` | BIAM peer to dispatch to. |
| `--max-iterations <N>` | `10` | Hard cap on per-call iterations. |
| `--cooldown <dur>` | `5m` | Sleep between iterations. Tests pass `0s`. |
| `--workdir <path>` | cwd | tick / final files land under `<workdir>/.clawtool/autonomous/`. |
| `--dry-run` | off | Print planned prompt + flags and exit. |
| `--resume <final.json>` | — | Continue a prior run from its final.json (rehydrates goal + iter offset). Mutually exclusive with positional `<goal>` and with `--watch`. |
| `--watch <workdir>` | — | Tail-follow tick-*.json files written by an in-progress loop. Mutually exclusive with `<goal>` and `--resume`. |
| `--watch-timeout <dur>` | `5m` | Wall-clock cap on `--watch`. |

Onboarding gate: the loop refuses to start when `<workdir>` has
no `.clawtool/` directory. The operator sees:

```text
clawtool autonomous: "<workdir>" is not onboarded (no .clawtool/ directory)
  run `clawtool onboard` (or call OnboardStatus + InitApply via MCP) first.
```

## tick.json contract

Each iteration the peer is contracted to write
`<workdir>/.clawtool/autonomous/tick-<N>.json` with the shape:

```json
{
  "summary": "what you did this turn",
  "files_changed": ["path/to/edited.go", "..."],
  "next_steps": "what to tackle next",
  "done": false
}
```

`done: true` ends the loop. A missing tick file (the peer
returned without writing one) is treated as `{done: false,
summary: "(no tick file written)"}` so the loop keeps going to
the iteration cap rather than hard-erroring.

## final.json contract

When the loop terminates (DONE / max-iterations / SIGINT /
error), clawtool writes `<workdir>/.clawtool/autonomous/final.json`:

```json
{
  "goal": "<the operator's goal>",
  "agent": "claude",
  "max_iterations": 10,
  "iterations": 4,
  "stopped_reason": "done",
  "finished": true,
  "ticks": [{"summary": "...", "done": false}, ...],
  "finished_at": "2026-04-30T18:14:22Z"
}
```

`stopped_reason` is one of `done` / `max-iterations` /
`interrupted` / `error: <message>`. `iterations` counts the
cumulative iterations including any prior resumes (see below)
so the next `--resume` picks up at the correct offset. The
field is intentionally NOT named `iterations_run` — the resume
loader accepts both names for forward compat, but the canonical
key is `iterations`.

## Session prompt template

Each iteration the dispatcher hands the peer a verbatim prompt:

```
You are operating in clawtool autonomous mode.

Goal: <goal>

This is iteration N of M. Make incremental progress toward the
goal. When you have finished EVERYTHING, emit a single line of
the form "DONE: <one-line summary>" as your final message AND
write <workdir>/.clawtool/autonomous/tick-N.json with
{"summary": "...", "files_changed": [...], "next_steps": "",
"done": true}.

If you are NOT finished, write the same path with
{"summary": "...", "files_changed": [...],
"next_steps": "...", "done": false}.

Do not block on operator input. Do not ask clarifying questions —
make the most reasonable interpretation of the goal and proceed.
The loop will dispatch you again with the same goal + a fresh
iteration counter.
```

## --resume

Continue a prior run from its `final.json`:

```sh
clawtool autonomous --resume .clawtool/autonomous/final.json
```

The loader reads `goal` + `iterations` (or `iterations_run` —
both accepted) and dispatches a fresh loop starting at
`tick-(iterations+1).json`. Tick filenames stay monotonic across
resumes; `--max-iterations` is a per-call cap, not cumulative.

Validation rejects:

- `--resume` + positional `<goal>` (mutually exclusive)
- `--resume` + `--watch` (mutually exclusive)
- malformed `final.json` (missing `goal`, `iterations < 0`)

## --watch

Tail-follow a loop running in another process. Polls
`<workdir>/.clawtool/autonomous/` every 2 s (overridable in
tests via `watchPollInterval`); prints one human-friendly line
per new `tick-*.json`. Exits on:

- `final.json` appearing → exit 0 (chat-side callers can chain).
- `--watch-timeout` firing → exit 1 with a `timeout after Xm`
  stderr line.
- SIGINT / SIGTERM → exit 0.

The directory does not need to exist when watch starts — the
operator can invoke `--watch` BEFORE the autonomous run begins;
the poll silently retries until the directory shows up.

Sample output:

```text
$ clawtool autonomous --watch . --watch-timeout 30m
clawtool autonomous --watch: tailing /repo/.clawtool/autonomous (timeout 30m, poll 2s)
[iter 1] discovered failing test in foo_test.go — files: foo.go, foo_test.go
[iter 2] fix flushes the goroutine before close — files: foo.go
[iter 3] all tests green; cleaning up — files: foo.go
clawtool autonomous --watch: final.json detected, stopping
```

## MCP tool: AutonomousRun

The chat-driven mirror. Args:

| Arg | Default | Notes |
| --- | --- | --- |
| `goal` | (required) | The multi-step request. |
| `repo` | server cwd | Repo to drive the loop in. |
| `agent` | `claude` | BIAM peer to drive. |
| `max_iterations` | `10` | Hard cap. |
| `cooldown_seconds` | `300` | Cooldown between iterations. |
| `dry_run` | `false` | When true, return the planned dispatch sequence without dispatching. |
| `core_only` | `true` | Pass-through to InitApply if onboarding gap detected. |

Returns a JSON struct with `done`, `iterations_run`,
`files_changed`, `summary`, `final_json_path`, `ticks[]`, and on
failure `error_reason`. `final_json_path` mirrors the CLI verb's
final.json convention so a follow-up turn can read it back (or
ignore it; the inline `summary` is already in the response).

`AutonomousRun` does NOT auto-onboard. When the repo lacks
`.clawtool/` it returns an `error_reason` pointing at
`OnboardWizard` + `InitApply` — the calling agent owns that
choice. See `docs/mcp-authoring.md` for the four-tool ladder.

## Test seam

Both surfaces dispatch through a swappable
`AutonomousDispatcher` interface:

```go
type AutonomousDispatcher interface {
    Dispatch(ctx context.Context, prompt string) (AutonomousTick, error)
}
```

CLI: `cli.SetAutonomousDispatcher(stub)`. MCP: package-level
`defaultDispatcher` var. Tests pass `--cooldown=0s` (CLI) or
`cooldown_seconds=0` (MCP) to skip the 5-min sleep.

## Cross-references

- `docs/mcp-authoring.md` — `OnboardStatus` / `OnboardWizard` /
  `InitApply` / `AutonomousRun` tool ladder.
- `docs/bootstrap.md` — `clawtool bootstrap` is the zero-click
  cousin: spawns an agent, pipes a prompt that runs
  `OnboardWizard` + `InitApply`, hands off to `AutonomousRun`.
- `docs/rules.md` — `pre_send` / `pre_unattended` rules fire
  inside the dispatcher and can block iterations.
