#!/usr/bin/env bash
# End-to-end test for the clawtool MCP server.
#
# Drives the built binary through a real MCP handshake over stdio, then asserts
# both surface (initialize → tools/list) and behavior (tools/call returns the
# expected structured-output JSON for Bash). Exits non-zero on any failure.
#
# MCP wraps tools/call results as text content where the inner structured JSON
# is escaped — `\"stdout\":\"value\"` shows up literally in the wire bytes.
# All assertions on call results use `grep -F` against that escaped form.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$REPO_ROOT/bin/clawtool"

if [[ ! -x "$BIN" ]]; then
  echo "✘ bin/clawtool not found — run 'make build' first" >&2
  exit 1
fi

# ── helpers ──────────────────────────────────────────────────────────────
fail() { echo "✘ $*" >&2; exit 1; }
pass() { echo "✓ $*"; }

mcp_session() {
  timeout 10 "$BIN" serve 2>/dev/null
}

initialize_msg='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0.1"}}}'
initialized_notification='{"jsonrpc":"2.0","method":"notifications/initialized"}'

# ── 1. initialize + tools/list returns Bash ──────────────────────────────
echo "▶ test: initialize + tools/list"
list_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | mcp_session)

echo "$list_response" | grep -q '"name":"clawtool"' \
  || fail "initialize: serverInfo.name != clawtool"
pass "initialize: serverInfo reports clawtool"

echo "$list_response" | grep -q '"name":"Bash"' \
  || fail "tools/list: Bash tool missing"
pass "tools/list: Bash tool registered (PascalCase per ADR-006)"

echo "$list_response" | grep -q '"required":\["command"\]' \
  || fail "tools/list: Bash inputSchema missing required:[command]"
pass "tools/list: Bash inputSchema enforces required:[command]"

# ── 2. tools/call Bash with a clean command ──────────────────────────────
echo ""
echo "▶ test: Bash success path"
call_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Bash","arguments":{"command":"printf clawtool"}}}' \
  | mcp_session)

echo "$call_response" | grep -qF '\"stdout\":\"clawtool\"' \
  || fail "Bash success: stdout != 'clawtool' — got: $call_response"
pass "Bash success: stdout captured exactly"

echo "$call_response" | grep -qF '\"exit_code\":0' \
  || fail "Bash success: exit_code != 0"
pass "Bash success: exit_code == 0"

echo "$call_response" | grep -qF '\"timed_out\":false' \
  || fail "Bash success: timed_out != false"
pass "Bash success: timed_out == false"

# ── 3. tools/call Bash that exits non-zero — output must not be lost ─────
echo ""
echo "▶ test: Bash non-zero exit"
fail_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Bash","arguments":{"command":"echo first; echo bad >&2; exit 7"}}}' \
  | mcp_session)

echo "$fail_response" | grep -qF '\"exit_code\":7' \
  || fail "Bash non-zero: exit_code != 7"
pass "Bash non-zero: exit_code propagated"

echo "$fail_response" | grep -qF '\"stdout\":\"first' \
  || fail "Bash non-zero: stdout dropped"
pass "Bash non-zero: stdout preserved before failure"

echo "$fail_response" | grep -qF '\"stderr\":\"bad' \
  || fail "Bash non-zero: stderr missing"
pass "Bash non-zero: stderr preserved"

# ── 4. tools/call Bash that times out — process group must be reaped ─────
echo ""
echo "▶ test: Bash timeout (ADR-005 headline quality bar)"
to_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Bash","arguments":{"command":"echo before; sleep 5; echo never","timeout_ms":300}}}' \
  | mcp_session)

echo "$to_response" | grep -qF '\"timed_out\":true' \
  || fail "Bash timeout: timed_out != true"
pass "Bash timeout: timed_out == true"

echo "$to_response" | grep -qF '\"stdout\":\"before' \
  || fail "Bash timeout: pre-timeout stdout dropped"
pass "Bash timeout: stdout preserved up to the deadline"

if echo "$to_response" | grep -qF '\"never\"'; then
  fail "Bash timeout: post-timeout output leaked into stdout"
fi
pass "Bash timeout: post-timeout output correctly suppressed"

# Pull duration_ms out of the escaped JSON. The pattern is `\"duration_ms\":NNN`.
duration=$(echo "$to_response" | grep -oE '\\"duration_ms\\":[0-9]+' | head -1 | grep -oE '[0-9]+')
if [[ -z "$duration" ]]; then
  fail "Bash timeout: duration_ms not present in response"
fi
if (( duration > 2000 )); then
  fail "Bash timeout: duration_ms=${duration} too high (runaway child not reaped)"
fi
pass "Bash timeout: returned in ${duration}ms (<2000ms; child reaped)"

# ── 5. tools/list registers Grep and Read ───────────────────────────────
echo ""
echo "▶ test: Grep and Read tools registered"
list2=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | mcp_session)

echo "$list2" | grep -q '"name":"Grep"' \
  || fail "tools/list: Grep tool missing"
pass "tools/list: Grep registered"

echo "$list2" | grep -q '"name":"Read"' \
  || fail "tools/list: Read tool missing"
pass "tools/list: Read registered"

# ── 6. tools/call Grep finds 'clawtool' in the repo's own README ─────────
echo ""
echo "▶ test: Grep call against repo README"
grep_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Grep","arguments":{"pattern":"clawtool","path":"README.md","cwd":"%s"}}}' "$REPO_ROOT")" \
  | mcp_session)

echo "$grep_response" | grep -qF '\"engine\":\"ripgrep\"' \
  || fail "Grep: engine != ripgrep — got: $grep_response"
pass "Grep: engine == ripgrep (preferred when present)"

echo "$grep_response" | grep -qF '\"matches_count\":' \
  || fail "Grep: matches_count missing"
pass "Grep: matches_count present in response"

# At least one match for 'clawtool' in README must be reported.
if ! echo "$grep_response" | grep -qF '\"text\":\"' ; then
  fail "Grep: no matches text in response — got: $grep_response"
fi
pass "Grep: at least one match returned"

# ── 7. tools/call Read returns the README structured correctly ──────────
echo ""
echo "▶ test: Read call against repo README"
read_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Read","arguments":{"path":"README.md","line_start":1,"line_end":3,"cwd":"%s"}}}' "$REPO_ROOT")" \
  | mcp_session)

echo "$read_response" | grep -qF '\"format\":\"text\"' \
  || fail "Read: format != text"
pass "Read: format == text"

echo "$read_response" | grep -qF '\"engine\":\"stdlib\"' \
  || fail "Read: engine != stdlib"
pass "Read: engine == stdlib"

echo "$read_response" | grep -qF '\"line_end\":3' \
  || fail "Read: line_end != 3 (range honored)"
pass "Read: line range honored (line_end=3)"

echo "$read_response" | grep -qF '\"total_lines\":' \
  || fail "Read: total_lines missing"
pass "Read: total_lines reported"

echo "$read_response" | grep -qF 'clawtool' \
  || fail "Read: README content missing"
pass "Read: README content captured"

# ── done ──────────────────────────────────────────────────────────────────
echo ""
echo "✓ all e2e tests passed"
