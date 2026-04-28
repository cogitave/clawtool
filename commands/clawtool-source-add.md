---
description: Add a source server from the built-in catalog. Bare-name install — no need to remember npx packages.
allowed-tools: Bash
argument-hint: <source-name> [--as <instance>]
---

Wraps `clawtool source add`. The user passes a bare name (e.g.
`github`, `slack`, `postgres`); clawtool resolves it against its
embedded catalog and writes the source config. The catalog covers
github, slack, postgres, sqlite, filesystem, fetch, brave-search,
google-maps, memory, sequentialthinking, time, and git out of the box.

```bash
clawtool source add $ARGUMENTS
```

After running, summarize:

- Which source got added and which package powers it.
- Any required env vars the user still needs to set, with the exact
  `clawtool source set-secret <instance> <KEY> --value <value>` command.
- A pointer to `clawtool source check` so the user can verify auth
  readiness before opening a fresh Claude Code session.

If the user already has an instance with the bare name and adds the
same source again, clawtool errors with an `--as <other-name>`
suggestion. Multi-instance is intentional (two GitHub accounts,
two Slack workspaces, etc.); just use `--as <name>`.
