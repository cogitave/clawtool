---
description: "Disarm clawtool's self-trigger Stop-hook loop. Next Stop event lets the turn end normally and operator regains control."
argument-hint: ""
allowed-tools: Bash(clawtool autodev:*)
---

Run this command to disarm the autonomous loop:

```bash
clawtool autodev stop
clawtool autodev status
```

**What this does**:

- Deletes `~/.config/clawtool/autodev.enabled` (the arm-flag).
- Deletes `~/.config/clawtool/autodev.counter` (so a future `start` gets a fresh budget).
- The Stop hook wired by the marketplace plugin's `hooks/hooks.json` notices the missing flag on the next turn-end and exits 0 silently — Claude Code closes the turn normally.

**Re-arm later**: `/clawtool-autodev-start` (or `clawtool autodev start`). Reference: <https://docs.claude.com/en/docs/claude-code/hooks> — Stop event, decision="block".
