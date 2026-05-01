---
description: Import a microsoft/apm manifest (apm.yml, schema 0.1) into the current repo. Registers MCP servers via `clawtool source add`; records skills + playbooks + other agent primitives in .clawtool/apm-imported-manifest.toml for phase-2 recipe wiring.
argument-hint: [<path/to/apm.yml>] [--dry-run] [--repo <path>]
allowed-tools: mcp__clawtool__Bash
---

Wraps `clawtool apm import`. Bridge between the upstream
microsoft/apm manifest format and clawtool's source / skill /
recipe surface.

## When to use

- The operator just received an `apm.yml` (from a partner team or
  upstream registry) and wants its declared MCP servers wired up in
  one pass without retyping each `clawtool source add`.
- Periodic re-import — apm.yml changed upstream; rerun to sync
  newly-added servers and refresh the manifest stub.
- CI gate — pair with `--dry-run` to assert what an import would
  do without mutating config or secrets.

## Example invocation

Default — read `./apm.yml` and apply:
```bash
clawtool apm import
```

Explicit path — useful when the manifest lives elsewhere in the repo:
```bash
clawtool apm import config/apm.yml
```

Preview the actions without writing config or the recipe stub:
```bash
clawtool apm import --dry-run
```

Override the recipe-stub destination root (default: cwd):
```bash
clawtool apm import --repo /path/to/other/repo
```

## What happens

- Parses the apm.yml manifest (schema 0.1).
- For each declared MCP server, runs the `clawtool source add`
  resolver — bare-name lookup against the built-in catalog when
  available, else registers the raw stdio command. Required-secret
  hints surface in stdout so the operator knows which
  `clawtool source set-secret` calls are still pending.
- Skills, playbooks, and other agent primitives are recorded in
  `<repo>/.clawtool/apm-imported-manifest.toml` for the operator
  to review. Phase 2 will wire those into the recipe engine; today
  they're surface-only.

## Common pitfalls

- The phase-2 recipe wiring is NOT live yet. Skills + playbooks
  declared in apm.yml land in the manifest stub, not in
  `~/.claude/skills/` or the recipe engine. Communicate that to
  the operator if they expect skills to materialise.
- MCP servers needing secrets won't auto-prompt. After the import
  finishes, run `clawtool source check` to see which instances are
  unauth'd, then `clawtool source set-secret <instance> <KEY>` for
  each.
- `--dry-run` previews the import but doesn't validate the manifest
  schema strictly; an apm.yml with unknown fields silently warns.
  Treat dry-run as "what would I do" rather than "is this manifest
  valid".
