# Archon — setup playbook

Connects clawtool's `playbook list-archon` surface to
[coleam00/Archon](https://github.com/coleam00/Archon), a YAML
DAG-workflow loader for AI-agent harnesses. Phase 1 is read-
only: clawtool parses workflow YAMLs under
`<repo>/.archon/workflows/` and surfaces them via
`clawtool playbook list-archon`. Phase 2 will add `playbook
run`.

## When to use this

- **Idea-to-PR pipelines** — Archon's sweet spot. A YAML
  workflow chains `prompt:` nodes (LLM calls), `bash:` nodes
  (shell steps), and `loop:` nodes (retry / fan-out) into a
  single dispatchable graph.
- **Multi-step reviews** — gating a PR through "summarise diff
  → run tests → suggest follow-ups" expressed as a graph the
  operator can read top-to-bottom.

clawtool's loader (phase 1) tags unrecognised node kinds rather
than erroring, so an upstream Archon schema bump never breaks
`clawtool` startup — you just see the new kinds annotated as
"unknown" in `playbook list-archon` until the loader catches up.

## Prerequisites

- A repo where you've already run `clawtool init`.
- (Optional, for execution) Archon installed upstream:
  ```bash
  git clone https://github.com/coleam00/Archon.git
  cd Archon
  bun install
  ```
  Phase 1 of the clawtool integration is parser-only — you can
  list workflows without Archon installed. Execution lands in
  phase 2.

## Step 1 — drop a sample workflow

```bash
clawtool recipe apply archon-template
```

This writes `.archon/workflows/idea-to-pr.yaml` — a sample DAG
that exercises every node kind clawtool's phase-1 loader
understands (`prompt`, `bash`, `loop`). Open the file to see
the canonical shape:

```yaml
# managed-by: clawtool
name: idea-to-pr
description: Sketch an idea, generate code, run tests, open a PR.
nodes:
  - id: sketch
    kind: prompt
    prompt: "Outline the change..."
  - id: implement
    kind: bash
    command: "clawtool send --agent claude '...'"
    depends_on: [sketch]
  - id: tests
    kind: bash
    command: "go test ./..."
    depends_on: [implement]
```

## Step 2 — list workflows

```bash
clawtool playbook list-archon
```

Default scans `<cwd>/.archon/workflows/`. Output:

```
Archon workflows in <cwd>/.archon/workflows:
  idea-to-pr — Sketch an idea, generate code, run tests, open a PR. (3 nodes)
```

Pin the directory explicitly with `--dir`, or get JSON output
for piping to `jq`:

```bash
clawtool playbook list-archon --dir /path/to/repo --format json | jq '.[].name'
```

The JSON shape is `[{name, description, path, node_count}]` —
stable per the verb's tests; phase 2 will add a `nodes` field
for the executor.

## Step 3 — author your own workflow

Drop additional YAMLs under `.archon/workflows/`. Each file
should follow the same shape as `idea-to-pr.yaml`:

- `name:` — unique identifier (matches the filename stem by
  convention).
- `description:` — one-line summary surfaced in `list-archon`.
- `nodes:` — list of `{id, kind, depends_on?, ...}` entries.
  `kind` is one of `prompt`, `bash`, `loop` (phase 1's
  recognised set).

Re-run `clawtool playbook list-archon` to confirm the new
file parses.

## Step 4 — execute (phase 2 placeholder)

Phase 1 ships parser + listing only. To execute a workflow
today, run it through Archon directly:

```bash
cd /path/to/Archon
bun run cli -- run /path/to/repo/.archon/workflows/idea-to-pr.yaml
```

Phase 2 of the clawtool integration adds `clawtool playbook
run <name>` — a unified executor that maps each node kind to
the corresponding clawtool surface (BIAM dispatch for `prompt`,
shell-mcp for `bash`).

## Troubleshooting

- **`no Archon workflows under <cwd>/.archon/workflows`** — the
  directory is empty or doesn't exist. Run the recipe (Step 1)
  to drop the sample.
- **Workflow listed but `(0 nodes)`** — the YAML parsed but the
  `nodes:` block is empty or malformed. Re-open the file and
  cross-check against the sample.
- **`unknown node kind ...`** — a phase-1 loader gap, not an
  error. Open an issue with the workflow YAML attached and the
  loader will be extended.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/archon/setup.md`.
**Auth flows covered**: none — Archon workflows are local YAML.
Authentication for the model providers each `prompt:` node
calls is handled per-provider (Anthropic / OpenAI / Gemini env
vars; see the BIAM transport docs).
