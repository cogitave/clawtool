---
type: decision
title: "006 Instance scoping and tool naming"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - naming
  - multi-instance
status: developing
related:
  - "[[004 clawtool initial architecture direction]]"
  - "[[005 Positioning replace native agent tools]]"
sources: []
---

# 006 — Instance Scoping and Tool Naming

> **Status: developing.** Locks the naming convention used both on the wire (MCP) and in the CLI/config. Critical to get right before any tool ships, because changing it later breaks every user's config.

## Context

Real workflows need multiple instances of the same logical source:

- Two GitHub accounts (personal + work)
- Two Slack workspaces (acme + personal)
- Two AWS accounts (prod + dev)
- Two Postgres databases (local + staging)

Each is the *same* MCP server package invoked with different credentials, but produces an independent tool surface. If clawtool exposes both as `github.create_issue`, the agent cannot disambiguate.

Constraint from the user:

> *"Adlandırmalarda çakışma olmayacak şekilde planlamalısın … aynı şeyden iki tane bağlamak isteyebilirler github gibi düşün çakışmasın iki üyelik ayrı olur sonuçta."*
>
> Plan naming so collisions are impossible. Users may want to attach two of the same source — think GitHub — must not collide; two memberships are separate accounts.

Additional constraint: clawtool's tool names must be predictable and follow conventions familiar to AI coding agents (Claude's PascalCase native built-ins + MCP's snake_case community style + the `mcp__<server>__<tool>` wire wrapper).

## Decision

clawtool introduces an **instance** layer between the source (MCP server type) and the tool. Naming has two forms:

| Surface | Form | Example |
|---|---|---|
| **Wire (MCP tool name)** | `<instance>__<tool>` (two underscores) | `github-personal__create_issue` |
| **CLI / config selector** | `<instance>.<tool>` (one dot) | `github-personal.create_issue` |

Mapping is mechanical and reversible: `__` ↔ `.` only at the instance boundary.

### Naming rules

- **Instance name** (user-chosen):
  - Charset: `[a-z0-9-]+` (kebab-case only — **no underscores**, no dots)
  - Length: 2–32 chars
  - Must be unique within a clawtool installation
  - Default if user adds the first instance of a source without a name: just the source name (e.g. `github`). Adding a second forces explicit names: `clawtool` errors with "instance name required" and suggests `github-personal` / `github-work`.
- **Tool name** (from underlying source):
  - Charset: `[a-z0-9_]+` (snake_case — **no hyphens**, no dots)
  - clawtool does not rename source-provided tool names; only prefixes them with the instance.
- **Separator**: `__` on the wire (two underscores), `.` in CLI/config.

The disjoint charsets (`-` only in instance, `_` only in tool) make `__`-split unambiguous.

### Wire-level shape

When Claude Code or another MCP client connects to clawtool, the host wrapper adds its own `mcp__<server>__` prefix. End shape Claude sees:

```
mcp__clawtool__github-personal__create_issue
└──┬──┘└──┬───┘└─────┬────────┘└───┬────────┘
  host   server     instance       tool
   tag    name      (clawtool)     (source)
```

Three `__`-delimited segments inside the tool-name field. Trivial to parse on either side.

### clawtool core tools (the canonical replacements)

Core tools — the ones positioned per [[005 Positioning replace native agent tools]] — get **PascalCase names** to match Claude's native built-in convention as closely as possible. Their wire form drops the instance segment because they have no instance:

| Core tool | Wire form |
|---|---|
| `Bash` | `mcp__clawtool__Bash` |
| `Read` | `mcp__clawtool__Read` |
| `Edit` | `mcp__clawtool__Edit` |
| `Write` | `mcp__clawtool__Write` |
| `Grep` | `mcp__clawtool__Grep` |
| `Glob` | `mcp__clawtool__Glob` |
| `WebFetch` | `mcp__clawtool__WebFetch` |
| `ToolSearch` | `mcp__clawtool__ToolSearch` |

