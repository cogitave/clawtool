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

# Snapshot the codex-stub marker BEFORE Phase 2 runs. Phase 2's
# auto-spawn path opens a fresh pane running `codex` (stub falls
# through to agent_loop.sh codex-stub) which truncates
# /tmp/codex-stub.last-prompt at startup. The final RESULT
# section needs to read the Phase 1 value, not the post-spawn
# empty file — capture it here.
PHASE1_LAST_PROMPT=""
if [ -f /tmp/codex-stub.last-prompt ]; then
    PHASE1_LAST_PROMPT="$(cat /tmp/codex-stub.last-prompt)"
fi

# ─── Phase 2 — lifecycle scenarios (ADR-034 Q1/Q2/Q3) ─────────────
# Phase 1 (sections above) validated transport: peer-register +
# unicast peer send + tmux send-keys land the prompt at the right
# stub. Phase 2 validates the LIFECYCLE the v0.22.106→v0.22.109
# work landed:
#
#   A. Window cleanup — when SendMessage auto-spawns a fresh tmux
#      pane in its own new window, the auto-close hook should kill
#      BOTH the pane AND the (now-empty) window. Asserts on
#      `tmux list-windows`, not just `list-panes`.
#   B. Grace period — peer.auto_close_grace_seconds=N defers the
#      kill by N seconds. Pane stays alive immediately after task
#      done; gone after the grace window elapses.
#   C. Per-task override — opts.auto_close=false on a SendMessage
#      pins the pane open even when the master gate is on.
#
# These three scenarios share a setup phase: deregister all the
# manually-attached peers from Phase 1 so the auto-spawn path's
# FindOnlinePeer miss fires (otherwise we'd just re-route to the
# pre-registered peer and never hit the auto-spawn lifecycle hook).
# The daemon also needs $TMUX set in its env so TmuxAvailable()
# returns true; we restart it under the right env to wire that.

emit_section "LIFECYCLE_SETUP"

# Track Phase 2 outcomes independently so an assertion failure in
# any one scenario surfaces in the final summary instead of being
# masked by Phase 1's PASS.
lifecycle_a_rc=2  # 2 = not run; 0 = pass; 1 = fail
lifecycle_b_rc=2
lifecycle_c_rc=2

# Step 1: deregister every peer so FindOnlinePeer misses and the
# auto-spawn fires. We hit /v1/peers/{id} DELETE directly because
# the stubs registered with disjoint --session keys; iterating the
# CLI would mean threading those keys back here.
DSF="$XDG_CONFIG_HOME/clawtool/daemon.json"
if [ ! -f "$DSF" ]; then
    echo "FAIL: daemon.json missing — cannot drive lifecycle scenarios"
    lifecycle_a_rc=1
    lifecycle_b_rc=1
    lifecycle_c_rc=1
fi

