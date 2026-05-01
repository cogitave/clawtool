# Fullstack E2E (`test/e2e/fullstack`)

End-to-end Docker harness exercising the v0.22.95–v0.22.106 surface
with REAL processes — clawtool daemon, tmux server, peer registry,
and `peer send` send-keys delivery. Only the LLM agents themselves
are stubbed (bash scripts), because the goal is to validate
transport, not language-model output.

## What it tests

1. The clawtool binary builds + runs in a clean Ubuntu 24.04 container.
2. `clawtool daemon start` brings the listener up; state file written.
3. A real `tmux` server hosts one pane per stub agent
   (claude / codex / gemini / opencode).
4. `clawtool peer register --tmux-pane <id>` populates the daemon's
   peer registry — `peer list` reports four peers.
5. `clawtool peer send --name codex-stub "<prompt>"` writes to the
   daemon inbox AND drives `tmux send-keys` at the codex-stub pane.
6. The codex-stub's read-loop receives the prompt and writes a
   marker file (`/tmp/codex-stub.last-prompt`).
7. The harness asserts the marker matches the prompt verbatim.
8. Cross-stub isolation: claude / gemini / opencode recv-logs stay
   empty, proving the send was unicast (not broadcast).

## The stub-agent pattern

`stub_agents/agent_loop.sh` is a generic stub that stands in for
any agent CLI. It:

- reads stdin line-by-line in a `while IFS= read -r line` loop;
- appends every received line to `/tmp/<name>.recv.log`;
- truncates `/tmp/<name>.last-prompt` and writes the latest line;
- echoes a small ack to stdout (so `tmux capture-pane` shows
  activity for debugging);
- runs forever — the pane stays alive until the harness kills it.

Each `claude` / `codex` / `gemini` / `opencode` stub on PATH is a
two-line shell wrapper: handle `--version` probes, then `exec`
into `agent_loop.sh "<family>-stub"`. This means any clawtool code
path that probes for the binary (`clawtool agents detect`, etc.)
finds the stub, and any spawn into a tmux pane lands in the read
loop.

## Running locally

Container build + run (the fast path):

```sh
cd <repo-root>
docker build -f test/e2e/fullstack/Dockerfile -t clawtool-e2e-fullstack:dev .
docker run --rm clawtool-e2e-fullstack:dev
```

Exit code 0 + a final `RESULT: PASS` line in stdout = the prompt
round-tripped end-to-end.

Through the Go test driver (the lane CI uses):

```sh
CLAWTOOL_E2E_DOCKER=1 go test -tags=e2e -count=1 -timeout=600s \
    ./test/e2e/fullstack/...
```

Or via the umbrella CI script (runs alongside the other docker
fixtures):

```sh
CLAWTOOL_E2E_DOCKER=1 bash scripts/ci.sh
```

## Files

| Path | Purpose |
|---|---|
| `Dockerfile` | ubuntu:24.04 base, builds clawtool, installs tmux + node |
| `run.sh` | in-container test driver; emits `==SECTION==`-delimited output |
| `stub_agents/agent_loop.sh` | generic stub agent (stdin → marker file) |
| `fullstack_e2e_test.go` | Go driver (`+build e2e`); builds image, runs container, asserts |

## Section markers

`run.sh` emits its progress as `==NAME==` headers so the Go driver
can split stdout deterministically. Names: `BINARY_VERSION`,
`DAEMON_START`, `DAEMON_STATUS`, `TMUX_NEW_SESSION`, `SPAWN_*`,
`PEER_LIST`, `PEER_LIST_COUNT`, `PEER_SEND`, `CODEX_LAST_PROMPT`,
`CODEX_RECV_LOG`, `*_RECV_LOG`, `RESULT`, `EXIT`. Add a section
when adding a new step — the parser auto-picks it up.
