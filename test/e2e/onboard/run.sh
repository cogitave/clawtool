#!/usr/bin/env bash
# test/e2e/onboard/run.sh — entrypoint for the onboard e2e container.
#
# Drives `clawtool onboard --yes` against a fixture host (claude /
# codex / gemini mocks on PATH), captures stdout + stderr + exit
# code, dumps the resulting state on the way out so the Go test
# wrapper can assert against deterministic JSON-ish output.
#
# Output sections (each prefixed `==<NAME>==` so the test can split):
#   ==STDOUT==     — onboard wizard stdout
#   ==STDERR==     — onboard wizard stderr
#   ==EXIT==       — onboard exit code
#   ==MARKER==     — contents of ~/.config/clawtool/.onboarded (or "ABSENT")
#   ==MCP_LIST==   — `clawtool mcp` not relevant; instead emit the
#                    invocations log from each mock CLI so we can see
#                    what onboard attempted.
#   ==MOCK_LOGS==  — concatenation of /tmp/<name>.invocations files
set -euo pipefail

# Sanity: clawtool must be on PATH or in a known location.
if ! command -v clawtool >/dev/null 2>&1; then
    echo "::error:: clawtool binary missing from PATH" >&2
    exit 127
fi

stdout_file=$(mktemp)
stderr_file=$(mktemp)
trap 'rm -f "$stdout_file" "$stderr_file"' EXIT

set +e
clawtool onboard --yes >"$stdout_file" 2>"$stderr_file"
rc=$?
set -e

echo "==STDOUT=="
cat "$stdout_file"
echo "==STDERR=="
cat "$stderr_file"
echo "==EXIT=="
echo "$rc"

echo "==MARKER=="
marker="${XDG_CONFIG_HOME:-$HOME/.config}/clawtool/.onboarded"
if [ -f "$marker" ]; then
    cat "$marker"
else
    echo "ABSENT"
fi

echo "==MOCK_LOGS=="
for log in /tmp/claude.invocations /tmp/codex.invocations /tmp/gemini.invocations; do
    if [ -f "$log" ]; then
        echo "--- $(basename "$log") ---"
        cat "$log"
    fi
done

# Final exit reflects onboard's exit. The harness inspects the
# section markers, so a non-zero rc here surfaces as a test
# failure with the full stdout/stderr captured above.
exit "$rc"
