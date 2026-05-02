# Ideator → Autopilot → Autonomous

Three-layer self-direction stack. Each layer answers one question:

| Layer | Question | Verb | Storage |
|---|---|---|---|
| **Ideator** | *What* should we work on? | `clawtool ideate` | None — read-only signal probes |
| **Autopilot** | *When* should we work on it? | `clawtool autopilot` | `~/.config/clawtool/autopilot/queue.toml` |
| **Autonomous** | *How* should we do it? | `clawtool autonomous "<goal>"` | `--workdir`-rooted tick stream |

The handoff is operator-gated at the top: Ideator emits proposals,
the operator (or chat agent) flips a proposal to `pending`, the
Autopilot serves it to the Autonomous loop. No layer escalates
itself past the gate; the autopilot queue's `proposed → pending`
transition is the explicit consent boundary.

---

## Ideator — `clawtool ideate`

Surveys cheap, repo-local signals and prints ranked Idea candidates.
Read-only by default. With `--apply`, every Idea is pushed onto the
autopilot backlog at status=`proposed` (operator runs
`clawtool autopilot accept <id>` to flip proposed → pending).

```sh
clawtool ideate --top 15
clawtool ideate --apply --top 15           # discover + queue
clawtool ideate --source vuln_advisories   # one source only
clawtool ideate --baseline-set             # seed bench_regression baseline
```

### Sources

| Name | Surfaces | Reference |
|---|---|---|
| `adr_questions` | `## Open questions` blocks in `wiki/decisions/*.md` that are >7 days unresolved. | [adr_questions.go](../internal/ideator/sources/adr_questions.go) |
| `adr_drafting` | ADRs in `status: drafting` >30 days old. | [adr_drafting.go](../internal/ideator/sources/adr_drafting.go) |
| `todos` | `TODO`/`FIXME`/`XXX` comments in `*.go` files (skips `(ideator-skip)`/`TODO[skip]:`/`TODO(template):`). | [todos.go](../internal/ideator/sources/todos.go) |
| `ci_failures` | Recent failed GitHub Actions runs (`gh run list`). Drops failures whose head sha is unreachable from HEAD (force-pushed) or superseded by a later green run on the same workflow. | [ci_failures.go](../internal/ideator/sources/ci_failures.go) |
| `manifest_drift` | Diffs canonical `mcp.WithDescription` strings against the registered manifest's bleve-indexed copies; flags drift before the next ToolSearch hit-rate regression. | [manifest_drift.go](../internal/ideator/sources/manifest_drift.go) |
| `bench_regression` | ToolSearch BM25 rank-1 hit-rate baseline diff (>5pp drop). | [bench_regression.go](../internal/ideator/sources/bench_regression.go) |
| `deps_outdated` | Outdated direct Go module dependencies (`go list -m -u -json all`); indirect deps filtered out. | [deps_outdated.go](../internal/ideator/sources/deps_outdated.go) |
| `deadcode_hits` | Unreachable functions reported by `deadcode -test ./...`. | [deadcode_hits.go](../internal/ideator/sources/deadcode_hits.go) |
| `vuln_advisories` | Go security advisories (`govulncheck -json ./...`). Drops stdlib findings already covered by the workflow `GO_VERSION` pin. Cached on go.sum hash for 12h. | [vuln_advisories.go](../internal/ideator/sources/vuln_advisories.go) |
| `stale_files` | Tracked `.go` files whose newest commit is >90 days old. Heuristic fallback so the loop never goes dry. | [stale_files.go](../internal/ideator/sources/stale_files.go) |
| `pr_review_pending` | Open GitHub PRs awaiting review for >24h (`gh pr list --search "review:none"`). Age-banded priority: 4 (<3d), 5 (3–7d), 6 (>7d). | [pr_review_pending.go](../internal/ideator/sources/pr_review_pending.go) |

Every source is **cheap-on-fail**: a missing CLI (`gh`, `govulncheck`,
`deadcode`), unreadable file, or non-repo cwd quietly returns zero
ideas instead of erroring. The orchestrator never panics on a quiet
caller.

### Concurrency

Sources run in parallel under a bounded semaphore
(`Options.MaxConcurrency`, default `4`). On slow filesystems
(WSL2 `/mnt/c`, sshfs, NFS) unbounded fanout saturates the FS bridge
and inflates wall time 2–5×; cap to 1 with the env override:

```sh
CLAWTOOL_IDEATOR_MAX_CONCURRENCY=1 clawtool ideate --top 15
```

### Performance

