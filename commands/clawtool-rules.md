---
description: Manage clawtool rules (predicate-based invariants enforced at lifecycle events). List, show, add, or remove rules in .clawtool/rules.toml or ~/.config/clawtool/rules.toml.
allowed-tools: mcp__clawtool__Bash, mcp__clawtool__RulesAdd, mcp__clawtool__RulesCheck
argument-hint: <list|show|add|remove> [args]
---

Manage operator-declared invariants. Rules fire at lifecycle
events (`pre_commit`, `post_edit`, `session_end`, `pre_send`,
`pre_unattended`) and gate the action when severity is `block`,
or warn when severity is `warn`.

**List existing rules**:
```bash
clawtool rules list
```

**Inspect one rule**:
```bash
clawtool rules show readme-current
```

**Add a new rule** — when the operator says "every commit should
update X if Y changed", or "block commits with Co-Authored-By":

ASK FIRST: should the rule be **local** (project-only,
`.clawtool/rules.toml`) or **user** (global, applies to every
repo, `~/.config/clawtool/rules.toml`)? Default is local.

Then via the MCP tool (preferred — programmatic + validated):
```
mcp__clawtool__RulesAdd(
  name: "readme-current",
  when: "pre_commit",
  condition: 'not (changed("internal/tools/core/*.go") and not changed("README.md"))',
  severity: "warn",
  hint: "Update README's feature table when shipping a new core tool.",
  scope: "local"
)
```

Or via CLI:
```bash
clawtool rules new readme-current \
  --when pre_commit \
  --condition 'not (changed("internal/tools/core/*.go") and not changed("README.md"))' \
  --severity warn \
  --hint "Update README's feature table when shipping a new core tool." \
  --local
```

**Remove a rule**:
```bash
clawtool rules remove readme-current
```

**Predicate DSL cheat sheet**:
- `changed("path/glob")` — glob match against staged paths
- `commit_message_contains("substring")`
- `tool_call_count("Edit") > 5`
- `arg("instance") == "opencode"`
- `true` / `false`
- Combine with `and` / `or` / `not` / parens

See `docs/rules.md` for the full schema.

**Hard rules**:
- Always ASK the operator about scope (local vs. user) — local is
  the default but never assume.
- Never write rules.toml by hand — use `RulesAdd` or `clawtool rules
  new` so the writer validates the predicate syntax.
- Never silently change a rule's severity without explicit operator
  request — operator-declared severity is policy.
