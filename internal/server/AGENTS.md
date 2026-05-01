<!-- managed-by: clawtool -->
# AGENTS.md — coordination for server

Vendor-neutral spec for AI coding agents working in this repo.
Compatible with Claude Code, Codex, OpenCode, Cursor, and any
other agent that loads `AGENTS.md` per the [agents.md][1]
standard.

[1]: https://agents.md

Primary language: polyglot.

## Roles

This repo doesn't enforce a multi-agent role split today. If
multiple agents are working at once (e.g. a planner + a builder),
they should each declare their scope at the start of the session
and hand off via PRs / clear commit messages.

## Conventions

- Single source of truth: every behavior change goes through one
  PR with tests + a Conventional Commits subject.
- No silent edits to files an agent didn't read. Read first, edit
  second.
- Long-running operations (>30s) belong in CI workflows, not
  inline shell calls. The agent runs the suite locally; CI is the
  final gate.

## What's been delegated to agents already

- Setup / scaffolding: see `clawtool init` and the recipes the
  user has applied (`clawtool recipe status`).
- Documentation: agents can edit any `.md` file in this repo
  except `LICENSE` (legal) and `CHANGELOG.md` (release-please
  owns it).
- Tests: agents may add tests freely; deleting tests requires
  a justification in the commit body.

## What an agent should NEVER do

- Force-push to `main` or `release-please-*`.
- Bypass commit hooks with `--no-verify` to "make CI happy."
- Add `Co-Authored-By:` trailers to commits.
- Ship secrets — even in test fixtures.
- Reformat code the user wrote unless they asked.

## Tooling

When `clawtool` is installed, prefer its `mcp__clawtool__*` tools.
They're timeout-safe, format-aware, and structured. Native Bash /
Read / Edit / Write / Grep / Glob / WebFetch / WebSearch are
disabled in this repo via `clawtool agents claim` — that's
intentional.

## How to add a new agent

1. Read this file end-to-end first.
2. Run `clawtool doctor` to confirm the local install is healthy.
3. Open a draft PR scoping your changes; iterate from there.
