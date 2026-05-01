# rtk — setup playbook

Connects clawtool's pre_tool_use rewrite layer to [rtk-ai/rtk](https://github.com/rtk-ai/rtk),
a CLI proxy that compresses common Bash command output before
it hits the LLM context. Measured 60-90% token savings on
`git status`, `ls -R`, `grep`, and other line-oriented output —
one of the highest-leverage knobs for cutting context burn on
long agent loops.

## How rtk-rewrite fits into clawtool

The rewrite primitive lives in `internal/rules/rtk_rewrite.go`.
On a Bash dispatch, the helper:
1. Looks at the first whitespace-delimited token of the command.
2. Checks it against a project-local allowlist
   (`.clawtool/rtk-rewrite-list.toml`) — read-only / line-
   oriented commands only.
3. If the token is in the allowlist AND `rtk` is on PATH, it
   prepends `rtk ` to the command before dispatch.
4. If `rtk` is missing, the helper no-ops silently — so the
   recipe is safe to apply on a host without rtk yet.

The default allowlist covers the canonical safe set: `git`,
`ls`, `grep`, `cat`, `head`, `tail`, `find`, `tree`, `diff`,
`stat`, `wc`, `awk`, `sed`, `rg`, `jq`, `yq`, `echo`, `which`.

## Prerequisites

- A Rust toolchain (`rustc --version`) — rtk is `cargo install`-
  distributed. (Prebuilt binaries land in upstream releases for
  some platforms; check the upstream release page first.)
- A project where you've already run `clawtool init` — the
  rewrite rule needs the rules engine wired up.

## Step 1 — install rtk

```bash
cargo install rtk
rtk --version
```

`cargo install` puts the binary under `~/.cargo/bin/`; ensure
that's on PATH. The rewrite helper memoizes the PATH probe via
`sync.Once`, so the first dispatch after install pays the
lookup cost once and every subsequent dispatch reuses the cached
verdict.

## Step 2 — apply the recipe

```bash
clawtool recipe apply rtk-token-filter
```

This writes `.clawtool/rtk-rewrite-list.toml` with the canonical
allowlist. Open the file and add project-specific commands you
want compressed — anything tabular / line-oriented and read-only.
Keep write commands and interactive commands OUT of the list:
rtk buffers output, which breaks streaming and prompt-driven
flows.

## Step 3 — verify the rewrite is firing

The helper has no observable side effect when rtk is missing
(silent pass-through), so verifying needs rtk-on-PATH plus a
dispatched Bash call. Run any allowlisted command through the
agent surface — for example, ask the agent "show me `git
status` for this repo" and confirm rtk runs:

```bash
# Watch rtk activity in another terminal:
ps -ef | grep rtk
```

You should see `rtk git status` (not bare `git status`) when
the rewrite fires. If you don't:

```bash
which rtk            # confirm rtk is on PATH
rtk git status       # confirm rtk itself works
```

## Step 4 — measure savings

rtk prints a compression-ratio footer on each invocation:

```
[rtk] compressed 4823 → 612 bytes (87.3% saved)
```

Aggregate across a full agent loop to see end-to-end token
savings. Typical results on a long autodev cron run:
- `git status`: 80-95% saved.
- `ls -R`: 90%+ saved.
- `grep` / `rg` over a large corpus: 60-80% saved.

## Troubleshooting

- **rtk installed but rewrite isn't firing** — the rewrite
  helper memoizes the PATH probe per process. If clawtool was
  running before you installed rtk, restart clawtool so the
  next dispatch re-probes.
- **Wrong command rewritten** — confirm the first token of your
  command is in `.clawtool/rtk-rewrite-list.toml`. The rewrite
  is first-token-only (no flags, no paths).
- **Output looks corrupted** — rtk's compression is lossy on
  binary-ish output. Remove the offending command from the
  allowlist.
- **rtk crashes / hangs** — the rewrite helper has no fallback
  inside a single dispatch. Remove the command from the
  allowlist while you file an upstream bug.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/rtk/setup.md`.
**Auth flows covered**: none — rtk is a local CLI proxy with
no remote surface.