if [ -f "$DSF" ]; then
    DAEMON_PORT="$(jq -r '.port' "$DSF")"
    # The daemon was launched without --token-file (`--no-auth`
    # mode), so the listener-token file may not even exist. Probe
    # for it; if absent, send requests without the header and the
    # listener accepts.
    AUTH_HEADER=""
    if [ -f "$XDG_CONFIG_HOME/clawtool/listener-token" ]; then
        TOKEN="$(tr -d '\n' < "$XDG_CONFIG_HOME/clawtool/listener-token")"
        AUTH_HEADER="Authorization: Bearer $TOKEN"
    fi

    echo "deregistering all Phase 1 peers..."
    PEER_IDS="$(curl -fsS ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
        "http://127.0.0.1:${DAEMON_PORT}/v1/peers" 2>/dev/null \
        | jq -r '.peers // [] | .[].peer_id')"
    for pid in $PEER_IDS; do
        curl -fsS -X DELETE ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
            "http://127.0.0.1:${DAEMON_PORT}/v1/peers/${pid}" >/dev/null 2>&1 || true
    done
    AFTER="$(clawtool peer list --format tsv 2>/dev/null | tail -n +2 | wc -l | tr -d ' ')"
    echo "peers_after_dereg=$AFTER"

    # Step 2: restart the daemon with TMUX env set so the
    # auto-spawn TmuxAvailable() check passes (peer_spawn.go:117
    # reads os.Getenv("TMUX") and falls through to legacy
    # spawn-fresh-subprocess when it's empty).
    #
    # IMPORTANT: tmux's TMUX env var is parsed as
    # `<socket-path>,<server-pid>,<window>` and the CLI dials the
    # named socket. Passing a sentinel like "fullstack-e2e" makes
    # tmux fail with `error connecting to fullstack-e2e (No such
    # file or directory)`. We must point TMUX at the REAL default
    # socket path tmux already created (the harness's tmux server
    # is on `/tmp/tmux-<uid>/default`) — otherwise the spawner's
    # `tmux new-window` call lands on a non-existent server and
    # the auto-spawn fails with exit status 1.
    TMUX_SOCK="/tmp/tmux-$(id -u)/default"
    DAEMON_TMUX_ENV="${TMUX_SOCK},$$,0"
    clawtool daemon stop 2>&1 || true
    sleep 0.3
    TMUX="$DAEMON_TMUX_ENV" clawtool daemon start
    # The daemon's HTTP listener is ready before its dispatch
    # socket on slower runners (the socket is mounted by a
    # post-listen goroutine). Poll until the socket file exists,
    # capped at 5s, so the first lifecycle dispatch lands on a
    # ready daemon rather than ErrNoDispatchSocket.
    for _ in $(seq 1 50); do
        if [ -S "$XDG_STATE_HOME/clawtool/dispatch.sock" ]; then
            break
        fi
        sleep 0.1
    done

    if [ ! -S "$XDG_STATE_HOME/clawtool/dispatch.sock" ]; then
        echo "FAIL: dispatch socket missing after daemon restart"
        lifecycle_a_rc=1
        lifecycle_b_rc=1
        lifecycle_c_rc=1
    fi

    # Re-read DAEMON_PORT post-restart. The old value was the
    # pre-restart daemon's port, which is now dead — using it
    # would silently fail every curl in the lifecycle scenarios
    # below (curl -fsS is quiet on connection refused). The
    # daemon.json was rewritten by `daemon start` so the new
    # port is on disk.
    DAEMON_PORT="$(jq -r '.port' "$DSF")"
fi

# wait_for_dispatch_socket — same wait used after each daemon
# restart inside individual lifecycle scenarios (B and C reload
# config). Returns 0 once the socket exists; 1 after a 5s poll.
wait_for_dispatch_socket() {
    for _ in $(seq 1 50); do
        if [ -S "$XDG_STATE_HOME/clawtool/dispatch.sock" ]; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}

# dispatch_submit ACTION OPTS_JSON PROMPT — pipes a SendMessage
# request through the daemon's Unix dispatch socket. Returns the
# task_id on stdout, or empty + non-zero rc on error. The dispatch
# socket protocol is one JSON request, one JSON response, then
# close — socat's `-,shut-down` half-closes the pipe so the
# daemon's reader unblocks after the request.
dispatch_submit() {
    local instance="$1"; shift
    local opts_json="$1"; shift
    local prompt="$*"
    local req
    req=$(jq -nc --arg i "$instance" --arg p "$prompt" --argjson o "$opts_json" \
        '{action:"submit", instance:$i, prompt:$p, opts:$o}')
    # 5s read deadline matches handleDispatchClient's
    # SetReadDeadline(5s). socat -t1 keeps the pipe open just long
    # enough to read the response after our newline-terminated JSON
    # closes the write side.
    local resp
    resp="$(printf '%s\n' "$req" | socat -t2 - "UNIX-CONNECT:$XDG_STATE_HOME/clawtool/dispatch.sock" 2>/dev/null)"
    if [ -z "$resp" ]; then
        return 1
    fi
    echo "$resp" | jq -r '.task_id // empty'
}

# tmux_window_with_id_exists WID → 0 if a window with the given id
# is still listed under the tmux server, 1 otherwise. Used to
# assert window cleanup AFTER the auto-close hook fires. Empty
# WID returns 1 (treat as absent) so a caller that failed to
# capture a window id doesn't false-positive on grep -Fx "".
tmux_window_with_id_exists() {
    local wid="$1"
    if [ -z "$wid" ]; then return 1; fi
    tmux list-windows -t "$TMUX_SESSION" -F '#{window_id}' 2>/dev/null \
        | grep -Fx "$wid" >/dev/null
}

# tmux_pane_with_id_exists PID → 0 if the pane is still alive
# anywhere in our tmux server, 1 otherwise. The auto-close hook
# fires `tmux kill-pane`; absence in the global pane list is the
# load-bearing assertion. Empty PID returns 1 (treat as absent)
# — same false-positive guard as tmux_window_with_id_exists.
tmux_pane_with_id_exists() {
    local pid="$1"
    if [ -z "$pid" ]; then return 1; fi
    tmux list-panes -a -F '#{pane_id}' 2>/dev/null \
        | grep -Fx "$pid" >/dev/null
}

