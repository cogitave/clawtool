# clawtool autodev — Stop-hook self-trigger loop

`autodev` keeps Claude Code working continuously across turns
without operator re-prompting. When armed, every Claude Code Stop
event in a clawtool-bound session emits
`{"decision":"block","reason":"<next prompt>"}` so the conversation
refuses to end and continues with a fresh autodev prompt instead.
The only path back to operator control is
`/clawtool-autodev-stop` (or `clawtool autodev stop`).

The mechanism leans entirely on Claude Code's existing
[Stop hook contract](https://docs.claude.com/en/docs/claude-code/hooks):
when a hook script returns `decision: "block"` plus a `reason`
string, Claude treats the supplied reason as the next user-visible
prompt and keeps the turn alive.

## How it works

1. The marketplace plugin's [`hooks/hooks.json`](../hooks/hooks.json)
   already binds the Stop event when the operator installs
   `clawtool@clawtool-marketplace`. The Stop entry runs two
   commands in order: `clawtool peer heartbeat --status online`
   (existing peer-registry housekeeping) and `clawtool autodev hook`
   (the autodev continuation).
2. `clawtool autodev hook` checks for the arm-flag at
   `~/.config/clawtool/autodev.enabled`.
   - **Flag absent** — exits 0 with empty stdout. Claude stops
     normally. Default state.
   - **Flag present, counter < 200** — increments the counter,
     emits `{"decision":"block","reason":"<auto prompt>"}` on
     stdout. Claude continues.
   - **Flag present, counter ≥ 200** — emits a different reason
     telling the operator to acknowledge the cap and re-arm. The
     200-trigger budget is the runaway safety belt.
3. The auto-generated prompt re-probes repo state at hook fire
   time (`git describe --tags`, `clawtool autopilot status`) so the
   prompt the model reads is always fresh, not a stale snapshot
   from when the loop started.

There is **no separate install step**. The Stop hook is already
wired via `hooks/hooks.json` for every clawtool-marketplace user;
the operator just toggles the flag.

## Subcommands

| Verb | Purpose |
|---|---|
| `clawtool autodev start` | Arm the loop. Creates the flag, resets the counter to 0. |
| `clawtool autodev stop` | Disarm the loop. Deletes the flag and counter. |
| `clawtool autodev status` | Show armed/disarmed state + counter value. Exits 1 when disarmed so shell scripts can branch. |
| `clawtool autodev hook` | The Stop-hook entry-point itself. Not for direct operator use — `hooks/hooks.json` calls it. Stdout-clean when flag absent, JSON envelope when armed. |

Slash-command equivalents in the marketplace plugin:

| Slash command | Equivalent |
|---|---|
| `/clawtool-autodev-start` | `clawtool autodev start` |
| `/clawtool-autodev-stop` | `clawtool autodev stop` |

## Files

| Path | Purpose |
|---|---|
| `~/.config/clawtool/autodev.enabled` | The arm-flag. Empty file. Existence = armed. |
| `~/.config/clawtool/autodev.counter` | Self-trigger counter (plain integer). Reset by `start`, deleted by `stop`. |

## Why it's flag-driven, not config-driven

The autodev loop is a deliberately-disposable runtime mode, not a
setting that survives across reboots. The flag-based design has
three consequences worth naming:

1. **Recoverable from a runaway**: the operator can `rm
   ~/.config/clawtool/autodev.enabled` from any other terminal
   and the next Stop hook silently disarms. No config-file edit,
   no daemon restart.
2. **Per-host scope**: each machine arms / disarms independently.
   A loop running on the operator's laptop doesn't leak into a
   remote SSH session that also has clawtool installed.
3. **Counter discipline**: the cap is intentionally low enough
   (200) that a buggy loop runs out of fuel before the operator's
   API bill does. The cap resets on every `start`; the operator
   chooses to re-arm.

## Choosing a prompt template

The default prompt is biased toward this repo's autonomous-loop
discipline (check tag-watcher logs, sweep ideate, dispatch agents,
ship architectural improvements when idle). For other repos, set
`CLAWTOOL_AUTODEV_REPO=/path/to/repo` so the prompt's `git
describe` and `clawtool autopilot status` probes resolve against
the right working tree. A future flag (`--prompt-template`) will
let operators ship their own loop prompts; for now the prompt is
hard-coded into the binary.
