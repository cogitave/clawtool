#!/usr/bin/env bash
# release-notes-rich.test.sh — smoke-tests for release-notes-rich.sh.
#
# Runs the script against the last 3 tags in this repo and asserts the
# rendered body has all the structural pieces:
#   - tag header
#   - 🚀 Features section
#   - at least one scoped sub-header
#   - install snippet
#   - stats footer
#
# Run via:  bash scripts/release-notes-rich.test.sh
#
# Exits 0 on pass, 1 on any failed assertion. Prints which assertion
# tripped + a tail of the rendered output so failures are easy to debug.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${SCRIPT_DIR}/release-notes-rich.sh"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [ ! -r "$TARGET" ]; then
  echo "FAIL: ${TARGET} not found" >&2
  exit 1
fi

FROM_TAG="v0.22.71"
TO_TAG="v0.22.73"

# Skip cleanly if the test fixtures (tags) don't exist locally — that
# happens when running on a shallow clone in CI before fetch-depth=0.
if ! git rev-parse "$FROM_TAG" >/dev/null 2>&1 || ! git rev-parse "$TO_TAG" >/dev/null 2>&1; then
  echo "skip: ${FROM_TAG} or ${TO_TAG} not present (shallow clone?)"
  exit 0
fi

OUT="$(bash "$TARGET" --from "$FROM_TAG" --to "$TO_TAG")"

fail() {
  echo "FAIL: $1" >&2
  echo "--- output (last 30 lines) ---" >&2
  printf '%s\n' "$OUT" | tail -30 >&2
  exit 1
}

# Assertion 1: tag header
echo "$OUT" | grep -qF "## clawtool ${TO_TAG}" \
  || fail "missing '## clawtool ${TO_TAG}' header"

# Assertion 2: features section
echo "$OUT" | grep -qF "🚀 Features" \
  || fail "missing '🚀 Features' section"

# Assertion 3: at least one of the scoped sub-headers
if ! echo "$OUT" | grep -qE "📦 catalog|🤖 agents|🖥️ cli|🍳 recipes|🛡️ rules|🛠️ tools|🌉 portal|📓 playbooks|⚙️ setup"; then
  fail "no scoped sub-header (📦/🤖/🖥️/🍳/🛡️/🛠️/🌉/📓/⚙️) found"
fi

# Assertion 4: install snippet preserved
echo "$OUT" | grep -qF "Install (user-local, no sudo)" \
  || fail "missing install snippet"
echo "$OUT" | grep -qF "clawtool_${TO_TAG#v}_linux_amd64.tar.gz" \
  || fail "install snippet missing version-specific tarball name"

# Assertion 5: stats footer
echo "$OUT" | grep -qE "[0-9]+ commits" \
  || fail "missing 'N commits' stats line"

# Assertion 6: at least one commit-link bullet (- subj ([`abc1234`](url))).
# Use fgrep on the unique fragment "/commit/" which only appears in
# commit-link bullets — avoids escaping backticks/brackets in regex.
echo "$OUT" | grep -qF "github.com/cogitave/clawtool/commit/" \
  || fail "no commit-link bullet found in output"

# Assertion 7: --help works
bash "$TARGET" --help >/dev/null \
  || fail "--help exited non-zero"

echo "ok: release-notes-rich.sh — 7/7 assertions passed for ${FROM_TAG}..${TO_TAG}"