# auto_spawned_peer_pane FAMILY → echoes "<pane_id> <window_id>"
# for the most-recently-registered auto-spawned peer of FAMILY.
# Empty stdout means no auto-spawned peer of that family exists.
# We read the pane_id from the registry (peer.tmux_pane) rather
# than tmux directly because the registry is the source of truth
# the lifecycle hook also reads.
#
# The registry's GET /v1/peers wire shape is
# `{"peers": [...], "count": N, "as_of": ...}` — the bare-array
# fallback (`.[]?`) used to be valid for an earlier wire shape
# but now causes "Cannot index string with string" errors when
# jq walks the count/as_of strings, so we pin on `.peers[]?`.
auto_spawned_peer_pane() {
    local family="$1"
    # backend disambiguation: claude family registers as
    # `claude-code` (familyToBackend in peer_route.go); pass the
    # backend literally so the filter hits.
    local backend="$family"
    if [ "$family" = "claude" ]; then
        backend="claude-code"
    fi
    curl -fsS ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
        "http://127.0.0.1:${DAEMON_PORT}/v1/peers?backend=${backend}" 2>/dev/null \
        | jq -r --arg b "$backend" \
            '.peers // []
             | map(select(.backend == $b and .metadata.auto_spawned == "true"))
             | sort_by(.last_seen) | reverse
             | .[0] // empty
             | "\(.tmux_pane // "") \(.metadata.tmux_window // "")"'
}

# ─── Lifecycle test A — window cleanup ────────────────────────────
emit_section "LIFECYCLE_TEST_A"

# Pre-condition: dispatch socket present.
if [ ! -S "$XDG_STATE_HOME/clawtool/dispatch.sock" ]; then
    echo "SKIP: no dispatch socket"
    lifecycle_a_rc=1
else
    # Submit a SendMessage to gemini (no live peer of that family
    # — we just deregistered the original gemini-stub). Auto-spawn
    # path opens a NEW tmux window running `gemini --yolo` (the
    # stub falls through to agent_loop.sh). The auto-spawn flow
    # records the pane_id + window_id in the peer's metadata.
    TASK_ID_A="$(dispatch_submit gemini '{"mode":"auto-tmux"}' "lifecycle-test-A-${STAMP}")"
    echo "task_id=$TASK_ID_A"
    if [ -z "$TASK_ID_A" ]; then
        echo "FAIL: dispatch_submit returned no task_id"
        lifecycle_a_rc=1
    else
        # The auto-spawn path is async w.r.t. the registry write
        # (the runner goroutine calls Supervisor.Send which calls
        # tmux new-window). Poll until either the registry shows
        # the auto-spawned peer OR the task hits terminal status —
        # whichever comes first. The auto-spawn writes the peer
        # BEFORE the runner finalises the ack stream, so on a
        # healthy run the peer is visible the moment we observe
        # the task as `done`. 10s budget covers slow Docker
        # runners + the optional close-hook delay.
        PANE_WIN_A=""
        for _ in $(seq 1 100); do
            PANE_WIN_A="$(auto_spawned_peer_pane gemini)"
            if [ -n "$(echo "$PANE_WIN_A" | tr -d ' \t')" ]; then
                break
            fi
            sleep 0.1
        done
        PANE_A="$(echo "$PANE_WIN_A" | awk '{print $1}')"
        WIN_A="$(echo "$PANE_WIN_A" | awk '{print $2}')"
        echo "pane=$PANE_A window=$WIN_A"

        if [ -z "$PANE_A" ]; then
            # Auto-spawn never registered (or the close hook beat
            # our polling to the peer's tmux_pane field). Surface
            # the daemon log, full peer list, and the task's
            # message timeline so the failure mode is obvious.
            echo "FAIL: no auto-spawned peer found for gemini after dispatch"
            echo "--- /v1/peers (full registry) ---"
            curl -fsS ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
                "http://127.0.0.1:${DAEMON_PORT}/v1/peers" 2>/dev/null \
                | sed 's/^/  peers| /' | head -60
            echo "--- daemon.log tail ---"
            tail -30 "$XDG_STATE_HOME/clawtool/daemon.log" 2>/dev/null \
                | sed 's/^/  daemon.log| /' || true
            echo "--- task get ---"
            clawtool task get "$TASK_ID_A" 2>&1 \
                | sed 's/^/  task| /' | tail -40
            lifecycle_a_rc=1
        else
            # Wait for the task to settle. The auto-spawn ack stream
            # is short text; the BIAM runner drains it quickly and
            # transitions to terminal status, which fires the close
            # hook.
            clawtool task wait "$TASK_ID_A" --timeout 30s >/dev/null 2>&1 || true
            # The close hook fires INSIDE the BIAM terminal-status
            # callback, which happens slightly after WaitForTerminal
            # returns (the hook is invoked after status is committed;
            # there's a small async window). 1s budget; poll the pane
            # disappearing rather than sleeping a fixed interval.
            gone=0
            for _ in 1 2 3 4 5 6 7 8 9 10; do
                if ! tmux_pane_with_id_exists "$PANE_A"; then
                    gone=1
                    break
                fi
                sleep 0.1
            done
            if [ "$gone" -ne 1 ]; then
                echo "FAIL: pane $PANE_A still alive after task done"
                lifecycle_a_rc=1
            elif [ -n "$WIN_A" ] && tmux_window_with_id_exists "$WIN_A"; then
                # ADR-034 Q1: an empty window must be reaped. If the
                # auto-spawn placed the only pane in this window, the
                # window itself should be gone.
                echo "FAIL: window $WIN_A still listed in tmux list-windows after pane death"
                tmux list-windows -t "$TMUX_SESSION" 2>&1 || true
                lifecycle_a_rc=1
            else
                echo "PASS: pane + window reaped after auto-close"
                lifecycle_a_rc=0
            fi
        fi
    fi
