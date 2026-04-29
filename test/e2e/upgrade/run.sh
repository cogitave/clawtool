#!/usr/bin/env bash
# test/e2e/upgrade/run.sh — executes inside the e2e container.
# Validates the atomic-binary-swap + `clawtool daemon restart`
# pipeline that `clawtool upgrade` invokes after selfupdate.UpdateTo.
#
# Output is delimited by ==SECTION== markers so the Go harness
# (upgrade_e2e_test.go) can parse stdout deterministically. The
# parser drops anything before the first marker, so build-stage
# noise from the docker layer doesn't pollute assertions.

set -uo pipefail
export XDG_CONFIG_HOME=/tmp/cfg
export XDG_STATE_HOME=/tmp/state
mkdir -p "$XDG_CONFIG_HOME/clawtool" "$XDG_STATE_HOME/clawtool"

step() { printf '→ %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; emit_exit 1; }

read_port()  { jq -r '.port' "$XDG_CONFIG_HOME/clawtool/daemon.json" 2>/dev/null; }
read_token() { tr -d '\n' < "$XDG_CONFIG_HOME/clawtool/listener-token" 2>/dev/null; }

probe_health() {
    local port=$1 token=$2 i out
    for i in $(seq 1 20); do
        if out=$(curl -fsS -H "Authorization: Bearer $token" \
                "http://127.0.0.1:$port/v1/health" 2>&1); then
            printf '%s' "$out"
            return 0
        fi
        sleep 0.3
    done
    return 1
}

EXIT_RC=0
emit_exit() {
    EXIT_RC=$1
    printf '==EXIT==\n%s\n' "$EXIT_RC"
    exit "$EXIT_RC"
}

trap 'emit_exit $?' EXIT

printf '==STDOUT==\n'

step "verify old binary version"
OLDV=$(/usr/local/bin/clawtool --version 2>&1)
echo "old --version: $OLDV"
# version.Resolved() strips a leading `v` from the ldflags-injected
# string, so the binary reports `0.0.0-old` not `v0.0.0-old`.
echo "$OLDV" | grep -q '0.0.0-old' || fail "expected 0.0.0-old, got: $OLDV"

step "start daemon (old binary)"
/usr/local/bin/clawtool daemon start
sleep 1

PORT=$(read_port)
TOKEN=$(read_token)
[ -n "$PORT" ]  || fail "no port in daemon.json"
[ -n "$TOKEN" ] || fail "no listener-token"
echo "old daemon pid=$(jq -r '.pid' "$XDG_CONFIG_HOME/clawtool/daemon.json") port=$PORT"

step "probe /v1/health → expect 0.0.0-old"
H1=$(probe_health "$PORT" "$TOKEN") || fail "old health unreachable on :$PORT"
echo "old health: $H1"
echo "$H1" | grep -q '0.0.0-old' || fail "old health did not advertise 0.0.0-old"

step "atomic-swap binary to new version"
cp /opt/clawtool-new /usr/local/bin/clawtool.new
mv /usr/local/bin/clawtool.new /usr/local/bin/clawtool
NEWV=$(/usr/local/bin/clawtool --version 2>&1)
echo "post-swap --version: $NEWV"
echo "$NEWV" | grep -q '0.0.0-new' || fail "binary did not swap"

step "daemon restart (Stop + Ensure on the NEW binary)"
/usr/local/bin/clawtool daemon restart
sleep 1

PORT2=$(read_port)
TOKEN2=$(read_token)
[ -n "$PORT2" ] || fail "no port in daemon.json after restart"
echo "new daemon pid=$(jq -r '.pid' "$XDG_CONFIG_HOME/clawtool/daemon.json") port=$PORT2"

step "probe /v1/health → expect 0.0.0-new"
H2=$(probe_health "$PORT2" "$TOKEN2") || fail "new health unreachable on :$PORT2"
echo "new health: $H2"
echo "$H2" | grep -q '0.0.0-new' || fail "post-restart health did not advertise 0.0.0-new"

step "PASS — upgrade flow validated end-to-end"
emit_exit 0
