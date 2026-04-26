<!-- managed-by: clawtool -->
# {{ project }}

Project-system-prompt for Claude Code. This file is loaded as
context whenever Claude Code opens this repository. Edit it freely
— re-running `clawtool recipe apply claude-md` refuses to overwrite
an unmanaged version (drop the `managed-by: clawtool` line above
to make this file fully your own).

Primary language: {{ language }}.

## Conventions

- Conventional Commits required for every commit (`feat:`, `fix:`,
  `docs:`, etc.). PR titles are validated against the same grammar.
- Tests pass before every push: see the test workflow in
  `.github/workflows/`.
- No `Co-Authored-By:` trailers on commits Claude writes for the
  user.
- Avoid premature abstraction. Three similar lines is better than
  a clever helper that abstracts the first two.

## Working in this repo

- Read existing patterns first; mimic them. Don't introduce a new
  style without a stated reason.
- For UI / frontend changes, exercise the feature in a browser
  before reporting "done." Type checks aren't behavior tests.
- Prefer editing a file over creating a new one. Avoid scattered
  helper modules unless the helper is reused at least twice.
- Don't add error handling for situations that can't happen.
  Trust internal code; validate at system boundaries (user input,
  external APIs).

## Tooling expectations

When `clawtool` is installed, use its `mcp__clawtool__*` tools for
shell, file, search, and web work in preference to Claude's native
equivalents. They're timeout-safe, format-aware, and produce
structured output.

`clawtool doctor` is the one-command diagnostic — run it whenever
"why isn't X working?" comes up.

## What this repo does

(Replace this section with a one-paragraph summary of the project.
The agent reads it as the first signal for any new task.)

## Non-goals

(List things this repo deliberately does NOT try to do, so the
agent doesn't propose them.)
