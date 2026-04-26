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

for t in Glob ToolSearch; do
  if ! echo "$list_response" | grep -q "\"name\":\"$t\""; then
    fail "tools/list: $t missing"
  fi
  pass "tools/list: $t registered"
done

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

# ── 8. Source proxy: spawn stub-server, aggregate stub__echo, route call ─
echo ""
echo "▶ test: source proxy via stub-server"

# Build a temp config that wires the stub-server as a source.
TMPCFG=$(mktemp -d)
trap 'rm -rf "$TMPCFG"' EXIT
mkdir -p "$TMPCFG/clawtool"
STUB="$REPO_ROOT/test/e2e/stub-server/stub-server"
if [[ ! -x "$STUB" ]]; then
  fail "stub-server not built; run 'make stub-server' first"
fi

cat > "$TMPCFG/clawtool/config.toml" <<EOF
[core_tools]
[core_tools.Bash]
enabled = true
[core_tools.Grep]
enabled = true
[core_tools.Read]
enabled = true

[sources.stub]
type = "mcp"
command = ["$STUB"]

[profile]
active = "default"
EOF

# 8a. tools/list with stub configured must include stub__echo alongside cores
list_with_proxy=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

echo "$list_with_proxy" | grep -q '"name":"Bash"' \
  || fail "proxy: core Bash missing from tools/list"
pass "proxy: core Bash still present alongside source tools"

echo "$list_with_proxy" | grep -q '"name":"stub__echo"' \
  || fail "proxy: stub__echo not aggregated — got: $list_with_proxy"
pass "proxy: stub__echo aggregated under wire-form name (ADR-006)"

# 8b. Wire-form name parsing: clawtool exposes 'stub__echo' (two underscores)
echo "$list_with_proxy" | grep -qE '"name":"stub_echo"' \
  && fail "proxy: tool wire-name uses single underscore (ADR-006 requires __)"
pass "proxy: wire-name uses double underscore separator"

# 8c. tools/call stub__echo must route to the child and return its response
call_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stub__echo","arguments":{"text":"e2e-proxy"}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

echo "$call_response" | grep -qF 'echo:e2e-proxy' \
  || fail "proxy: tools/call did not return echoed text — got: $call_response"
pass "proxy: tools/call routed to child and child's response returned"

# 8d. Disabled core tool stays out of tools/list
cat > "$TMPCFG/clawtool/config.toml" <<EOF
[core_tools]
[core_tools.Bash]
enabled = false
[core_tools.Grep]
enabled = true
[core_tools.Read]
enabled = true

[sources.stub]
type = "mcp"
command = ["$STUB"]

[profile]
active = "default"
EOF

list_no_bash=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

if echo "$list_no_bash" | grep -q '"name":"Bash"' ; then
  fail "proxy: Bash present despite core_tools.Bash.enabled=false"
fi
pass "proxy: disabled core tool correctly absent from tools/list"

echo "$list_no_bash" | grep -q '"name":"stub__echo"' \
  || fail "proxy: stub__echo missing when Bash disabled"
pass "proxy: source tool unaffected by core-tool disable"

# ── 9. ToolSearch ranks the right tool for a semantic query ─────────────
echo ""
echo "▶ test: ToolSearch semantic ranking"

# Restore full config for the search test.
cat > "$TMPCFG/clawtool/config.toml" <<EOF
[core_tools]
[core_tools.Bash]
enabled = true
[core_tools.Grep]
enabled = true
[core_tools.Read]
enabled = true
[core_tools.Glob]
enabled = true
[core_tools.ToolSearch]
enabled = true

[sources.stub]
type = "mcp"
command = ["$STUB"]
EOF

# 9a. Query for grep-shaped intent → Grep should rank first.
search_grep=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ToolSearch","arguments":{"query":"search file contents regex","limit":3}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

echo "$search_grep" | grep -qF '\"engine\":\"bleve-bm25\"' \
  || fail "ToolSearch: engine != bleve-bm25"
pass "ToolSearch: engine == bleve-bm25"

# Top hit must be Grep — its name appears first inside the results array.
top_name=$(echo "$search_grep" | grep -oE '\\"name\\":\\"[A-Za-z_]+\\"' | head -1 | grep -oE '[A-Za-z_]+' | tail -1)
if [[ "$top_name" != "Grep" ]]; then
  fail "ToolSearch: top hit for 'search file contents regex' = $top_name, want Grep"
fi
pass "ToolSearch: top hit for grep-shaped query is Grep"

# 9b. Query semantically matching the stub source → stub__echo should top.
search_stub=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ToolSearch","arguments":{"query":"echo back input text","limit":3}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

top_name=$(echo "$search_stub" | grep -oE '\\"name\\":\\"[A-Za-z_]+\\"' | head -1 | grep -oE '[A-Za-z_]+' | tail -1)
if [[ "$top_name" != "stub__echo" ]]; then
  fail "ToolSearch: top hit for 'echo back input' = $top_name, want stub__echo (sourced)"
fi
pass "ToolSearch: top hit for echo-shaped query is stub__echo (sourced tool)"

# 9c. type=core filter excludes sourced tools.
search_core=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ToolSearch","arguments":{"query":"echo","type":"core","limit":5}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

if echo "$search_core" | grep -qF '\"name\":\"stub__echo\"' ; then
  fail "ToolSearch type=core: leaked sourced tool stub__echo"
fi
pass "ToolSearch: type=core filter excludes sourced tools"

# ── 10. Glob returns the repo's Markdown files ──────────────────────────
echo ""
echo "▶ test: Glob"
glob_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Glob","arguments":{"pattern":"**/*.md","cwd":"%s","limit":50}}}' "$REPO_ROOT")" \
  | XDG_CONFIG_HOME="$TMPCFG" timeout 15 "$BIN" serve 2>/dev/null)

echo "$glob_resp" | grep -qF '\"engine\":\"doublestar\"' \
  || fail "Glob: engine != doublestar"
pass "Glob: engine == doublestar"

echo "$glob_resp" | grep -qF 'README.md' \
  || fail "Glob: README.md not in matches"
pass "Glob: README.md found via **/*.md"

echo "$glob_resp" | grep -qF '\"matches_count\":' \
  || fail "Glob: matches_count missing"
pass "Glob: matches_count present"

# ── done ──────────────────────────────────────────────────────────────────
echo ""
echo "✓ all e2e tests passed"
