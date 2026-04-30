#!/usr/bin/env bash
# test/e2e/bootstrap/run.sh — drives `clawtool bootstrap --agent
# claude` against a stub claude binary inside the container.
#
# Output is delimited by ==SECTION== markers so the Go harness
# (bootstrap_e2e_test.go) can split stdout deterministically:
#   ==STDOUT==           — bootstrap stdout
#   ==STDERR==           — bootstrap stderr
#   ==EXIT==             — bootstrap exit code
#   ==VERB_PRESENT==     — "yes" / "no" (probe result)
#   ==STUB_INVOCATIONS== — contents of /tmp/claude.invocations
#   ==STUB_STDIN==       — contents of /tmp/claude.stdin

set -uo pipefail

mkdir -p "$XDG_CONFIG_HOME/clawtool" "$XDG_STATE_HOME/clawtool"

# Probe whether the bootstrap verb actually exists on this commit.
# The parallel branch (autodev/bootstrap-claude-init) lands the
# verb; until it merges, this fixture skips cleanly. We use
# `--help` because every cobra-style verb implements it; absence
# means the verb subcommand isn't registered.
verb_present=no
if clawtool bootstrap --help >/dev/null 2>&1; then
    verb_present=yes
fi

stdout_file=$(mktemp)
stderr_file=$(mktemp)
trap 'rm -f "$stdout_file" "$stderr_file"' EXIT

rc=0
if [ "$verb_present" = "yes" ]; then
    set +e
    clawtool bootstrap --agent claude >"$stdout_file" 2>"$stderr_file"
    rc=$?
    set -e
else
    # Verb missing — emit a placeholder so the section parser still
    # has something to read. The Go driver checks VERB_PRESENT
    # first and t.Skip()'s before asserting on these.
    echo "(bootstrap verb not present on this commit; skipping invocation)" >"$stdout_file"
fi

echo "==STDOUT=="
cat "$stdout_file"
echo "==STDERR=="
cat "$stderr_file"
echo "==EXIT=="
echo "$rc"
echo "==VERB_PRESENT=="
echo "$verb_present"
echo "==STUB_INVOCATIONS=="
if [ -f /tmp/claude.invocations ]; then
    cat /tmp/claude.invocations
else
    echo "(none)"
fi
echo "==STUB_STDIN=="
if [ -f /tmp/claude.stdin ]; then
    cat /tmp/claude.stdin
else
    echo "(none)"
fi

# Always exit 0 from run.sh itself; the harness reads the EXIT
# section to learn the bootstrap rc. Returning the bootstrap rc
# here would conflate "stub didn't get called" with "harness is
# broken" on the Go side.
exit 0
