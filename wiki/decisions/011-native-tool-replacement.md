---
type: decision
title: "011 Native tool replacement via opt-in disabling"
aliases:
  - "011 Native tool replacement via opt-in disabling"
  - "ADR-011"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - integration
  - claude-code
status: developing
related:
  - "[[005 Positioning replace native agent tools]]"
  - "[[010 Plugin packaging and auto-preference]]"
sources: []
---

# 011 — Native Tool Replacement via Opt-In Disabling

> **Status: developing.** Bridges ADR-005 (positioning: clawtool is the
> canonical layer) and ADR-010 (plugin packaging: soft preference). This
> ADR adds the **hard-replacement** layer the user explicitly asked for.

## Context

ADR-010 ships a Claude Code plugin whose `skills/clawtool/SKILL.md` biases
agent tool selection toward `mcp__clawtool__*`. That bias is **soft** —
when both `Bash` (native) and `mcp__clawtool__Bash` are visible, the
agent's tool-selection heuristic picks one or the other based on
description match for the current task. Sometimes it picks native.

The user pushed back on that residual ambiguity:

> *"Yani claude'a dahil edildiğinde native bash kullanmasını vs. nasıl
> önleriz."*
>
> When clawtool is installed in Claude, how do we prevent it from using
> native Bash etc.

The answer is to **make the native ones invisible to the agent** —
not by removing them from Claude Code's binary (we can't), but by
adding their names to `~/.claude/settings.json`'s `disabledTools` array.
Once disabled, Claude Code does not advertise those tools in
`tools/list` to the model, so the model literally cannot call them
even if it wanted to. `mcp__clawtool__Bash` becomes the only `Bash` the
model sees.

## Decision

clawtool ships a new CLI subcommand `clawtool agents claim <agent>` that
mutates the agent's settings file. Reversal is `clawtool agents release
<agent>`.

```bash
# Disable native Bash/Read/Edit/Write/Grep/Glob/WebFetch/WebSearch in
# Claude Code so only mcp__clawtool__* equivalents are exposed.
clawtool agents claim claude-code

# Reverse it.
clawtool agents release claude-code

# See current state.
clawtool agents status
```

### What gets disabled

When claiming `claude-code`, exactly these eight names are added to
`~/.claude/settings.json`'s `disabledTools` array:

```
Bash Read Edit Write Grep Glob WebFetch WebSearch
```

These are the native tools that have direct `mcp__clawtool__*`
equivalents in clawtool's canonical core list (per ADR-005, finalized
v0.8.0). One-to-one mapping; nothing surprising.

### What does NOT get disabled

We **explicitly leave alone**:

- `Task`, `Agent`, `ExitPlanMode` — subagent dispatch / planning. No
  clawtool counterpart yet; we don't make the agent unable to delegate.
- `TodoWrite`, `TaskCreate`, `TaskList`, `TaskGet`, `TaskUpdate`,
  `TaskOutput`, `TaskStop` — task tracking. No counterpart, and these
  are core to Claude Code's UX.
- `NotebookEdit` — Jupyter-specific edit. We have `Edit` for code and
  `Read` for ipynb but no notebook-cell-mutate equivalent.
- `WebSearch` — wait, we DO disable this. (Listed above.)
- `WebFetch` — likewise disabled.
- Anything else built-in (skills, slash commands, MCP servers, plugins)
  that clawtool doesn't replace.

Conservative rule: **disable only what we have a direct, documented
replacement for**. The `internal/agents/claudecode.go` file holds this
list as a constant and updates per release.

### Idempotency + safety

Every operation is atomic and idempotent:

- **Atomic write**: settings.json is read, mutated in memory, written
  via `atomic.go`'s temp+rename primitive (same one Edit/Write use).
  No partial-state observation possible.
- **Idempotent claim**: running `clawtool agents claim claude-code`
  twice yields identical settings.json. We never duplicate entries in
  the array.
- **Idempotent release**: only removes the exact names clawtool added.
  If the user manually disabled `Bash` for unrelated reasons, release
  doesn't touch it. We track ownership via a sibling marker file
  `~/.claude/settings.clawtool.lock` listing tools clawtool added; on
  release we only remove those.
- **Preserves user state**: any other field in settings.json (theme,
  permissions, mcp.servers, …) is untouched. We never re-serialize the
  whole file; we only mutate `disabledTools`.
- **Dry-run**: every `claim` / `release` accepts `--dry-run` that
  prints the diff that would be applied without writing.

### Auto-claim / auto-release on plugin lifecycle

Future work (v0.8.5+): the plugin's install hook calls
`clawtool agents claim claude-code --silent`; uninstall hook calls
`release`. For v0.8.4 this is **manual** — the user runs the command
explicitly. We start manual because settings.json mutation is
high-trust; we want to verify the implementation in user hands before
making it automatic.

### Multi-agent surface

The `<agent>` argument is a string; per agent we ship a small adapter:

| Agent | Settings file | Disable mechanism |
|---|---|---|
| `claude-code` | `~/.claude/settings.json` | `disabledTools` array (v0.8.4) |
| `codex` | `~/.codex/config.toml` | TBD — Codex's mechanism (v0.8.5) |
| `opencode` | `~/.config/opencode/config.json` | TBD (v0.8.6) |
| `cursor` / `windsurf` | `mcp_config.json` per-project | possibly not needed if clawtool's MCP is the only registered server |

For v0.8.4 we ship `claude-code` only; `agents.go` defines an
interface so Codex / OpenCode adapters drop in cleanly later.

### CLI shape

```
clawtool agents <subcommand> [<agent>] [flags]