fi

# ─── Lifecycle test B — grace period ──────────────────────────────
emit_section "LIFECYCLE_TEST_B"

if [ ! -S "$XDG_STATE_HOME/clawtool/dispatch.sock" ]; then
    echo "SKIP: no dispatch socket"
    lifecycle_b_rc=1
else
    # Set peer.auto_close_grace_seconds=3 in config and restart
    # the daemon so the new value lands in
    # agents.SetAutoCloseGraceSeconds.
    cat > "$XDG_CONFIG_HOME/clawtool/config.toml" <<'CFG'
[peer]
auto_close_grace_seconds = 3
CFG
    clawtool daemon stop 2>&1 || true
    sleep 0.3
    TMUX="$DAEMON_TMUX_ENV" clawtool daemon start
    wait_for_dispatch_socket || echo "WARN: dispatch socket slow to come up"
    DAEMON_PORT="$(jq -r '.port' "$DSF")"

    TASK_ID_B="$(dispatch_submit codex '{"mode":"auto-tmux"}' "lifecycle-test-B-${STAMP}")"
    echo "task_id=$TASK_ID_B"
    if [ -z "$TASK_ID_B" ]; then
        echo "FAIL: dispatch_submit returned no task_id"
        lifecycle_b_rc=1
    else
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            PANE_WIN_B="$(auto_spawned_peer_pane codex)"
            if [ -n "$PANE_WIN_B" ]; then break; fi
            sleep 0.1
        done
        PANE_B="$(echo "$PANE_WIN_B" | awk '{print $1}')"
        echo "pane=$PANE_B"

        clawtool task wait "$TASK_ID_B" --timeout 30s >/dev/null 2>&1 || true
        # Immediately after task done: pane MUST still be alive.
        # The grace timer is scheduled via time.AfterFunc and the
        # 3-second window hasn't elapsed yet. Anything <500ms in
        # is the live-pane region we're asserting on.
        if ! tmux_pane_with_id_exists "$PANE_B"; then
            echo "FAIL: pane $PANE_B closed BEFORE grace window elapsed"
            lifecycle_b_rc=1
        else
            echo "pane alive immediately after task done (in grace window) — good"
            # Sleep through the grace window. 3s grace + 1.5s
            # margin so the time.AfterFunc has fired and the kill
            # propagated. Then the pane must be gone.
            sleep 4.5
            if tmux_pane_with_id_exists "$PANE_B"; then
                echo "FAIL: pane $PANE_B still alive after grace window elapsed"
                lifecycle_b_rc=1
            else
                echo "PASS: pane survived grace, then was reaped"
                lifecycle_b_rc=0
            fi
        fi
    fi
fi

# ─── Lifecycle test C — per-task auto_close=false override ─────────
emit_section "LIFECYCLE_TEST_C"

if [ ! -S "$XDG_STATE_HOME/clawtool/dispatch.sock" ]; then
    echo "SKIP: no dispatch socket"
    lifecycle_c_rc=1
