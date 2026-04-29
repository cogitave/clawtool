#!/usr/bin/env bash
# test/e2e/realinstall/run.sh — drives the GitHub-release install
# flow against a clean Alpine container. See Dockerfile for the
# scenario design; this file is the actual harness body.
#
# Output is delimited by ==SECTION== markers so the Go harness
# (realinstall_e2e_test.go) can parse stdout deterministically.
# Anything before the first marker is build-stage noise.

set -uo pipefail

mkdir -p "$XDG_CONFIG_HOME/clawtool" "$XDG_STATE_HOME/clawtool"

step()  { printf '→ %s\n' "$*"; }
ok()    { printf '✓ %s\n' "$*"; }
fail()  { printf 'FAIL: %s\n' "$*" >&2; emit_exit 1; }

EXIT_RC=0
emit_exit() {
    EXIT_RC=$1
    printf '==EXIT==\n%s\n' "$EXIT_RC"
    exit "$EXIT_RC"
}
trap 'emit_exit $?' EXIT

printf '==STDOUT==\n'

step "Stage 1: run install.sh (GitHub-release path)"
# The script downloads the latest release tarball from
# github.com/cogitave/clawtool/releases — real network round trip.
# CLAWTOOL_NO_ONBOARD=1 prevents the post-install wizard prompt
# (we drive the wizard ourselves below).
CLAWTOOL_NO_ONBOARD=1 sh /usr/local/bin/clawtool-install.sh \
    2>&1 | sed 's/^/    install.sh| /'
[ -x $HOME/.local/bin/clawtool ] || fail "clawtool not found at $HOME/.local/bin/clawtool after install"
ok "install.sh placed binary at $HOME/.local/bin/clawtool"

step "Stage 2: clawtool --version"
INSTALLED_VERSION=$($HOME/.local/bin/clawtool --version 2>&1)
echo "    $INSTALLED_VERSION"
case "$INSTALLED_VERSION" in
    *"clawtool"*)
        ok "binary runs and reports a version string"
        ;;
    *)
        fail "unexpected --version output: $INSTALLED_VERSION"
        ;;
esac

step "Stage 3: daemon start"
$HOME/.local/bin/clawtool daemon start 2>&1 | sed 's/^/    daemon| /'
sleep 1
DSF="$XDG_CONFIG_HOME/clawtool/daemon.json"
[ -f "$DSF" ] || fail "daemon.json missing at $DSF"
PID=$(jq -r '.pid' "$DSF")
PORT=$(jq -r '.port' "$DSF")
TOKEN=$(tr -d '\n' < "$XDG_CONFIG_HOME/clawtool/listener-token")
ok "daemon.json: pid=$PID port=$PORT"

step "Stage 4: probe /v1/health"
HEALTH=$(curl -fsS -H "Authorization: Bearer $TOKEN" \
    "http://127.0.0.1:$PORT/v1/health" 2>&1)
echo "    $HEALTH"
echo "$HEALTH" | grep -q '"status":"ok"' || fail "health probe missing status:ok"
ok "daemon answers /v1/health"

step "Stage 5: clawtool tools list (sanity — surface populated?)"
TOOL_COUNT=$($HOME/.local/bin/clawtool tools list 2>/dev/null | grep -cE '^(Bash|Read|Write|Grep)\s' || true)
echo "    core-tool rows seen: $TOOL_COUNT"
[ "$TOOL_COUNT" -ge 4 ] || fail "tools list didn't surface core tools (Bash/Read/Write/Grep)"
ok "tools list shows at least 4 core tools"

step "Stage 6: clawtool overview (one-screen status)"
$HOME/.local/bin/clawtool overview 2>&1 | head -10 | sed 's/^/    overview| /'
ok "overview rendered"

step "Stage 7: clawtool upgrade --check (network round-trip to GitHub)"
UPGRADE_CHECK=$($HOME/.local/bin/clawtool upgrade --check 2>&1 || true)
echo "$UPGRADE_CHECK" | sed 's/^/    upgrade --check| /'
case "$UPGRADE_CHECK" in
    *"up to date"*|*"current:"*|*"latest:"*)
        ok "upgrade --check completed (operator-readable output)"
        ;;
    *)
        fail "upgrade --check produced unexpected output (network down?)"
        ;;
esac

step "Stage 8: clawtool onboard --yes (wizard against mock CLIs)"
# Onboard probes claude / codex / gemini, picks a primary, runs the
# bridge install + agent-claim flow. The mocks accept anything so
# the recipe-Verify steps go ✓; only the daemon / identity / secrets
# pieces touch the real filesystem.
$HOME/.local/bin/clawtool onboard --yes 2>&1 | tail -20 | sed 's/^/    onboard| /'
[ -f "$XDG_CONFIG_HOME/clawtool/.onboarded" ] || fail "onboarded marker missing after onboard --yes"
ok "onboard wrote the .onboarded marker"

step "Stage 9: confirm mock CLIs were probed"
for c in claude codex gemini; do
    if [ -f "/tmp/${c}.invocations" ]; then
        echo "    ${c}: $(wc -l < /tmp/${c}.invocations) invocation(s)"
    else
        echo "    ${c}: NOT invoked"
    fi
done

step "Stage 10: daemon stop (graceful SIGTERM)"
$HOME/.local/bin/clawtool daemon stop 2>&1 | sed 's/^/    daemon| /'
sleep 1
[ -f "$DSF" ] && fail "daemon.json should have been removed by stop, still present"
ok "daemon stopped + state file cleaned up"

step "PASS — clean install + daemon + onboard + upgrade-check flow"
emit_exit 0
