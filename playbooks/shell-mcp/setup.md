# shell-mcp — setup playbook

Connects clawtool to [devrelopers/shell-mcp](https://github.com/devrelopers/shell-mcp),
a sandbox-aware shell MCP server. Per-directory `.shell-mcp.toml`
allowlist that walks up the tree like git, hard non-overridable
denylist (sudo, rm -rf /, fork bombs), and rejection of every
shell metacharacter before tokenisation.

## When to use this vs `mcp__clawtool__Bash`

- **`mcp__clawtool__Bash`** is unconstrained shell. Auditable
  via the rules engine, but the agent can run any command the
  operator's shell can.
- **shell-mcp** is allowlist-first: the agent's shell surface is
  exactly what `.shell-mcp.toml` allows, and shell metacharacters
  (`; && || | $() backticks > < >>`) are rejected before parsing.
  Pick this when you want a hard sandbox boundary the agent
  can't argue its way past.
- They compose. The recommended pairing per the rtk-rewrite rule
  is: shell-mcp enforces the sandbox; rtk_rewrite optionally
  rewrites loose Bash invocations into structured shell-mcp
  calls.

## Prerequisites

- Rust toolchain (`rustc --version`) — shell-mcp is a Rust
  binary installed via `cargo install`.
- A repo root you want to scope shell access to.

## Step 1 — install the binary

```bash
cargo install shell-mcp
shell-mcp --version
```

`cargo install` puts the binary under `~/.cargo/bin/`; ensure
that's on PATH.

## Step 2 — scaffold the allowlist

Drop a starter `.clawtool/shell-mcp.toml`:

```bash
clawtool recipe apply shell-mcp
```

The starter ships:
- `include_defaults = true` — keeps shell-mcp's curated
  read-only allowlist on (git / ls / grep / cat / head / tail /
  find / tree / diff / stat / wc).
- An `allow = [...]` block mirroring the read-only set, with
  commented-out write-side examples (`cargo build **`,
  `go test ./**`, `./scripts/deploy.sh **`) for you to
  uncomment + adapt.
- A documentation comment listing the hard denylist (which
  lives upstream and cannot be relaxed by this file).

Open the file and add the write-side commands your project
needs. Pattern syntax:
- `cargo build` → exact match.
- `cargo build *` → exactly one extra arg.
- `cargo build **` → any number of extra args (only as the
  final token).

## Step 3 — point shell-mcp at the root

The catalog entry resolves the sandbox root from
`SHELL_MCP_ROOT`. Export the absolute path of the directory
you want shell access scoped to:

```bash
export SHELL_MCP_ROOT="$(pwd)"
```

Add the export to your shell profile (or a project `.envrc`)
so subsequent agent sessions inherit it.

## Step 4 — register with clawtool

```bash
clawtool source add shell-mcp
```

clawtool spawns:

```
shell-mcp --root ${SHELL_MCP_ROOT}
```

…and the agent gets shell-mcp tools under
`mcp__clawtool__shell_mcp__*`.

## Step 5 — smoke-test a sandboxed command

```bash
clawtool tools list | grep shell_mcp
```

Then drive a allowed command end-to-end through the agent —
e.g. ask the agent "use shell-mcp to run `git status`" and
confirm it returns the expected tree state. Then ask it to run
a denied command (`sudo whoami`) and confirm shell-mcp refuses
with the policy violation message.

## Troubleshooting

- **`cargo install` fails to compile** — shell-mcp pins a
  recent Rust edition. Run `rustup update stable` first.
- **`SHELL_MCP_ROOT not set`** — the catalog refuses without it.
  Export it (Step 3) and re-run `source add`.
- **Allowed command rejected** — the rejection message names
  the rule that fired. Most often it's a metacharacter the
  operator added unintentionally (e.g. `git status | grep foo`
  hits the metachar block). Write a script and allowlist the
  script.
- **Allowlist edit not picked up** — shell-mcp re-reads the
  TOML on each invocation, but if the file is symlinked make
  sure the symlink resolves. `realpath .shell-mcp.toml` to
  confirm.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/shell-mcp/setup.md`.
**Auth flows covered**: none — sandboxing is local-only,
allowlist-driven. shell-mcp has no remote surface and no auth.
