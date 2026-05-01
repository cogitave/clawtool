#!/usr/bin/env bash
# test/e2e/fullstack/run.sh — end-to-end driver for the fullstack
# Docker harness. Exercises the full v0.22.95–v0.22.106 surface
# inside a container with REAL processes (no stubs on the clawtool
# side; the LLM agents themselves are bash stubs because the goal is
# to validate transport, not language-model output).
#
# Flow (each step prints a banner so failures localise instantly):
#
#   1. clawtool --version            — binary-on-PATH sanity
#   2. clawtool daemon start         — listener up + state file written
#   3. tmux new-session -d           — host the stub agent panes
#   4. spawn N stub agent panes      — one per family (claude / codex /
#                                      gemini / opencode); each registers
#                                      itself via `clawtool peer register`
#                                      with --tmux-pane=<its pane id>
#   5. clawtool peer list            — assert >= 4 peers visible
#   6. clawtool peer send --name codex-stub "FROM_E2E_TEST_<ts>"
#                                    — exercises the daemon inbox write
#                                      AND the additive tmux send-keys
#                                      push (the load-bearing v0.22.x
#                                      contract).
#   7. sleep 3                       — let the codex-stub's read loop
#                                      flush stdin into its log.
#   8. cat /tmp/codex-stub.last-prompt
#                                    — assert the prompt round-tripped
#                                      end-to-end.
#
# Output is delimited by ==SECTION== markers so the Go harness
# (fullstack_e2e_test.go) can split stdout deterministically. Each
# section corresponds to one step above so a single bad assertion
# points straight at the broken step.
#
# Determinism: every sleep is bounded; every tmux invocation runs
# under -L <socket> with a unique socket name so a stale tmux server
# from a prior container can't haunt this run; XDG paths are
# isolated per the Dockerfile so peers.d is empty at start.

set -uo pipefail

STAMP="$(date +%s)"
PROMPT="FROM_E2E_TEST_${STAMP}"
TMUX_SESSION="claw-fullstack"
# IMPORTANT: clawtool's internal tmux helpers (peer_tmux_push.go)
# call `tmux list-panes` / `tmux send-keys` WITHOUT a `-L <socket>`
# flag, which means they hit the DEFAULT tmux socket. The harness
# must run on the same socket so peer-send's send-keys finds the
# pane it's trying to push to. (Earlier versions of this harness
# used `-L claw-e2e` and the symptom was: peer send returned 0,
# inbox got the message, but the codex-stub's marker stayed empty
# because the tmux push went to a different server.)
TMUX_FLAGS=""

mkdir -p "$XDG_CONFIG_HOME/clawtool" "$XDG_STATE_HOME/clawtool"

emit_section() { printf '==%s==\n' "$1"; }

# ─── 1. binary sanity ─────────────────────────────────────────────
emit_section "BINARY_VERSION"
clawtool --version || true

# ─── 2. daemon start ──────────────────────────────────────────────
emit_section "DAEMON_START"
daemon_rc=0
clawtool daemon start || daemon_rc=$?
echo "rc=$daemon_rc"

emit_section "DAEMON_STATUS"
clawtool daemon status || true

# ─── 3. tmux session ──────────────────────────────────────────────
emit_section "TMUX_NEW_SESSION"
# -L pins a socket name so we don't collide with any other tmux
# server on this host (containers normally have none, but the
# realinstall fixture has bitten us with shared sockets in the
# past). -d = detached, -s = session name.
tmux new-session -d -s "$TMUX_SESSION" -n shell "sleep infinity"
tmux list-sessions
echo "rc=$?"

