#!/usr/bin/env bash
# test/e2e/fullstack/stub_agents/agent_loop.sh — generic stub agent
# script. Stands in for `claude` / `codex` / `gemini` / `opencode`
# inside the fullstack e2e container so the harness can exercise the
# end-to-end peer-send → tmux send-keys → recipient receive pipeline
# without needing a real LLM.
#
# Each stub:
#   - reads stdin line-by-line in a `while read line` loop
#   - appends every received line to /tmp/<name>.recv.log
#   - rewrites /tmp/<name>.last-prompt with the most recent line
#     (truncating; this is the marker the harness asserts on)
#   - echoes a small ack to stdout so the host's tmux pane shows
#     activity (helpful for debugging — irrelevant to assertions)
#
# The script runs forever (until killed by the harness) so a tmux
# pane spawned with `tmux new-window -- agent_loop.sh codex-stub`
# stays alive and keeps draining the prompt stream the way a real
# agent CLI would.
#
# First positional argv = stub name (e.g. codex-stub). Used for the
# log path AND the agent banner so a multi-pane tmux session shows
# which pane is which.

set -uo pipefail

name="${1:-stub}"
recv_log="/tmp/${name}.recv.log"
last_prompt="/tmp/${name}.last-prompt"

# Truncate logs at startup so a re-run inside the same container
# starts clean. The harness reads these files immediately after a
# send, so stale content from a prior tmux pane would false-positive
# the assertion.
: > "$recv_log"
: > "$last_prompt"

echo "[${name}] ready — pid=$$ pane=${TMUX_PANE:-none}"

# The read loop intentionally has NO timeout: the pane is bound to
# tmux's stdin, which only delivers data when `tmux send-keys` fires
# at this pane. A real agent CLI behaves the same way.
#
# tmuxSendKeys (peer_tmux_push.go) drives a 3-step sequence: literal
# text → Escape → Enter. The Escape step injects a literal ESC byte
# (0x1b) into our stdin BEFORE the Enter terminates the line, so a
# raw `read` here surfaces "<text>\x1b" — we strip the ESC bytes
# and any other ANSI control characters before recording so the
# marker file matches the prompt the harness sent verbatim.
strip_ansi() {
    # Remove ESC + ANSI CSI sequences. The send-keys Escape we
    # see in practice is just a bare 0x1b with nothing after it,
    # but stripping the full CSI shape keeps us safe if a future
    # tmuxSendKeys revision adds a key with a real ANSI sequence.
    printf '%s' "$1" | sed -E $'s/\x1b\\[[0-9;]*[a-zA-Z]//g; s/\x1b//g'
}

while IFS= read -r line; do
    # Filter empty lines: tmux's send-keys appends a literal Enter
    # after the payload, which surfaces as an extra empty read here.
    # Skipping keeps the .last-prompt marker tied to actual content.
    if [ -z "$line" ]; then
        continue
    fi
    cleaned="$(strip_ansi "$line")"
    if [ -z "$cleaned" ]; then
        continue
    fi
    printf '%s\n' "$cleaned" >> "$recv_log"
    printf '%s' "$cleaned" > "$last_prompt"
    # Echo back so the operator watching the tmux pane sees the
    # round-trip; harness ignores stdout, only asserts on the files.
    echo "[${name}] received: $cleaned"
done

# Clean shutdown when stdin closes (tmux pane gone or harness killed
# the process). Logged so a `docker logs` after the run shows the
# stub exited gracefully rather than crashed.
echo "[${name}] stdin closed — exiting"
