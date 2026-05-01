---
description: "List Archon (coleam00/Archon) DAG workflows under <dir>/.archon/workflows/. Read-only — phase 1 parses + surfaces; phase 2 will wire execution. Text or JSON output."
argument-hint: "[--dir <path>] [--format text|json]"
allowed-tools: mcp__clawtool__Bash
---

Wraps `clawtool playbook list-archon`. Surface Archon DAG
workflows that already exist in the repo, so an agent (or the
operator) knows which orchestrations are available before phase-2
execution lands.

## When to use

- A repo carries `.archon/workflows/*.yml` and the operator wants
  a quick inventory — names, descriptions, node counts — without
  opening each file.
- An agent session is deciding whether to scaffold a new workflow
  or extend an existing one; this verb is the read step.
- CI / dashboard pipelines — `--format json` gives a stable shape
  with `name`, `description`, `path`, `node_count` for each
  workflow.

## Example invocation

Default — list workflows under `./.archon/workflows/` in text form:
```bash
clawtool playbook list-archon
```

Different repo / subdirectory:
```bash
clawtool playbook list-archon --dir /path/to/other/repo
```

Machine-readable for downstream tooling:
```bash
clawtool playbook list-archon --format json | jq '.[] | {name, node_count}'
```

## What happens

- Walks `<dir>/.archon/workflows/` for `*.yml` and `*.yaml` files.
- Parses each as an Archon DAG manifest (per coleam00/Archon
  schema). Successfully-parsed workflows surface with name,
  description, file path, and node count. Parse errors are
  reported per-file but don't abort the listing.
- Read-only — never edits or executes a workflow. Phase 2 will
  add `clawtool playbook run-archon <name>` for execution.

## Common pitfalls

- Phase 1 is read-only. There's no execution today — surfacing a
  workflow doesn't mean clawtool will run it. Don't promise
  end-to-end automation off this verb yet.
- The schema is upstream Archon's, not a clawtool invention.
  Workflows authored against an older Archon version may show
  zero nodes if the schema diverged; check Archon's release
  notes if a known-good workflow surfaces empty.
- The default `--dir` is the cwd — running from outside the repo
  root with no `--dir` will silently list zero workflows. Pass
  `--dir` explicitly if the verb's output looks empty.