# ─── 4. spawn stub agents ─────────────────────────────────────────
# For each family, open a NEW window in the tmux session running
# the agent_loop.sh stub. After the pane is up we capture its pane
# id from `list-panes` and call `clawtool peer register --tmux-pane
# <id>` so the daemon's registry knows where to deliver tmux pushes.
#
# We use a per-family CLAWTOOL_PEER_SESSION so each register stores
# its peer_id under a separate session key — `peer send --name`
# resolves by display_name, but having distinct session keys lets
# the harness deregister cleanly at the end if it ever needs to.
spawn_stub() {
    local family="$1"
    local stub_name="${family}-stub"
    local window_name="$family"

    emit_section "SPAWN_${family^^}"

    # Spawn the stub in a fresh window. The pane runs `agent_loop.sh
    # <name>` which truncates its log files at startup and then
    # blocks on stdin until tmux send-keys delivers data.
    tmux new-window -t "$TMUX_SESSION" -n "$window_name" \
        "/usr/local/bin/agent_loop.sh ${stub_name}"

    # Tmux assigns the pane id asynchronously; a quick sleep lets
    # `list-panes` see the new pane. 200ms is well under the
    # determinism budget — ~20s for the whole harness.
    sleep 0.2

    local pane_id
    pane_id="$(tmux list-panes -t "${TMUX_SESSION}:${window_name}" \
        -F '#{pane_id}' | head -1)"
    echo "pane_id=$pane_id"

    if [ -z "$pane_id" ]; then
        echo "FAIL: no pane id captured for $family"
        return 1
    fi

    # Register the peer with the daemon. Per-family session key
    # keeps the saved peer_id pointers separate so the harness can
    # interrogate any single one. --tmux-pane wires the daemon's
    # registry up so peer send fires send-keys at the right pane.
    CLAWTOOL_PEER_SESSION="${stub_name}" clawtool peer register \
        --backend "$family" \
        --display-name "${stub_name}" \
        --session "${stub_name}" \
        --tmux-pane "$pane_id" \
        --circle e2e
}

for fam in claude codex gemini opencode; do
    spawn_stub "$fam"
done

# Give every stub's `: > recv.log` startup truncation a moment to
# settle before the first send. Without this the recv.log reads
# below have intermittently caught the file mid-truncate on slower
# CI runners.
sleep 0.5

# ─── 5. peer list ─────────────────────────────────────────────────
emit_section "PEER_LIST"
clawtool peer list --format tsv || true

emit_section "PEER_LIST_COUNT"
# Count data rows (excluding the header) — `peer list --format tsv`
# emits 1 header + N peer rows. tail +2 strips the header.
peer_count=$(clawtool peer list --format tsv 2>/dev/null | tail -n +2 | wc -l | tr -d ' ')
echo "$peer_count"

# ─── 6. peer send to codex-stub ───────────────────────────────────
emit_section "PEER_SEND"
send_rc=0
clawtool peer send --name codex-stub "$PROMPT" || send_rc=$?
echo "rc=$send_rc"
echo "prompt=$PROMPT"

# ─── 7. drain delay ───────────────────────────────────────────────
# Tmux send-keys delivery is async — the codex-stub's read loop has
# to dequeue the line before .last-prompt updates. 3s is generous;
# a healthy run lands in ~50ms.
sleep 3

# ─── 8. assert marker ─────────────────────────────────────────────
emit_section "CODEX_LAST_PROMPT"
if [ -f /tmp/codex-stub.last-prompt ]; then
    cat /tmp/codex-stub.last-prompt
    echo
else
    echo "(missing /tmp/codex-stub.last-prompt)"
fi

emit_section "CODEX_RECV_LOG"
if [ -f /tmp/codex-stub.recv.log ]; then
    cat /tmp/codex-stub.recv.log
else
    echo "(missing /tmp/codex-stub.recv.log)"
fi

# Cross-check: the OTHER stubs' recv.logs must remain empty —
# proves the send was scoped to codex-stub only, not broadcast.
for other in claude gemini opencode; do
    emit_section "${other^^}_RECV_LOG"
    if [ -f "/tmp/${other}-stub.recv.log" ]; then
        cat "/tmp/${other}-stub.recv.log"
    else
        echo "(missing)"
    fi
done

# ─── final assertion ──────────────────────────────────────────────
# Compare the codex-stub's last-prompt marker against the prompt we
# sent. The Go harness can do this assertion too (it reads the
# section); doing it here as well gives `docker run` a meaningful
# exit code when run standalone (no Go test driver).
emit_section "RESULT"
got=""
if [ -f /tmp/codex-stub.last-prompt ]; then
    got="$(cat /tmp/codex-stub.last-prompt)"
fi
if [ "$got" = "$PROMPT" ]; then
    echo "PASS: prompt round-tripped end-to-end"
    final_rc=0
else
    echo "FAIL: expected=$PROMPT got=$got"
    final_rc=1
fi

# ─── cleanup ──────────────────────────────────────────────────────
emit_section "CLEANUP"
clawtool daemon stop 2>&1 || true
tmux kill-server 2>&1 || true
echo "rc=$final_rc"

emit_section "EXIT"
echo "$final_rc"

exit "$final_rc"
