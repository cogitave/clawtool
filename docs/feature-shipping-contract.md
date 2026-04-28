# Feature shipping contract

> **Promise to the operator**: every clawtool feature must arrive as a
> *complete package* — MCP tool **and** marketplace surface **and**
> agent-routing bias. A feature that exists only on one of those three
> planes leaves install-time users in a partial state.

## The three-plane rule

When you ship a new core capability `X`, all three planes must be
updated *in the same commit*:

### Plane 1 — MCP tool (the engine)

- `internal/tools/core/<x>.go` — the implementation
- `RegisterX(s)` wired into `internal/server/server.go`
- ToolSearch entry added to `internal/tools/core/toolsearch.go`'s
  `CoreToolDocs()` so discovery works
- Tests under `internal/tools/core/<x>_test.go`, `-race -count=1` clean

### Plane 2 — marketplace surface (the install-time face)

- Slash command in `commands/clawtool-<x>.md` (only when X has a
  user-facing verb — `BashOutput` doesn't need one, `Commit` does)
- Plugin manifest version bumped in `.claude-plugin/plugin.json` and
  `.claude-plugin/marketplace.json`
- README feature list updated under "Tools" / "Commands" sections
- `docs/<x>.md` page when X has more than ~5 lines of operator-facing
  behaviour

### Plane 3 — agent routing bias (the "Claude won't forget"
guarantee)

- `skills/clawtool/SKILL.md` routing map gets a row mapping the
  *intent* to the new tool — not just the tool's existence, but the
  trigger phrases and the wrong path it replaces
- `description` field at the top of SKILL.md adds the trigger
  vocabulary so Claude pulls the skill into context the moment the
  user expresses that intent
- If the new tool *replaces* a Bash one-liner the agent might reach
  for, add an explicit "instead of `git commit -m …`, use Commit"
  redirect — Claude obeys explicit redirects more reliably than
  implicit "prefer clawtool" wording

## Why all three

| Plane | What it guarantees | Failure mode if missing |
|---|---|---|
| MCP tool | Tool *exists* and is callable | feature is dead |
| Marketplace surface | Tool *appears* on install | tool exists but is invisible |
| Routing bias | Tool *gets picked* over the wrong path | tool appears but agents still shell out to Bash |

The third plane is the easiest one to skip and the most expensive to
miss — without it, the agent uses the new tool the day you ship it
(while you're testing) and forgets it three days later when conversation
context shifts. The skill bias is what keeps the discipline after
attention moves on.

## Review checklist

Before merging a feature PR, the reviewer (human or agent) walks this
list:

- [ ] `internal/tools/core/<x>.go` exists, registered in `server.go`
- [ ] `CoreToolDocs()` lists the tool with keywords
- [ ] Tests under `-race -count=1`
- [ ] `commands/clawtool-<x>.md` exists (or feature is sub-tool only)
- [ ] `.claude-plugin/plugin.json` version bumped
- [ ] `skills/clawtool/SKILL.md` routing map row added
- [ ] SKILL.md description field updated with trigger phrases
- [ ] If the tool replaces a Bash idiom, explicit redirect is in SKILL.md
- [ ] An architecture decision record under `wiki/` if the feature has a
      non-trivial design choice

## Deviations

A PR that ships fewer than three planes must say so in the commit body
and link the follow-up issue that closes the gap. "Will fix in next
commit" is *not* an acceptable deviation — by the time you remember,
you won't.
