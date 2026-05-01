# Semble — setup playbook

Connects clawtool to [MinishLab/semble](https://github.com/MinishLab/semble),
a code-search MCP server that returns the relevant code chunks
for a query rather than the raw `grep -n` + `cat` round-trip
the agent would otherwise have to assemble. Measured retrieval
saves ~98% of the tokens vs grep-then-read on large repos.

## When to use this vs `clawtool source add semble`

`clawtool source add semble` IS the canonical install path —
this playbook is the operator-facing companion that documents
prereqs, smoke tests the source, and shows how to re-validate
later. There's no parallel CLI / browser path: semble is an MCP
server, period.

## Prerequisites

- Python 3.10+ (`python --version`).
- `uv` on PATH. Install per upstream:
  ```bash
  curl -LsSf https://astral.sh/uv/install.sh | sh
  ```
  (Or `pipx install uv`, `brew install uv`, etc. See
  <https://docs.astral.sh/uv/getting-started/installation/>.)
- No API keys. semble runs fully on CPU.

## Step 1 — confirm uv

```bash
uv --version
```

Should print `uv 0.x.y`. If not, finish the upstream install
and re-open the shell.

## Step 2 — register with clawtool

```bash
clawtool source add semble
```

This writes the source entry under your clawtool config and
spawns the MCP server via the catalog command:

```
uvx --from "semble[mcp]" semble
```

The first run downloads `semble[mcp]` and its dependencies into
`uv`'s cache; subsequent runs are warm-cache fast. There's no
auth step — semble has no remote surface.

## Step 3 — drop the workflow marker (optional)

```bash
clawtool recipe apply semble
```

This writes `.clawtool/semble/config.yaml` — a starter that
documents the indexing-and-query workflow for sub-agents that
read the marker. The MCP server itself ignores this file; it's
purely a discoverability artifact for the operator + downstream
agent prompts.

## Step 4 — round-trip a query

After `source add semble`, semble's tools appear under the
`mcp__clawtool__semble__*` namespace. Smoke-test through the
agent surface:

```bash
clawtool tools list | grep semble
```

Should list at least one semble tool (e.g. `semble__search`).
Drive a query end-to-end via the agent — for example, ask the
agent "use semble to find every place we register an MCP source
in this repo" and confirm the reply cites real file paths from
the worktree.

## Troubleshooting

- **`uvx: command not found`** — `uv` isn't on PATH. Re-run the
  installer (Step 1) and re-open the shell.
- **First call hangs for ~30s** — first-run dependency download.
  Subsequent calls are sub-second. If it stays hung past a
  minute, kill and inspect with `uv cache dir` to confirm the
  cache directory is writable.
- **`semble__search` returns empty** — semble indexes lazily on
  first query. Re-run the same query; the second call should
  hit the warm index.
- **Wrong repo indexed** — semble indexes the cwd of the spawned
  MCP server. Restart `clawtool` from the repo root you intend
  to search.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/semble/setup.md`.
**Auth flows covered**: none required (local CPU indexing).
