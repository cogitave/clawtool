# clawtool fanout

Parallel-subgoal orchestrator. Spawn N subgoals in parallel —
each in its own git worktree under
`<workdir>/.clawtool/fanout/wt-N` on branch
`fanout/<run-id>/sub-N` — dispatch each to a BIAM peer as a
mini autonomous loop, then sequentially fast-forward-merge
each completed subgoal back into the main branch with a
cooldown between merges.

Host-agnostic alternative to Claude Code's built-in Agent
fan-out: any agent host (or bare terminal) can drive parallel
sub-processes through clawtool's own primitive.

> **Status:** the `fanout` verb + `Fanout` MCP tool live on the
> `autodev/fanout-primitive` branch and ship as v0.22.85 once
> merged. This page documents the merged surface so the doc
> lands together with the implementation.

Two surfaces:

- **CLI**: `clawtool fanout` — `internal/cli/fanout.go`.
- **MCP tool**: `Fanout` — `internal/tools/setup/fanout_run.go`.

Both share the same `summary.json` wire shape and reuse the
`AutonomousDispatcher` seam from `docs/autonomous.md` — every
subgoal IS an autonomous run, so a single test stub covers
both surfaces.

## CLI

```text
clawtool fanout "<sub-1> ;; <sub-2> ;; <sub-3>" [flags]
clawtool fanout --plan <plan.json>             [flags]
```

Plan source is mutually exclusive: pass either the positional
`;;`-separated string OR `--plan <plan.json>`. The JSON shape
is `{"subgoals": ["...", "...", ...]}`.

Flags:

| Flag | Default | Notes |
| --- | --- | --- |
| `--plan <path>` | — | Read subgoals from JSON. Mutually exclusive with the positional plan arg. |
| `--agent <instance>` | `claude` | BIAM peer to dispatch to. |
| `--max-concurrent <N>` | `4` | Cap on parallel in-flight subgoals. Hard cap: 8. |
| `--cooldown <dur>` | `5m` | Sleep between sequential ff-merges. Tests pass `0s`. |
| `--workdir <path>` | cwd | Repo root. Worktrees land under `<workdir>/.clawtool/fanout/wt-N`. |
| `--max-iterations-per-sub <N>` | `5` | Per-subgoal autonomous-loop cap. |
| `--dry-run` | off | Print parsed plan + worktree paths and exit without dispatching or merging. |

Onboarding gate: same as `autonomous` — `<workdir>` must have
a `.clawtool/` directory. Bare repos surface a structured
error pointing at `OnboardWizard` + `InitApply`.

## Worktree layout

Each subgoal lands in its own worktree under the run's tree:

```text
<workdir>/.clawtool/fanout/
├── <run-id>/
│   ├── wt-1/                 # worktree for subgoal 1
│   ├── wt-2/                 # worktree for subgoal 2
│   ├── wt-3/                 # worktree for subgoal 3
│   └── summary.json          # written when the run terminates
```

Branches: `fanout/<run-id>/sub-1`, `fanout/<run-id>/sub-2`, ...
Created via `git worktree add -b <branch> <path>` from the
current HEAD.

## Lifecycle

1. **Plan parse** — split the positional string on `;;` (or
   read the JSON file). Empty plans are rejected.
2. **Worktree setup** — `git worktree add -b
   fanout/<run-id>/sub-N <path>` for each subgoal. Failures
   abort the run before any dispatch fires.
3. **Parallel dispatch** — up to `--max-concurrent` subgoals
   run as concurrent autonomous loops. Each loop uses
   `--max-iterations-per-sub` as its iteration cap.
4. **Sequential merge** — as each subgoal completes, the
   orchestrator queues it for fast-forward merge back into the
   main branch with `--cooldown` between successive merges.
   Order matches completion order, NOT plan order.
5. **summary.json** — written when the run terminates (all
   merged / Ctrl-C / error).

`Ctrl-C` cancels in-flight subs, writes a partial summary, and
tears down unstarted worktrees.

## summary.json contract

```json
{
  "run_id": "<short id>",
  "goal": ["sub-1 text", "sub-2 text", "..."],
  "agent": "claude",
  "cooldown": "5m",
  "started_at": "2026-04-30T18:00:00Z",
  "finished_at": "2026-04-30T18:14:22Z",
  "subs": [
    {
      "index": 1,
      "subgoal": "...",
      "branch": "fanout/abc123/sub-1",
      "worktree_path": ".clawtool/fanout/abc123/wt-1",
      "status": "merged",
      "iterations": 3,
      "done": true,
      "files_changed": ["foo.go"]
    }
  ],
  "stopped": "ok"
}
```

`status` is one of: `merged` / `failed` / `timeout` / `pending`
/ `skipped` (CLI) or `ready` (MCP, when the tool returns
before merge for chat-side handoff). `stopped` is `ok` or
`interrupted`.

## MCP tool: Fanout

The chat-driven mirror. Args:

| Arg | Default | Notes |
| --- | --- | --- |
| `subgoals` | (required) | List of subgoal strings. |
| `repo` | server cwd | Repo to drive the run in. |
| `agent` | `claude` | BIAM peer to drive. |
| `max_concurrent` | `4` | In-flight cap. |
| `cooldown_seconds` | `300` | Cooldown between merges. |
| `max_iterations_per_sub` | `5` | Per-sub iteration cap. |
| `dry_run` | `false` | When true, return the parsed plan without dispatching. |

Returns the same `fanoutResult` shape as `summary.json`, with
`summary_path` pointing at the on-disk file for follow-up
reads. `Fanout` does NOT auto-onboard — same gate as
`AutonomousRun`.

## Test seams

Two package-level vars cover both surfaces:

- `cli.SetAutonomousDispatcher(stub)` — drives the per-sub
  dispatch.
- `cli.SetFanoutGitExec(stub)` — bypasses real `git worktree`
  / `git merge` calls.
- MCP-side equivalents: `defaultDispatcher` and
  `defaultGitExec` (package-level vars in
  `internal/tools/setup/`).

Tests pass `--cooldown=0s` (CLI) or `cooldown_seconds=0` (MCP)
to skip the merge cooldown.

## Cross-references

- `docs/autonomous.md` — every subgoal IS an autonomous run;
  the loop contract and tick.json shape apply per-sub.
- `docs/mcp-authoring.md` — `Fanout` is registered next to
  `AutonomousRun` in the manifest; same chat-driven onboarding
  ladder.
- `internal/cli/fanout.go` — CLI implementation.
- `internal/tools/setup/fanout_run.go` — MCP implementation.
