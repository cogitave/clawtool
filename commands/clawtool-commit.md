---
description: Create a git commit through clawtool's Commit tool — Conventional Commits validation, hard Co-Authored-By block, pre_commit rules gate. Use this instead of running `git commit` from Bash.
allowed-tools: mcp__clawtool__Commit, mcp__clawtool__Bash, mcp__clawtool__RulesCheck
argument-hint: [<commit message>]
---

Drive a clawtool-validated commit. This is the path the operator
wants: never `Bash git commit -m "…"` when Commit is available.

**Step 1 — confirm intent.** Ask the user (or read from context)
what should land:
- The commit message (Conventional Commits required: `feat:`,
  `fix:`, `docs:`, `style:`, `refactor:`, `perf:`, `test:`,
  `build:`, `ci:`, `chore:`, `revert:` — optional `(scope)` and `!`
  for breaking changes)
- Which files (if not already staged)
- Whether to push after

**Step 2 — preflight (optional but recommended).** Run
`mcp__clawtool__RulesCheck` with `event="pre_commit"`, the proposed
`commit_message`, and `changed_paths` from `git diff --name-only`.
Surface any warnings to the user before proceeding; refuse to
proceed on a `block` severity unless the user explicitly overrides.

**Step 3 — call Commit.** Pass:
- `message` — the message body
- `files` — paths to stage (or `auto_stage_all=true` if intentional)
- `push=true` if the user asked to push
- Default `require_conventional=true` and `forbid_coauthor=true` —
  do NOT pass `forbid_coauthor=false` without an explicit user
  request; the operator's policy hard-blocks AI attribution.

**Step 4 — surface the result.** On success, paste the short SHA +
subject + branch + push status. On a rule or validation block, paste
the `rule_violations` list with `hint` text — the user should know
exactly which rule fired and how to satisfy it before retrying.

**Hard rules** (do not violate):
- Never append `Co-Authored-By: Claude` (or any AI attribution).
- Never run `git commit` directly via Bash when Commit is available.
- Never bypass `forbid_coauthor` without explicit user instruction.

Example invocation once the message and staged paths are settled:

```
mcp__clawtool__Commit{
  message: "feat(server): add commands lint",
  files: ["internal/server/commands_lint_test.go"],
  push: false
}
```