else
    # Reset config so grace is back to 0; we want the IMMEDIATE
    # close path to be in effect, then prove the per-task
    # `auto_close=false` opt overrides it.
    : > "$XDG_CONFIG_HOME/clawtool/config.toml"
    clawtool daemon stop 2>&1 || true
    sleep 0.3
    TMUX="$DAEMON_TMUX_ENV" clawtool daemon start
    wait_for_dispatch_socket || echo "WARN: dispatch socket slow to come up"
    DAEMON_PORT="$(jq -r '.port' "$DSF")"

    # Submit with auto_close=false. Even with the master gate
    # on (default) and grace=0 (so the immediate-kill code path
    # runs), tryPeerRoute should skip LinkTaskToPeer when the opt
    # is false, so MaybeAutoClosePane finds no row to close.
    TASK_ID_C="$(dispatch_submit opencode \
        '{"mode":"auto-tmux","auto_close":false}' \
        "lifecycle-test-C-${STAMP}")"
    echo "task_id=$TASK_ID_C"
    if [ -z "$TASK_ID_C" ]; then
        echo "FAIL: dispatch_submit returned no task_id"
        lifecycle_c_rc=1
    else
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            PANE_WIN_C="$(auto_spawned_peer_pane opencode)"
            if [ -n "$PANE_WIN_C" ]; then break; fi
            sleep 0.1
        done
        PANE_C="$(echo "$PANE_WIN_C" | awk '{print $1}')"
        echo "pane=$PANE_C"

        clawtool task wait "$TASK_ID_C" --timeout 30s >/dev/null 2>&1 || true
        # Generous post-terminal wait so any (incorrectly-fired)
        # close hook has had time to land. Then assert the pane
        # is STILL alive — the override worked.
        sleep 1
        if tmux_pane_with_id_exists "$PANE_C"; then
            echo "PASS: pane preserved across task done (auto_close=false honoured)"
            lifecycle_c_rc=0
        else
            echo "FAIL: pane $PANE_C closed despite auto_close=false"
            lifecycle_c_rc=1
        fi
        # Manual cleanup so the container teardown stays tidy:
        # the brief explicitly calls for `peer deregister` +
        # `tmux kill-pane` here. Best-effort — failures don't
        # mask the assertion result above.
        if [ -n "$PANE_C" ]; then
            tmux kill-pane -t "$PANE_C" 2>/dev/null || true
        fi
    fi
fi

emit_section "LIFECYCLE_SUMMARY"
echo "A=$lifecycle_a_rc B=$lifecycle_b_rc C=$lifecycle_c_rc"

# ─── final assertion ──────────────────────────────────────────────
# Compare the codex-stub's last-prompt marker against the prompt we
# sent. The Go harness can do this assertion too (it reads the
# section); doing it here as well gives `docker run` a meaningful
# exit code when run standalone (no Go test driver).
emit_section "RESULT"
# Phase 1 marker is snapshotted into PHASE1_LAST_PROMPT before
# Phase 2 starts (Phase 2's auto-spawn truncates the file when a
# fresh codex-stub pane comes up). Fall back to the on-disk file
# for backwards compatibility if the snapshot somehow missed.
got="$PHASE1_LAST_PROMPT"
if [ -z "$got" ] && [ -f /tmp/codex-stub.last-prompt ]; then
    got="$(cat /tmp/codex-stub.last-prompt)"
fi
phase1_rc=1
if [ "$got" = "$PROMPT" ]; then
    echo "PASS: prompt round-tripped end-to-end"
    phase1_rc=0
else
    echo "FAIL: expected=$PROMPT got=$got"
fi

# Phase 2 result — fold every lifecycle scenario into one rc. A
# scenario that didn't run (rc=2) is treated as a fail because the
# Phase 2 surface is now mandatory in v0.22.109+.
phase2_rc=0
for rc in "$lifecycle_a_rc" "$lifecycle_b_rc" "$lifecycle_c_rc"; do
    if [ "$rc" != "0" ]; then
        phase2_rc=1
    fi
done

if [ "$phase1_rc" = "0" ] && [ "$phase2_rc" = "0" ]; then
    final_rc=0
    echo "PHASE2_PASS: lifecycle window-cleanup + grace + per-task override"
else
    final_rc=1
    echo "PHASE2_FAIL: lifecycle phase2_rc=$phase2_rc phase1_rc=$phase1_rc"
fi

# ─── cleanup ──────────────────────────────────────────────────────
emit_section "CLEANUP"
clawtool daemon stop 2>&1 || true
tmux kill-server 2>&1 || true
echo "rc=$final_rc"

emit_section "EXIT"
echo "$final_rc"

exit "$final_rc"
