#!/usr/bin/env bash
# test/e2e/upgrade/long_running.sh — alternative entrypoint for the
# upgrade e2e container when we want to model "user has clawtool
# running, keeps the container open, runs upgrade against it."
#
# Differs from run.sh in one important way: instead of running the
# entire harness in-process and exiting, this script starts the
# daemon and then BLOCKS so the host can drive the upgrade from
# outside via `docker exec`. The container therefore stays in
# Docker Desktop's running list — operator visibility is the
# whole point of this entrypoint.
#
# Once the host-side test is done it can either:
#   - leave the container running (operator inspects state in
#     Desktop), and clean up later via `docker rm -f <name>`
#   - call `docker stop <name>` if it wants the daemon's SIGTERM
#     handler exercised
#
# The container's stdout is the daemon's lifecycle markers; the
# host test scrapes them via `docker logs` to know when the
# daemon is ready.

set -uo pipefail
export XDG_CONFIG_HOME=/tmp/cfg
export XDG_STATE_HOME=/tmp/state
mkdir -p "$XDG_CONFIG_HOME/clawtool" "$XDG_STATE_HOME/clawtool"

emit() { printf '%s\n' "$*"; }

emit "LIVE_CONTAINER_BOOT"
INITIAL_VERSION=$(/usr/local/bin/clawtool --version 2>&1 | head -1)
emit "INITIAL_VERSION: $INITIAL_VERSION"

emit "DAEMON_STARTING"
/usr/local/bin/clawtool daemon start
sleep 1
DSF="$XDG_CONFIG_HOME/clawtool/daemon.json"
if [ ! -f "$DSF" ]; then
    emit "DAEMON_FAILED_TO_START"
    exit 2
fi

# Surface state so the host can read it back via `docker logs`
# without exec'ing a jq.
PORT=$(grep -oP '"port":\s*\K[0-9]+' "$DSF" 2>/dev/null)
PID=$(grep -oP '"pid":\s*\K[0-9]+' "$DSF" 2>/dev/null)
emit "DAEMON_READY pid=$PID port=$PORT"
emit "BLOCKING_FOR_DOCKER_EXEC"

# Block forever — host drives via `docker exec`. Trap SIGTERM so
# `docker stop` cleanly stops the daemon (exercises the daemon's
# own SIGTERM handler instead of process-group SIGKILL).
trap 'emit "RECEIVED_SIGTERM"; /usr/local/bin/clawtool daemon stop || true; exit 0' TERM
tail -f /dev/null &
TAIL_PID=$!
wait "$TAIL_PID"
