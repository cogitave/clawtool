# clawtool bootstrap

Zero-click onboarding verb (v0.22.76). After a fresh install
the operator should do nothing: `clawtool bootstrap` spawns the
chosen BIAM peer's CLI with its elevation flag, pipes a
bootstrap prompt to it as the FIRST user message, and streams
the agent's reply back to stdout. The agent runs
`OnboardWizard` + `InitApply` autonomously, then prints a
3-line summary and exits.

## When to use

| Situation | Use |
| --- | --- |
| Fresh repo, fresh install, want everything scaffolded by an agent in one command | `clawtool bootstrap` |
| Already have a CLAUDE.md, want to drop a single recipe | `clawtool recipe apply <name>` |
| Want the interactive TUI wizard (bridge installs, daemon ensure, MCP host registration) | `clawtool onboard` |
| Want to drive setup from chat instead of from a terminal | `OnboardWizard` + `InitApply` MCP tools (see `docs/mcp-authoring.md`) |
| Want a long-running agent loop after setup | `clawtool autonomous` (see `docs/autonomous.md`) |

`bootstrap` is the equivalent of running `onboard` non-interactively
through an agent — the agent does the clicking instead of the
operator.

## Surface

```text
clawtool bootstrap [--agent <family>] [--workdir <path>] [--dry-run]
```

Flags:

| Flag | Default | Notes |
| --- | --- | --- |
| `--agent <family>` | `claude` | Peer to spawn. One of: claude / codex / gemini / opencode / hermes / aider. |
| `--workdir <path>` | cwd | cwd handed to the spawned agent. |
| `--dry-run` | off | Print planned spawn argv + bootstrap prompt and exit without invoking the agent. |

## What gets spawned

Per family, `bootstrap` invokes the canonical headless mode plus
the elevation flag from `agents.ElevationFlag`. The prompt is
piped via stdin uniformly:

| Family | Spawn |
| --- | --- |
| claude | `claude <flag> --print --output-format text` |
| codex | `codex exec --skip-git-repo-check --json <flag>` |
| gemini | `gemini -p - --skip-trust --output-format text <flag>` |
| opencode | `opencode run <flag>` |
| hermes | `hermes chat <flag>` |
| aider | `aider --message-file - --no-stream --no-pretty <flag>` |

When the binary is missing from PATH, `bootstrap` prints a
canonical install hint and exits 2:

```text
clawtool bootstrap: claude not found on PATH. Install: visit
anthropic.com/claude-code or run "npm i -g @anthropic-ai/claude-code".
```

## Bootstrap prompt

The verbatim markdown blob piped to the agent:

```text
# clawtool bootstrap

You're being onboarded by clawtool. Run the following steps
autonomously, without asking for confirmation:

1. Run the OnboardWizard MCP tool with agent_family=claude,
   telemetry_opt_in=true, non_interactive=true.
2. Run the InitApply MCP tool with core_only=true.
3. Read the AutonomousRun tool's UsageHint so you understand
   the loop contract.
4. Print a 3-line summary of what was set up.

Begin.
```

The prompt deliberately leans on the MCP tool names the
operator opted in to via `clawtool onboard` — listing them by
name lets the agent skip the usual "let me poke around to see
what's available" warmup.

## Test seam

`internal/cli/bootstrap.go` uses two package-level vars as
test seams:

- `lookPath` — mirrors `exec.LookPath`. Tests rebind via
  `t.Cleanup` to control the missing-binary branch.
- `spawnAgent` — the actual subprocess spawn. Tests rebind to
  capture argv + prompt without ever forking a real binary.

The container e2e test (`autodev/e2e-bootstrap-container`
branch) verifies the zero-click flow end-to-end against a fresh
distro image.

## Cross-references

- `docs/mcp-authoring.md` — the MCP tools the bootstrap prompt
  asks the agent to call.
- `docs/autonomous.md` — what to run AFTER bootstrap to drive a
  long-running dev loop.
- `internal/cli/bootstrap.go` — implementation.
- `internal/agents/elevation.go` — `agents.ElevationFlag` map
  shared with the relay path.
