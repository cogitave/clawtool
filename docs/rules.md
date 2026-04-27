# clawtool rules

Operator-defined invariants enforced by the `internal/rules` engine
and surfaced via the `RulesCheck` MCP tool. Per ADR-021 (core tools
polish) and the upcoming ADR-022 (checkpoint), rules give clawtool
a way to encode "you can't end this session without doing X" without
hard-coding the policy into individual tools.

## Where the file lives

Rules are project-scoped first, user-global second:

1. `./.clawtool/rules.toml` — project-local, highest precedence
2. `~/.config/clawtool/rules.toml` — XDG fallback
   (or `$XDG_CONFIG_HOME/clawtool/rules.toml` when set)

First match wins; clawtool does not merge across roots. Drop a
`.clawtool/rules.toml` into a repo to scope rules to that project
without affecting your other repos.

When no file is present, clawtool's mode is **permissive** — rules
are opt-in.

## Schema

```toml
[[rule]]
name        = "no-coauthor"
description = "Hard-block on AI attribution in commits."
when        = "pre_commit"          # pre_commit | post_edit | session_end | pre_send | pre_unattended
condition   = 'not commit_message_contains("Co-Authored-By")'
severity    = "block"               # off | warn | block (default: warn)
hint        = "Operator memory feedback — never attribute to AI."

[[rule]]
name      = "readme-current"
when      = "pre_commit"
condition = 'not (changed("internal/tools/core/*.go") and not changed("README.md"))'
severity  = "warn"
hint      = "Update README's feature table when shipping a new core tool."

[[rule]]
name      = "skill-routing-in-sync"
when      = "pre_commit"
condition = 'not (changed("internal/tools/core/*.go") and not changed("skills/clawtool/SKILL.md"))'
severity  = "block"
hint      = "Three-plane shipping contract (docs/feature-shipping-contract.md) — every new core tool needs a SKILL.md routing-map row."

[[rule]]
name      = "no-opencode-codewriting"
when      = "pre_send"
condition = 'arg("instance") == "opencode"'
severity  = "block"
hint      = "Operator memory feedback — opencode is research-only; route code-writing tasks to codex / gemini / claude / hermes."
```

## Predicate vocabulary

| Predicate | Description |
|---|---|
| `changed(glob)` | True if any path in `Context.ChangedPaths` matches `glob` (doublestar globbing — `**` for recursive). |
| `any_change(glob)` | Alias for `changed`. |
| `commit_message_contains(s)` | Substring match against `Context.CommitMessage`. |
| `tool_call_count(name) > N` | Numeric compare on `Context.ToolCalls[name]`. Supports `>`, `>=`, `==`, `!=`. |
| `arg(key) == "value"` | String compare on `Context.Args[key]`. Supports `==`, `!=`. |
| `true` / `false` | Literal booleans, useful for staging or temporarily neutralising a rule. |

Logical operators: `and` / `or` / `not` (case-insensitive; `&&` / `||`
also accepted). Parens group; precedence is `not` > `and` > `or`.

## Severity ladder

- `off` — rule defined but disabled. Useful for staging a new rule
  before flipping it on.
- `warn` — surface the violation in the result payload but don't
  block. Default when severity is omitted.
- `block` — refuse the action. Callers MUST treat a `block` result
  as a hard stop.

## Events

| Event | Fires from |
|---|---|
| `pre_commit` | The future `Commit` core tool, before finalising. |
| `post_edit` | After `Edit` / `Write` succeed. |
| `session_end` | When the BIAM task / agent loop terminates. Last-chance gate. |
| `pre_send` | Before `SendMessage` dispatches to a clawtool instance. |
| `pre_unattended` | Before `--unattended` mode activates. The safety brake before unsupervised loops. |

## How agents call it

From any agent loaded with the clawtool skill:

```
mcp__clawtool__RulesCheck(
  event="pre_commit",
  changed_paths=["internal/tools/core/bash.go", "skills/clawtool/SKILL.md"],
  commit_message="feat(bash): background mode\n\n…",
  tool_calls={"Edit": 5, "Write": 1},
  args={}
)
```

Returns a `Verdict` with `results`, `warnings`, `blocked`. The agent
should treat a non-empty `blocked` list as a refusal to proceed and
surface the rule's `hint` to the operator.

## Compose with hooks

`internal/hooks` (the existing shell-script event bus) and
`internal/rules` are complementary:

- **rules** — pure in-process Go evaluation against a typed Context.
  Fast, deterministic, no shell roundtrip. Use this for invariants
  the agent should enforce mid-flight.
- **hooks** — fires shell commands. Use this when an external tool
  (CI, audit log, notification system) needs to know about the event.

A hook entry can call `clawtool rules check ...` to invoke this
engine, but most callers (the future `Commit` tool, the unattended-
mode supervisor) call `rules.Evaluate` directly.

## What ships in v0.20

- The engine, the loader, the `RulesCheck` MCP tool, the
  `clawtool rules check` CLI, this doc, sample rules.
- **Not yet wired**: automatic enforcement at tool-call time. That
  needs the Tool Manifest Registry refactor (Codex's #1 ROI pick)
  to give us a clean middleware seam. Until then, the agent calls
  `RulesCheck` explicitly at the lifecycle points it cares about.