- **vuln_advisories** caches govulncheck output keyed on
  `bin=<resolved-path> gosum=<sha1>` with 12h TTL at
  `$TMPDIR/clawtool-govulncheck-cache.json`. Cache hit: ~0.18s
  vs. 17s cold. Bumping a dep (go.sum changes) or upgrading
  govulncheck (binary path resolves elsewhere) busts the cache.
- **bench_regression** reads a one-line baseline JSON; trivial.
- **ci_failures** runs at most three `gh run` calls per cron tick
  thanks to per-workflow caching of the latest-green probe.

---

## Autopilot — `clawtool autopilot`

TOML-backed queue at `~/.config/clawtool/autopilot/queue.toml`.
Three terminal statuses (`done`, `skipped`) plus three working
ones (`proposed`, `pending`, `in_progress`):

```
proposed   ──accept──▶  pending   ──next──▶  in_progress  ──done──▶  done
                                                                  └─skip──▶  skipped
```

| Verb | Effect |
|---|---|
| `add "<prompt>"` | Append a `pending` item directly (no Ideator gate). |
| `accept <id>` | Flip `proposed` → `pending`. The operator-gate. |
| `next` | Atomic claim of the highest-priority `pending` item; marks `in_progress`, prints the prompt. |
| `done <id>` / `skip <id>` | Terminal flips with optional `--note`. |
| `list [--status ...] [--format text\|json]` | Inspect. |
| `status` | Histogram. |

Default agent loop:

```sh
while true; do
  prompt=$(clawtool autopilot next --format text)
  [ -z "$prompt" ] && break
  clawtool autonomous "$prompt"
done
```

The `accept` step is non-negotiable. Ideator-emitted `proposed`
items are not picked up by `next`; only manually-vetted `pending`
items are. This is the safety boundary that keeps the agent from
silently driving its own pipeline past human review.

---

## Autonomous — `clawtool autonomous "<goal>"`

Self-paced single-message dev loop. The agent emits `tick-N.json`
after each iteration; the loop ends on `done: true`,
`--max-iterations`, or SIGINT.

| Flag | Effect |
|---|---|
| `--workdir <path>` | Where ticks land. Defaults to a tempdir. |
| `--max-iterations N` | Hard cap. |
| `--resume <final.json>` | Continue a prior run. |
| `--watch <workdir>` | Tail an existing run's tick stream into the terminal. |
| `--unattended` | Inject the host's elevation flag (Claude Code `--dangerously-skip-permissions`, etc.). |
| `--instance <name>` | Pick a named BIAM peer instead of the default. |

See [docs/autonomous.md](autonomous.md) for the full envelope shape.

---

## MCP equivalents

The same three layers are surfaced as MCP tools so chat agents drive
the loop without context-switching to the CLI:

| MCP tool | CLI equivalent |
|---|---|
| `IdeateRun` | `clawtool ideate` |
| `IdeateApply` | `clawtool ideate --apply` |
| `AutopilotStatus` | `clawtool autopilot status` |
| `AutopilotList` | `clawtool autopilot list` |
| `AutopilotAdd` | `clawtool autopilot add` |
| `AutopilotAccept` | `clawtool autopilot accept` |
| `AutopilotNext` | `clawtool autopilot next` |
| `AutopilotDone` / `AutopilotSkip` | `clawtool autopilot done`/`skip` |
| `AutonomousRun` | `clawtool autonomous` |

---

## Adding a new Ideator source

1. Implement `IdeaSource` (see [internal/ideator/source.go](../internal/ideator/source.go)):

   ```go
   type IdeaSource interface {
       Name() string
       Scan(ctx context.Context, repoRoot string) ([]Idea, error)
   }
   ```

2. Honour the cheap-on-fail contract: missing CLI / network / config
   returns `(nil, nil)`, never an error.

3. Set `Idea.SuggestedPriority` thoughtfully. Convention so far:

   | Priority | Class |
   |---|---|
   | 8 | Stdlib security advisory |
   | 7 | CI failure |
   | 6 | Third-party module advisory |
   | 5 | ADR open question |
   | 4 | Outdated dependency |
   | 3 | Deadcode hit |
   | 2 | Heuristic fallback (stale_files) |
   | 1 | Bench regression |

4. Set `Idea.DedupeKey` so re-running ideate after operator inaction
   is idempotent. The autopilot queue rejects new proposals whose
   key already lives on a non-terminal row.

5. Wire it into `defaultIdeatorSources()` in
   [internal/cli/ideate.go](../internal/cli/ideate.go).

6. Add a row to the table at the top of this doc.
