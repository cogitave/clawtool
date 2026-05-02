---
description: "Arm clawtool's self-trigger Stop-hook loop. Until /clawtool-autodev-stop or 200-trigger cap, Claude continues every turn instead of ending."
argument-hint: ""
allowed-tools: Bash(clawtool autodev:*)
---

Run this command to arm the autonomous loop:

```bash
clawtool autodev start
clawtool autodev status
```

**What this does**:

- Creates `~/.config/clawtool/autodev.enabled` (the arm-flag).
- Resets `~/.config/clawtool/autodev.counter` (fresh 200-trigger budget).
- The Stop hook wired by the marketplace plugin's `hooks/hooks.json` notices the flag on the next turn-end and emits `{"decision":"block","reason":"…"}` so Claude Code refuses to stop, continuing instead with the supplied autodev prompt as the next user input.

**Stop the loop**: `/clawtool-autodev-stop` (or `clawtool autodev stop`).

The 200-trigger cap is a safety belt for runaway loops; reset on every start. Reference: <https://docs.claude.com/en/docs/claude-code/hooks> — Stop event, decision="block".
