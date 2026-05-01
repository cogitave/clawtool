# Aider — setup playbook

Connects clawtool's BIAM dispatch surface to [Aider](https://github.com/Aider-AI/aider),
a repo-map-aware pair-programming CLI that lands diffs straight
into the working tree. Aider is BIAM peer #6 alongside claude /
codex / opencode / gemini / hermes — picked when the prompt
spans many files and the repo-map context is the cheapest
shortcut to a correct edit.

## When to use this vs the other code-writing peers

- **claude / codex / gemini / hermes** are the general-purpose
  code-writing peers. They handle most prompts.
- **opencode** is read-only (research). Per the routing memory,
  code-writing never goes there.
- **This peer (`aider`)** is the right pick when the prompt
  edits many files and the agent benefits from a pre-built repo
  map: Aider parses the whole tree once and reasons over the
  map, which makes "rename this method everywhere it appears"
  or "thread this new arg through every caller" cheap.

Aider stores chat history in `.aider.chat.history.md` in the
cwd, which it reads automatically on subsequent runs — so a
sequence of `clawtool send --agent aider` calls in the same
working directory composes into a single Aider session without
clawtool plumbing a session ID.

## Prerequisites

- Python 3.10+ (`python --version`).
- `pip install aider-chat` (the lighter PyPI distribution) or
  `pip install aider-install && aider-install` (Aider's bundled
  installer). Both put `aider` on PATH; `aider-chat` is the path
  the rest of this playbook assumes.
- An API key for whichever model Aider should drive. The default
  is Claude Sonnet — set `ANTHROPIC_API_KEY`. For OpenAI models
  set `OPENAI_API_KEY`; for Gemini, `GEMINI_API_KEY`.
- A git working tree. Aider refuses to run outside one because
  every edit lands as a commit on `HEAD`.

## Step 1 — confirm the binary

```bash
aider --version
```

Should print a version like `aider 0.62.x`. If `aider` isn't on
PATH, re-run `pip install aider-chat` and re-open the shell so
the new entry-point is picked up.

## Step 2 — set the API key

Export the key in your shell profile so subsequent agent
sessions inherit it:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

Aider also reads `OPENAI_API_KEY`, `GEMINI_API_KEY`, etc.; pick
whichever matches the `--model` you intend to run. Store the key
in your OS keychain (`security add-generic-password` on macOS,
`pass insert` on Linux) and source it from the keychain rather
than checking it into a dotfile.

## Step 3 — round-trip through clawtool

clawtool's BIAM transport already wraps Aider's headless mode.
Smoke-test the dispatch:

```bash
clawtool send --agent aider "echo a one-line readme summary"
```

What happens under the hood (see
`internal/agents/aider_transport.go`):
- clawtool runs `aider --message "<prompt>" --no-stream --no-pretty`.
- `--no-stream` + `--no-pretty` strip TTY decoration so the
  dispatch pipe gets clean text.
- If you pass `--unattended` (or the global `--yolo`), clawtool
  appends Aider's `--yes-always` flag so edit / git-op prompts
  auto-confirm.

The reply prints to stdout. If Aider chose to land an edit, the
diff is committed on `HEAD` of the cwd repo before the dispatch
returns.

## Step 4 — what the agent can do now

| Intent | Command |
|---|---|
| One-shot edit | `clawtool send --agent aider "rename FooBar to BazQux across the repo"` |
| Multi-file refactor | `clawtool send --agent aider "thread context.Context through every public method in pkg/foo"` |
| Targeted fix with files | `clawtool send --agent aider "fix the off-by-one in pkg/x/y.go::Range"` |
| Unattended (CI, autodev) | `clawtool send --agent aider --unattended "..."` |

## Troubleshooting

- **`aider: command not found`** — `pip install aider-chat`
  finished but PATH wasn't refreshed. Open a new shell or
  `hash -r`.
- **`No API key found`** — Aider prints which env var it tried.
  Export the matching key (Step 2) and re-run.
- **`fatal: not a git repository`** — `cd` into a git working
  tree before dispatching. Aider commits every edit; it refuses
  to run loose.
- **The agent keeps picking aider for trivial single-line
  edits** — the routing memory's intent is "aider for repo-map
  prompts." Re-dispatch trivial fixes via `--agent claude` or
  `--agent codex` to avoid burning Aider's repo-map setup cost.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/aider/setup.md`.
**Auth flows covered**: API-key env var (Anthropic / OpenAI /
Gemini). OAuth-flow models are not yet covered.
