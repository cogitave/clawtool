#!/usr/bin/env bash
# test/e2e/ideator/run.sh — scripted bash e2e for the Ideator
# (top of the three-layer self-direction stack).
#
# Drives the canonical chain end-to-end:
#
#   ideate (read-only) → ideate --apply (queue at proposed)
#                      → autopilot accept (operator gate)
#                      → autopilot next (claim)
#
# No Docker — local binary against an isolated $XDG_CONFIG_HOME +
# a tmp repo with hand-crafted ADR + TODO seed signals.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
BIN="$REPO_ROOT/bin/clawtool"

if [[ ! -x "$BIN" ]]; then
  echo "building bin/clawtool ..."
  (cd "$REPO_ROOT" && go build -o bin/clawtool ./cmd/clawtool)
fi

XDG_TMP="$(mktemp -d)"
SEED_REPO="$(mktemp -d)"
trap 'rm -rf "$XDG_TMP" "$SEED_REPO"' EXIT
export XDG_CONFIG_HOME="$XDG_TMP"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

# ── 0. seed the test repo with one TODO and one ADR open question ──
mkdir -p "$SEED_REPO/wiki/decisions"
cat > "$SEED_REPO/foo.go" <<'GO'
package fake

// TODO: wire up the auth rotation
func Foo() {}
GO

cat > "$SEED_REPO/wiki/decisions/099-test.md" <<'MD'
# 099 Test ADR

Status: accepted

## Decision

Synthetic ADR for the ideator e2e.

## Open questions

- How do we expire stale proposals?
MD

# ── 1. ideate (read-only) lists ideas ─────────────────────────
echo "▶ ideate (read-only)"
read_out="$("$BIN" ideate --repo "$SEED_REPO" --format json)"
echo "$read_out" | grep -q '"ideas"' \
  || fail "read-only ideate missing 'ideas' key: $read_out"
echo "$read_out" | grep -q '"todos"' \
  || fail "read-only ideate missing todos source: $read_out"
echo "$read_out" | grep -q '"adr_questions"' \
  || fail "read-only ideate missing adr_questions source: $read_out"
pass "ideate read-only surfaces TODOs + ADR open questions"

# Confirm autopilot still empty (read-only path must not write).
status_json="$("$BIN" autopilot status --format json)"
echo "$status_json" | grep -q '"total": 0' \
  || fail "read-only ideate dirtied the queue: $status_json"
pass "queue still empty after read-only ideate"

# ── 2. ideate --apply pushes to autopilot at status=proposed ──
echo "▶ ideate --apply"
apply_out="$("$BIN" ideate --repo "$SEED_REPO" --apply --format json)"
added="$(echo "$apply_out" | grep -o '"added": *[0-9]*' | head -1 | grep -o '[0-9]*')"
[[ -n "$added" && "$added" -ge 1 ]] \
  || fail "ideate --apply added 0 ideas: $apply_out"
pass "ideate --apply queued $added idea(s)"

# Re-run is idempotent — DedupeKey collisions become Skipped.
echo "▶ ideate --apply (idempotent)"
apply2="$("$BIN" ideate --repo "$SEED_REPO" --apply --format json)"
added2="$(echo "$apply2" | grep -o '"added": *[0-9]*' | head -1 | grep -o '[0-9]*')"
[[ "$added2" == "0" ]] \
  || fail "second ideate --apply added $added2 ideas, want 0 (dedupe broke)"
pass "second ideate --apply was a no-op"

# ── 3. autopilot list --status proposed shows the queued ideas ──
echo "▶ autopilot list --status proposed"
proposed_json="$("$BIN" autopilot list --status proposed --format json)"
echo "$proposed_json" | grep -q '"status": "proposed"' \
  || fail "no proposed items: $proposed_json"
pass "queue shows proposed items"

# Pull the first proposed id for the accept step.
prop_id="$(echo "$proposed_json" | grep -o '"id": *"[^"]*"' | head -1 | sed 's/"id": *"\(.*\)"/\1/')"
[[ -n "$prop_id" ]] || fail "could not parse proposed id from: $proposed_json"
echo "proposed id: $prop_id"

# ── 4. autopilot next is silent on a proposed-only queue ─────
echo "▶ autopilot next (proposed-only)"
empty_json="$("$BIN" autopilot next --format json | tr -d '[:space:]')"
[[ "$empty_json" == "{}" ]] \
  || fail "next claimed a proposed item: $empty_json"
pass "next ignored proposed items (operator gate intact)"

# ── 5. autopilot accept flips proposed → pending ─────────────
echo "▶ autopilot accept"
accept_out="$("$BIN" autopilot accept "$prop_id" --note "operator approved")"
echo "$accept_out" | grep -q "$prop_id accepted" \
  || fail "accept did not confirm: $accept_out"
pass "accept → $prop_id"

# ── 6. autopilot next now claims the formerly-proposed item ──
echo "▶ autopilot next (post-accept)"
claim_json="$("$BIN" autopilot next --format json)"
echo "$claim_json" | grep -q "\"id\": \"$prop_id\"" \
  || fail "next did not return accepted id $prop_id: $claim_json"
echo "$claim_json" | grep -q '"status": "in_progress"' \
  || fail "next did not mark in_progress: $claim_json"
pass "next claimed accepted id $prop_id"

# ── 7. autopilot done closes the chain ───────────────────────
echo "▶ autopilot done"
"$BIN" autopilot done "$prop_id" --note "ideator e2e: closed" \
  | grep -q "$prop_id done" || fail "done did not confirm"
pass "done → $prop_id"

echo
echo "✓ ideator e2e: ideate → accept → next → done chain works"