Selectors for core tools use PascalCase too: `clawtool tools disable Bash`. Tags still apply (`tag:destructive`).

### Disambiguation rule for core vs sourced

- A clawtool name that **starts with an uppercase letter and contains no `__`** is a **core tool** (`Bash`, `WebFetch`, …).
- A clawtool name that **contains `__`** is a **sourced instance tool** (`github-personal__create_issue`).

These never overlap because instance names are kebab-case (lowercase + hyphen) — the leading character of a sourced tool is always lowercase.

### CLI examples

```bash
# Add first instance — bare name allowed
clawtool source add github -- npx -y @modelcontextprotocol/server-github
# Adding a second forces explicit naming
clawtool source rename github github-personal
clawtool source add github-work -- npx -y @modelcontextprotocol/server-github

# Configure each with its own auth (in config.toml)
clawtool source set github-personal.env GITHUB_TOKEN=$PERSONAL_TOKEN
clawtool source set github-work.env GITHUB_TOKEN=$WORK_TOKEN

# Toggle tools — either instance independently
clawtool tools disable github-work.delete_repo
clawtool tools enable github-personal.create_issue

# Toggle a core tool
clawtool tools disable Bash      # use the agent's native Bash for this profile
clawtool tools enable Bash

# Status / inspection
clawtool tools status github-work.delete_repo
# → "disabled (rule: tools disable github-work.delete_repo)"
```

### Config file shape (`~/.config/clawtool/config.toml`)

```toml
[core_tools]
Bash = { enabled = true }
Read = { enabled = true }
WebFetch = { enabled = true }

[sources.github-personal]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_PERSONAL_TOKEN}" }

[sources.github-work]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_TOKEN = "${GITHUB_WORK_TOKEN}" }

[tools."github-work.delete_repo"]
enabled = false                              # explicit per-tool override

[tags.destructive]
match = ["*.delete_*", "*.drop_*", "Write"]  # patterns auto-tag tools
disabled = true

[groups.review-set]
include = ["Grep", "Read", "github-personal.create_issue", "github-personal.list_pulls"]

[profile.active]
name = "personal"
```

## Alternatives Rejected

- **Single-underscore separator (`<instance>_<tool>`)** — collides with snake_case tool names like `create_issue`. Splitting becomes ambiguous.
- **Dot on the wire (`<instance>.<tool>`)** — some MCP clients sanitize `.` in tool names. `__` is universally safe.
- **Force user to choose unique tool names per instance** — leaks the multi-instance concern to the user; defeats the point.
- **Number-suffix instances (`github`, `github2`, `github3`)** — meaningless to users. Named instances (`github-personal`) carry intent.
- **Allow underscores in instance names** — breaks the unambiguous-split guarantee.
- **Lowercase core tools (`bash`, `read`)** — abandons Claude's native PascalCase convention. PascalCase signals "this is a first-class tool, peer to native built-ins" — important for [[005 Positioning replace native agent tools]] credibility.
- **Add a separate "namespace" concept beyond instance** — over-engineering. Source + instance is enough; tags/groups cover orthogonal grouping.

## Consequences

- **First-instance ambiguity is intentionally rejected:** when a user has only `github` and adds a second, clawtool forces a rename of the first to `github-personal` (or whatever) before adding `github-work`. Prevents a silent collision later. The CLI prompts the user.
- **Core tools cannot be renamed.** `Bash` is `Bash` everywhere. This is the canonical-layer guarantee — no one's `Bash` is different from anyone else's.
- **Wire format is stable across clawtool versions.** Reordering segments or changing separators in v2 breaks every user's `mcp add` config.
- **Pattern matching for tags uses glob** (`*.delete_*`, `Write`). Patterns evaluate against the **selector form** (dot-notation), not wire form, for readability.
- **Two-character separators (`__`)** are slightly verbose. Trade accepted for unambiguous parsing.

## Status

Developing. The prototype implementation must respect this convention from the first commit. Changing it later means breaking every `mcp__clawtool__*` reference in user configs and agent histories.
