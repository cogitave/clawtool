# MCP Toolbox — setup playbook

Connects clawtool to [googleapis/mcp-toolbox](https://github.com/googleapis/mcp-toolbox),
Google's reference DB MCP server. Out of the box it speaks
Postgres, MySQL, SQLite, BigQuery, Mongo, Redis, and Spanner
through a single declarative `tools.yaml`: each entry binds a
named source (connection string) to a parameterised SQL / NoSQL
tool the agent can call.

## When to use this vs `clawtool source add postgres`

- **`source add postgres`** spins up the
  `@modelcontextprotocol/server-postgres` MCP server. Pick this
  for ad-hoc Postgres-only access where typed queries via the
  default tools are enough.
- **MCP Toolbox** lets you pre-author a *named* set of queries
  the agent can call — `getOrderById`, `searchInventory`,
  `dailyRevenueReport` — each with explicit parameter schemas
  and an explicit access surface. Pick this when the database
  has more than a handful of read patterns and you want the
  agent to call them by name instead of composing raw SQL.
- **Both** can coexist if you want raw `query` access alongside
  named recipes. They're separate MCP sources in clawtool.

## Prerequisites

- A database the agent will query (Postgres, MySQL, SQLite,
  BigQuery, Mongo, Redis, or Spanner).
- The `toolbox` binary from the upstream releases page:
  <https://github.com/googleapis/mcp-toolbox/releases>. Grab the
  archive for your OS / arch, extract, and put `toolbox` on
  PATH.
- A connection string for each database source you'll register.

## Step 1 — install the binary

```bash
# example, macOS arm64 — pin to a real release tag
curl -LO https://github.com/googleapis/mcp-toolbox/releases/download/v0.X.Y/toolbox-darwin-arm64.tar.gz
tar -xzf toolbox-darwin-arm64.tar.gz
sudo mv toolbox /usr/local/bin/
toolbox --version
```

## Step 2 — scaffold tools.yaml

Run the recipe to drop a starter at `.clawtool/mcp-toolbox/tools.yaml`:

```bash
clawtool recipe apply mcp-toolbox
```

The starter ships commented-out Postgres + SQLite source blocks.
Open the file and uncomment / fill in:
- A `sources:` block per database (kind, host, port, database,
  user, password — or use `${ENV_VAR}` placeholders so the
  secret never lands in the repo).
- A `tools:` block per named query, with `parameters:` schemas
  the agent will pass.

See <https://github.com/googleapis/mcp-toolbox/tree/main/docs>
for the full schema.

## Step 3 — point toolbox at the file

The catalog entry resolves the tools-file path from
`MCP_TOOLBOX_TOOLS_FILE`. Export the absolute path:

```bash
export MCP_TOOLBOX_TOOLS_FILE="$(pwd)/.clawtool/mcp-toolbox/tools.yaml"
```

Add the export to your shell profile (or a project `.envrc`
loaded by direnv) so subsequent agent sessions inherit it.

## Step 4 — register with clawtool

```bash
clawtool source add mcp-toolbox
```

clawtool reads the catalog entry, spawns:

```
toolbox --tools-file ${MCP_TOOLBOX_TOOLS_FILE}
```

…and proxies the named tools through to the agent. Each tool
arrives at the agent under `mcp__clawtool__mcp_toolbox__<name>`.

## Step 5 — smoke-test a named tool

Confirm the tool surface:

```bash
clawtool tools list | grep mcp_toolbox
```

Then exercise one tool through the agent — e.g. ask the agent
"call `mcp__clawtool__mcp_toolbox__getOrderById` with id=42"
and confirm the reply contains the row.

For raw debugging (without going through the agent) you can
also drive the toolbox HTTP surface directly — see upstream
docs for the `toolbox query --tool ...` form.

## Troubleshooting

- **`toolbox: command not found`** — the binary isn't on PATH.
  Re-run Step 1 and confirm `which toolbox` prints a path.
- **`MCP_TOOLBOX_TOOLS_FILE not set`** — `clawtool source add`
  refuses without it. Export the var (Step 3) and re-run.
- **`tools.yaml: unknown source kind ...`** — the kind name is
  case-sensitive and pinned to upstream's source list (e.g.
  `postgres`, not `postgresql`). Cross-check against the docs.
- **Connection refused / auth failure** — toolbox surfaces the
  driver error verbatim. Test the connection string with
  `psql` / `mysql` / `sqlite3` from the same host before
  blaming the MCP layer.
- **Secret accidentally committed** — replace the literal in
  `tools.yaml` with `${DB_PASSWORD}` and rotate the credential
  upstream. The starter ships with `${...}` placeholders for
  this reason.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/mcp-toolbox/setup.md`.
**Auth flows covered**: connection strings via env vars
(`${MCP_TOOLBOX_TOOLS_FILE}` resolution + `${DB_*}` placeholders
inside tools.yaml). IAM-driven auth (Cloud SQL IAM, BigQuery
service-account-impersonation) is not yet covered — open an
issue with the auth shape if you hit one.