Subcommands:
  claim <agent>           Disable the native tools clawtool replaces.
  release <agent>         Re-enable everything clawtool disabled.
  status [<agent>]        Show what's claimed and which tools are off.

Flags:
  --dry-run               Print the diff without writing.
  --tools-marker <path>   Override the marker-file path (default
                          ~/.claude/settings.clawtool.lock for the
                          claude-code agent). Used by tests.
```

### Output shape

`claim` prints, e.g.:

```
✓ claimed claude-code
  • disabled in ~/.claude/settings.json: Bash, Read, Edit, Write, Grep,
    Glob, WebFetch, WebSearch
  • marker written: ~/.claude/settings.clawtool.lock
  • run 'clawtool agents release claude-code' to undo
```

`status` prints a per-agent summary table. For agents with no settings
file we report "not detected" rather than failing.

## Alternatives Rejected

- **Replace built-in tools at the binary level (e.g. CC fork).** Not
  feasible; we don't own Claude Code's source and forking just to
  swap tools is anti-distribution.
- **Add a system-level CLAUDE.md telling the agent never to use
  native Bash.** Soft (same problem as skill bias) and pollutes user
  config.
- **Use Claude Code's `permissions.deny` instead of `disabledTools`.**
  Both work; `disabledTools` is the documented surface for "make it
  invisible." `permissions.deny` answers "if asked, refuse" which is
  about runtime checks, not tool-list shaping. We picked the right
  field for the goal.
- **Auto-claim on plugin install.** Postponed to v0.8.5 after the
  manual command earns trust.
- **Disable Task / Agent / TodoWrite too.** Out of scope — clawtool
  has no replacement for those. Disabling them would brick CC.
- **Make claim global (all agents at once).** Rejected — different
  agents have different risks; one explicit command per agent is
  clearer.

## Consequences

- **Settings.json mutation requires user trust.** We document the
  exact diff in `claim --dry-run` so users can audit before applying.
  We never modify settings without an explicit `clawtool` command.
- **Marker file** `~/.claude/settings.clawtool.lock` is now a thing.
  It records which tools clawtool disabled so release is precise.
  Documented in SECURITY.md as a clawtool-owned artifact (not user data).
- **CONTRIBUTING.md gets a checklist item:** every new core tool
  needs to update the disabled-set in `internal/agents/claudecode.go`
  if a corresponding native tool exists. Otherwise users running
  `clawtool agents claim` won't get full coverage.
- **Plugin uninstall must auto-release** (v0.8.5 work). Until then,
  if a user uninstalls the plugin without running `clawtool agents
  release claude-code`, native tools stay disabled and clawtool's
  MCP registration is gone — leaving Claude Code partly disarmed.
  We surface a warning in the plugin uninstall message until the
  hook is wired.
- **Tests use a tmp settings path** via the `--tools-marker` flag and
  an env var override on the settings-file path; we never touch the
  real `~/.claude/settings.json` during `make test`.

## Status

Developing. v0.8.4 ships claim/release/status for the claude-code
adapter. v0.8.5 wires plugin install/uninstall hooks to call them
automatically (after the manual flow is verified in the wild).
