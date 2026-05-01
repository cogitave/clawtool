---
description: "Run clawtool's first-run onboarding wizard. Detects host CLIs, offers bridge installs, bootstraps the BIAM identity, records telemetry consent. --yes for non-interactive; --force to wipe progress."
argument-hint: "[--yes] [--force] [--no-defaults]"
allowed-tools: mcp__clawtool__Bash, mcp__clawtool__OnboardWizard, mcp__clawtool__OnboardStatus
---

Wraps `clawtool onboard`. Host-level setup, distinct from project
recipes (`clawtool init`). Onboard claims agent CLIs, generates
the BIAM identity, and starts the daemon; init writes
release-please / dependabot / etc. into a repo.

## When to use

- Brand-new clawtool install — the user just ran `npm i -g`
  / `brew install` / `go install` and needs the host wired up
  (daemon, bridges, identity, telemetry consent).
- Recovering after partial install — onboard saves progress;
  re-running resumes from the step you left off (Ctrl-C / closed
  terminal mid-flow).
- CI / Dockerfile bootstrapping — pass `--yes` for the smart-defaults
  path: install every missing bridge, claim every claimable host,
  start daemon, generate identity, init secrets stub.

## Example invocation

Interactive wizard (default):
```bash
clawtool onboard
```

Non-interactive smart-defaults — drives Dockerfile / e2e harness:
```bash
clawtool onboard --yes
```

Skip the auto-applied defaults but still walk the wizard; useful
for an operator who wants the prompts without preselected toggles:
```bash
clawtool onboard --no-defaults
```

Wipe saved progress + the onboarded marker, then start fresh:
```bash
clawtool onboard --force
```

## What happens

- Detects installed CLIs (claude, codex, opencode, gemini, hermes,
  aider) and offers to claim each one (claim = disable the agent's
  native Bash/Read/Edit/etc. so only the `mcp__clawtool__*`
  equivalents are exposed).
- Installs the canonical bridge for each non-Claude family (codex /
  opencode / gemini) by running their published Claude Code plugin
  or built-in subcommand. Clawtool never re-implements bridges.
- Generates the BIAM agent identity at `~/.config/clawtool/biam.toml`
  and writes a secrets stub at `~/.config/clawtool/secrets.toml`.
- Records anonymous-telemetry consent (allow-listed payload only;
  see `clawtool telemetry status` for the schema).
- Marks onboarding complete; subsequent invocations prompt before
  re-running unless `--force` is passed.

## Common pitfalls

- `clawtool onboard` is host-scoped, not repo-scoped. For repo
  setup (release-please, dependabot, AGENTS.md), run `clawtool init`
  separately.
- `--yes` claims every detectable CLI; on a shared machine, use the
  interactive form so you can decline claims for hosts other people
  rely on.
- The MCP-driven peer is `mcp__clawtool__OnboardWizard` (chat-driven)
  + `mcp__clawtool__OnboardStatus` (read-only check). Use those when
  driving onboard from inside an agent session.
