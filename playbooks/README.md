# Playbooks — agent-readable tool integration recipes

`playbooks/` is the **markdown layer** clawtool ships alongside the
MCP source-server layer. Inspired by ZhixiangLuo's
[10xProductivity](https://github.com/ZhixiangLuo/10xProductivity)
philosophy: every tool a human can use on a laptop, an agent can
use too — through browsers, CLIs, REST APIs — without a middleware
MCP server.

## Why this layer exists alongside `clawtool source add`

clawtool already exposes integrated tools via the **MCP source**
mechanism: `clawtool source add github` spins up
`@modelcontextprotocol/server-github` as a child process, proxies
its tools through the supervisor, and the agent calls structured
MCP tools.

The MCP-source path is the right one when:
- A maintained MCP server already exists for the tool.
- The agent benefits from typed tool schemas (typed inputs, JSON
  output, no shell parsing).
- Auth is via tokens / API keys, not browser-OAuth.

The **playbook** path is the right one when:
- No MCP server exists (yet) — internal portals, niche SaaS,
  custom systems.
- Authentication needs the user's existing browser session (SSO
  flows, MFA, "log in via Okta") that bare API keys can't cover.
- A workflow composes multiple tools whose individual MCP servers
  don't compose well — e.g. read Slack thread + open Jira + post
  GitHub comment in a single agent run.
- Zero-infrastructure is a hard requirement: no daemon, no
  hosted service, no IT ticket.

Both layers coexist. A playbook can call `mcp__clawtool__Bash` to
shell out to `gh` CLI, `mcp__clawtool__BrowserFetch` to drive a
SPA, or `mcp__clawtool__SendMessage` to fan out to another agent.

## Directory layout

```
playbooks/
  README.md                ← this file
  add-new-tool.md          ← meta-playbook for connecting any
                             tool not already covered

  <tool>/                  ← pre-built recipes for common tools
    setup.md               ← orchestration: ask URL, run auth,
                             validate the connection
    connection-<auth>.md   ← one file per auth flow (SSO, PAT,
                             OAuth device-flow, …)

  workflows/               ← composed flows that span multiple
                             tools
    <flow>.md              ← one file per workflow

  _personal/               ← gitignored — user-private playbooks
                             for internal company tools that
                             stay on the operator's machine
    .gitkeep               ← placeholder so `playbooks/_personal/`
                             survives a clone of the repo
```

### Why `_personal/` is gitignored

Internal company tools (the proprietary CRM, the company-specific
incident dashboard, the bespoke deploy console) carry tribal
knowledge that's specific to one organization. The playbook
conventions and example file are open-source; the playbooks
themselves stay on the operator's machine. clawtool's
`.gitignore` excludes `playbooks/_personal/*` by default; the
operator's authoring tools (`clawtool playbook new <name>
--personal`, future tick) drop new files there.

## Reading playbooks

For now, the agent reads playbooks the same way it reads any
other doc: the operator says "read playbooks/github/setup.md and
help me set up GitHub" and the agent follows the markdown
instructions, calling `mcp__clawtool__Bash` / `BrowserFetch` /
`PortalAsk` as needed.

A future `clawtool playbook` verb (separate autodev tick) will
add:
- `clawtool playbook list` — enumerate available playbooks +
  status (configured / not).
- `clawtool playbook show <name>` — print the playbook for the
  agent or operator.
- `clawtool playbook new <name>` — scaffold a new playbook from
  the `add-new-tool.md` meta-template.

## Threat model

Per the 10xProductivity philosophy: the agent is *the operator,
running scripts*. The threat model is identical to a human
opening a browser and following the same steps. Nothing new is
exposed:
- The agent uses **the operator's** existing browser sessions /
  CLI tokens — no new service accounts, no new credentials.
- Every action is attributed to **the operator** in upstream
  audit logs.
- Credentials never leave the operator's machine. There's no
  hosted middleware, no cloud relay, no shared secret.

The only added trust is in the agent runtime (Claude Code,
Cursor, Codex, …) — which the operator already chose to trust
when they installed it.

## Tool playbooks

Pre-built recipes for tools clawtool integrates with. Each one
follows the structure documented in `add-new-tool.md` (Prereqs /
Install / Auth / Register-with-clawtool / Smoke-test /
Troubleshoot).

- `aider/setup.md` — Aider as BIAM peer #6 (repo-map-aware
  pair-programming CLI).
- `archon/setup.md` — Archon YAML workflow loader (phase 1:
  parse + list via `clawtool playbook list-archon`).
- `github/setup.md` — GitHub via `gh` CLI OAuth device flow.
- `mastra/setup.md` — Mastra TypeScript agent server, driven via
  HTTP from `mcp__clawtool__Bash`.
- `mcp-toolbox/setup.md` — Google's reference DB MCP server
  (Postgres, MySQL, SQLite, BigQuery, Mongo, Redis, Spanner).
- `promptfoo/setup.md` — promptfoo redteam baseline that drives
  every clawtool agent family through `clawtool send --agent`.
- `rtk/setup.md` — rtk CLI proxy that compresses Bash output for
  60-90% token savings (pre_tool_use rewrite layer).
- `semble/setup.md` — semble code-search MCP source (~98% fewer
  tokens than grep+read).
- `shell-mcp/setup.md` — sandbox-aware shell MCP server with
  per-directory allowlist.

## Status

Foundation + an initial wave of tool playbooks. Still on the
backlog:

- ⏳ `slack/` — to come.
- ⏳ `jira/` — to come.
- ⏳ `linear/` — to come.
- ⏳ `confluence/` — to come.
- ⏳ `workflows/enterprise-search.md` — composed search flow
  across all configured playbooks (the 10xProductivity
  flagship workflow).

The `clawtool playbook` CLI verb landed in phase 1 with
`list-archon`; the broader `list` / `show` / `new` surface is
still on the autodev backlog.
