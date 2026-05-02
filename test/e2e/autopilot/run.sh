#!/usr/bin/env bash
# test/e2e/autopilot/run.sh — scripted bash e2e for the autopilot
# self-direction backlog primitive.
#
# Drives the built binary through the canonical agent loop:
#
#   add → next → done  → next-empty exits silent
#
# No Docker — just the local binary against an isolated
# $XDG_CONFIG_HOME tmpdir. Verifies CLI behaviour the way an agent
# (or autodev cron) would chain the verbs from a shell.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
BIN="$REPO_ROOT/bin/clawtool"

if [[ ! -x "$BIN" ]]; then
  echo "building bin/clawtool ..."
  (cd "$REPO_ROOT" && go build -o bin/clawtool ./cmd/clawtool)
fi

# Isolated config root so the developer's own queue is untouched.
XDG_TMP="$(mktemp -d)"
trap 'rm -rf "$XDG_TMP"' EXIT
export XDG_CONFIG_HOME="$XDG_TMP"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

# ── 1. add an item ────────────────────────────────────────────
echo "▶ add"
id="$("$BIN" autopilot add "ship the autopilot primitive" --priority 5 | tr -d '\n')"
[[ -n "$id" ]] || fail "add did not print an id"
[[ "$id" == ap-* ]] || fail "add returned malformed id: $id"
pass "add → $id"

# ── 2. status reflects the new item ──────────────────────────
echo "▶ status (after add)"
status_json="$("$BIN" autopilot status --format json)"
echo "$status_json" | grep -q '"pending": 1' \
  || fail "status missing pending=1: $status_json"
pass "status: pending=1"

# ── 3. next claims the item ──────────────────────────────────
echo "▶ next"
claim_json="$("$BIN" autopilot next --format json)"
echo "$claim_json" | grep -q "\"id\": \"$id\"" \
  || fail "next did not return claimed id $id; got: $claim_json"
echo "$claim_json" | grep -q '"status": "in_progress"' \
  || fail "next did not mark in_progress: $claim_json"
pass "next → $id (in_progress)"

# ── 4. status reflects the in-progress claim ─────────────────
echo "▶ status (after claim)"
status_json="$("$BIN" autopilot status --format json)"
echo "$status_json" | grep -q '"in_progress": 1' \
  || fail "status missing in_progress=1: $status_json"
pass "status: in_progress=1"

# ── 5. done closes the item ──────────────────────────────────
echo "▶ done"
done_out="$("$BIN" autopilot done "$id" --note "merged into main")"
echo "$done_out" | grep -q "$id done" \
  || fail "done did not confirm: $done_out"
pass "done → $id"

# ── 6. next on empty queue is silent (text) and {} (json) ────
echo "▶ next (drained, text)"
empty_text="$("$BIN" autopilot next || true)"
[[ -z "$(echo -n "$empty_text" | tr -d '[:space:]')" ]] \
  || fail "next on drained queue wrote: $empty_text"
pass "next-empty: silent (text)"

echo "▶ next (drained, json)"
empty_json="$("$BIN" autopilot next --format json | tr -d '[:space:]')"
[[ "$empty_json" == "{}" ]] \
  || fail "next-empty json got '$empty_json', want '{}'"
pass "next-empty: '{}' (json)"

# ── 7. final status: pending=0, done=1 ───────────────────────
echo "▶ status (final)"
final_status="$("$BIN" autopilot status --format json)"
echo "$final_status" | grep -q '"pending": 0' \
  || fail "final status missing pending=0: $final_status"
echo "$final_status" | grep -q '"done": 1' \
  || fail "final status missing done=1: $final_status"
pass "final status: pending=0 done=1"

echo
echo "✓ autopilot e2e: every verb exercised, agent loop drains cleanly"
