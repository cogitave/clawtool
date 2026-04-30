#!/usr/bin/env bash
# End-to-end test for the clawtool MCP server.
#
# Drives the built binary through a real MCP handshake over stdio, then asserts
# both surface (initialize → tools/list) and behavior (tools/call returns the
# expected structured-output JSON for Bash). Exits non-zero on any failure.
#
# MCP wraps tools/call results as text content where the inner structured JSON
# is escaped — `"stdout":"value"` shows up literally in the wire bytes.
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

# `timeout` is in GNU coreutils on Linux but absent from macOS's BSD
# userland; coreutils-via-brew installs it as `gtimeout`. Resolve once
# at script start so every later invocation can use $TIMEOUT_BIN.
if command -v timeout >/dev/null 2>&1; then
  TIMEOUT_BIN=timeout
elif command -v gtimeout >/dev/null 2>&1; then
  TIMEOUT_BIN=gtimeout
else
  echo "✘ neither 'timeout' nor 'gtimeout' on PATH — install GNU coreutils" >&2
  exit 1
fi

mcp_session() {
  "$TIMEOUT_BIN" 10 "$BIN" serve 2>/dev/null
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

grep -q '"name":"clawtool"' <<<"$list_response" \
  || fail "initialize: serverInfo.name != clawtool"
pass "initialize: serverInfo reports clawtool"

grep -q '"name":"Bash"' <<<"$list_response" \
  || fail "tools/list: Bash tool missing"
pass "tools/list: Bash tool registered (PascalCase per ADR-006)"

for t in Glob ToolSearch WebFetch WebSearch Edit Write SendMessage AgentList BridgeList BridgeAdd BridgeRemove BridgeUpgrade Verify SemanticSearch TaskGet TaskWait TaskList; do
  if ! grep -q "\"name\":\"$t\"" <<<"$list_response"; then
    fail "tools/list: $t missing"
  fi
  pass "tools/list: $t registered"
done

grep -q '"required":\["command"\]' <<<"$list_response" \
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

grep -qF '"stdout":"clawtool"' <<<"$call_response" \
  || fail "Bash success: stdout != 'clawtool' — got: $call_response"
pass "Bash success: stdout captured exactly"

grep -qF '"exit_code":0' <<<"$call_response" \
  || fail "Bash success: exit_code != 0"
pass "Bash success: exit_code == 0"

grep -qF '"timed_out":false' <<<"$call_response" \
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

grep -qF '"exit_code":7' <<<"$fail_response" \
  || fail "Bash non-zero: exit_code != 7"
pass "Bash non-zero: exit_code propagated"

grep -qF '"stdout":"first' <<<"$fail_response" \
  || fail "Bash non-zero: stdout dropped"
pass "Bash non-zero: stdout preserved before failure"

grep -qF '"stderr":"bad' <<<"$fail_response" \
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

grep -qF '"timed_out":true' <<<"$to_response" \
  || fail "Bash timeout: timed_out != true"
pass "Bash timeout: timed_out == true"

grep -qF '"stdout":"before' <<<"$to_response" \
  || fail "Bash timeout: pre-timeout stdout dropped"
pass "Bash timeout: stdout preserved up to the deadline"

if grep -qF '"never"' <<<"$to_response"; then
  fail "Bash timeout: post-timeout output leaked into stdout"
fi
pass "Bash timeout: post-timeout output correctly suppressed"

# Pull duration_ms out of structuredContent. The pattern is `"duration_ms":NNN`.
duration=$(echo "$to_response" | grep -oE '"duration_ms":[0-9]+' | head -1 | grep -oE '[0-9]+')
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

grep -q '"name":"Grep"' <<<"$list2" \
  || fail "tools/list: Grep tool missing"
pass "tools/list: Grep registered"

grep -q '"name":"Read"' <<<"$list2" \
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

grep -qF '"engine":"ripgrep"' <<<"$grep_response" \
  || fail "Grep: engine != ripgrep — got: $grep_response"
pass "Grep: engine == ripgrep (preferred when present)"

grep -qF '"matches_count":' <<<"$grep_response" \
  || fail "Grep: matches_count missing"
pass "Grep: matches_count present in response"

# At least one match for 'clawtool' in README must be reported.
if ! grep -qF '"text":"' <<<"$grep_response" ; then
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

grep -qF '"format":"text"' <<<"$read_response" \
  || fail "Read: format != text"
pass "Read: format == text"

grep -qF '"engine":"stdlib"' <<<"$read_response" \
  || fail "Read: engine != stdlib"
pass "Read: engine == stdlib"

grep -qF '"line_end":3' <<<"$read_response" \
  || fail "Read: line_end != 3 (range honored)"
pass "Read: line range honored (line_end=3)"

grep -qF '"total_lines":' <<<"$read_response" \
  || fail "Read: total_lines missing"
pass "Read: total_lines reported"

grep -qF 'clawtool' <<<"$read_response" \
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
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

grep -q '"name":"Bash"' <<<"$list_with_proxy" \
  || fail "proxy: core Bash missing from tools/list"
pass "proxy: core Bash still present alongside source tools"

grep -q '"name":"stub__echo"' <<<"$list_with_proxy" \
  || fail "proxy: stub__echo not aggregated — got: $list_with_proxy"
pass "proxy: stub__echo aggregated under wire-form name (ADR-006)"

# 8b. Wire-form name parsing: clawtool exposes 'stub__echo' (two underscores)
grep -qE '"name":"stub_echo"' <<<"$list_with_proxy" \
  && fail "proxy: tool wire-name uses single underscore (ADR-006 requires __)"
pass "proxy: wire-name uses double underscore separator"

# 8c. tools/call stub__echo must route to the child and return its response
call_response=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stub__echo","arguments":{"text":"e2e-proxy"}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

grep -qF 'echo:e2e-proxy' <<<"$call_response" \
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
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

if grep -q '"name":"Bash"' <<<"$list_no_bash" ; then
  fail "proxy: Bash present despite core_tools.Bash.enabled=false"
fi
pass "proxy: disabled core tool correctly absent from tools/list"

grep -q '"name":"stub__echo"' <<<"$list_no_bash" \
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
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

grep -qF '"engine":"bleve-bm25"' <<<"$search_grep" \
  || fail "ToolSearch: engine != bleve-bm25"
pass "ToolSearch: engine == bleve-bm25"

# Top hit must be Grep — its name appears first inside the results array.
# structuredContent is JSON in the tools/call response only — drop
# the initialize response so its serverInfo.name doesn't shadow the
# real top hit.
top_name=$(echo "$search_grep" | grep structuredContent | grep -oE '"name":"[A-Za-z_]+"' | head -1 | grep -oE '[A-Za-z_]+' | tail -1)
if [[ "$top_name" != "Grep" ]]; then
  fail "ToolSearch: top hit for 'search file contents regex' = $top_name, want Grep"
fi
pass "ToolSearch: top hit for grep-shaped query is Grep"

# 9b. Query semantically matching the stub source → stub__echo should top.
search_stub=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ToolSearch","arguments":{"query":"echo back input text","limit":3}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

top_name=$(echo "$search_stub" | grep structuredContent | grep -oE '"name":"[A-Za-z_]+"' | head -1 | grep -oE '[A-Za-z_]+' | tail -1)
if [[ "$top_name" != "stub__echo" ]]; then
  fail "ToolSearch: top hit for 'echo back input' = $top_name, want stub__echo (sourced)"
fi
pass "ToolSearch: top hit for echo-shaped query is stub__echo (sourced tool)"

# 9c. type=core filter excludes sourced tools.
search_core=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ToolSearch","arguments":{"query":"echo","type":"core","limit":5}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

if grep -qF '"name":"stub__echo"' <<<"$search_core" ; then
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
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 15 "$BIN" serve 2>/dev/null)

grep -qE '"engine":"doublestar(\+git-ls-files)?"' <<<"$glob_resp" \
  || fail "Glob: engine != doublestar(+git-ls-files)"
pass "Glob: engine matches doublestar variant (with optional git-ls-files suffix when cwd is a worktree, ADR-021 phase B)"

grep -qF 'README.md' <<<"$glob_resp" \
  || fail "Glob: README.md not in matches"
pass "Glob: README.md found via **/*.md"

grep -qF '"matches_count":' <<<"$glob_resp" \
  || fail "Glob: matches_count missing"
pass "Glob: matches_count present"

# ── 11. Read multi-format coverage (HTML + CSV) ─────────────────────────
echo ""
echo "▶ test: Read multi-format (HTML + CSV)"

HTMLFX="$TMPCFG/page.html"
cat > "$HTMLFX" <<'EOF'
<!DOCTYPE html>
<html><head><title>E2E Article</title></head>
<body>
<header><nav>Home | About | Login | Subscribe Now</nav></header>
<article>
<h1>E2E Article</h1>
<p>This article body contains real prose so the readability extractor has
enough textual signal to identify it as the main content. Multiple
sentences are required for the algorithm to score this region above the
surrounding chrome.</p>
<p>A second paragraph reinforces the article's content density.</p>
</article>
<footer>(c) 2026 Example</footer>
</body></html>
EOF

html_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Read","arguments":{"path":"%s"}}}' "$HTMLFX")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"format":"html"' <<<"$html_resp" \
  || fail "Read HTML: format != html — got: $html_resp"
pass "Read HTML: format == html"

grep -qF '"engine":"go-readability"' <<<"$html_resp" \
  || fail "Read HTML: engine != go-readability"
pass "Read HTML: engine == go-readability"

grep -qF 'readability extractor' <<<"$html_resp" \
  || fail "Read HTML: article body missing"
pass "Read HTML: article body preserved"

# Nav clutter must be stripped.
if grep -qF 'Subscribe Now' <<<"$html_resp"; then
  fail "Read HTML: nav clutter leaked through (Subscribe Now)"
fi
pass "Read HTML: nav clutter correctly stripped"

# CSV
CSVFX="$TMPCFG/data.csv"
printf 'name,city,score\nalpha,Istanbul,42\nbravo,Berlin,17\n' > "$CSVFX"
csv_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Read","arguments":{"path":"%s"}}}' "$CSVFX")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"format":"csv"' <<<"$csv_resp" \
  || fail "Read CSV: format != csv"
pass "Read CSV: format == csv"

grep -qF '"engine":"csv-stdlib"' <<<"$csv_resp" \
  || fail "Read CSV: engine != csv-stdlib"
pass "Read CSV: engine == csv-stdlib"

grep -qF 'columns (3): name | city | score' <<<"$csv_resp" \
  || fail "Read CSV: header preview missing"
pass "Read CSV: header preview rendered"

grep -qF 'alpha | Istanbul | 42' <<<"$csv_resp" \
  || fail "Read CSV: data row missing"
pass "Read CSV: data row rendered"

# ── 12. WebFetch input validation (no live network required) ────────────
echo ""
echo "▶ test: WebFetch + WebSearch input/error paths"

webfetch_bad=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"WebFetch","arguments":{"url":"ftp://example.com/file"}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF 'http://' <<<"$webfetch_bad" \
  || fail "WebFetch: error_reason missing scheme hint"
pass "WebFetch: rejects non-http(s) scheme with structured reason"

# 12b. WebSearch without API key surfaces helpful error mentioning BRAVE_API_KEY
websearch_noauth=$(env -u BRAVE_API_KEY printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"WebSearch","arguments":{"query":"go programming"}}}' \
  | env -u BRAVE_API_KEY XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF 'BRAVE_API_KEY' <<<"$websearch_noauth" \
  || fail "WebSearch: missing-key error should mention BRAVE_API_KEY"
pass "WebSearch: missing-key error guides user to BRAVE_API_KEY"

# ── 13. Edit + Write end-to-end via real MCP stdio ──────────────────────
echo ""
echo "▶ test: Edit + Write end-to-end"

WFILE="$TMPCFG/wtest.txt"
write_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Write","arguments":{"path":"%s","content":"hello\\nworld\\n"}}}' "$WFILE")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"created":true' <<<"$write_resp" \
  || fail "Write: created flag missing/false on fresh file"
pass "Write: created==true on fresh file"

[[ -f "$WFILE" ]] || fail "Write: target file not created on disk"
got_w=$(cat "$WFILE")
[[ "$got_w" == $'hello\nworld' ]] || fail "Write: file content unexpected: $(printf '%q' "$got_w")"
pass "Write: file content matches request"

edit_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Edit","arguments":{"path":"%s","old_string":"hello","new_string":"HOWDY"}}}' "$WFILE")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"replaced":true' <<<"$edit_resp" \
  || fail "Edit: replaced flag missing/false"
pass "Edit: replaced==true after substitution"

got_e=$(cat "$WFILE")
[[ "$got_e" == $'HOWDY\nworld' ]] || fail "Edit: file content unexpected: $(printf '%q' "$got_e")"
pass "Edit: substitution applied to file content"

# Ambiguous match must refuse without --replace_all-equivalent flag.
echo "dup line" >> "$WFILE"
echo "dup line" >> "$WFILE"
ambig_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Edit","arguments":{"path":"%s","old_string":"dup line","new_string":"X"}}}' "$WFILE")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF 'appears 2 times' <<<"$ambig_resp" \
  || fail "Edit: should refuse ambiguous match — got: $ambig_resp"
pass "Edit: refuses ambiguous match (suggests replace_all)"

# ── 14. Recipe* MCP tools (v0.9 surface) ─────────────────────────────────
echo ""
echo "▶ test: Recipe* MCP tools"

# 14a. tools/list registers all three Recipe tools.
recipe_list_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

for t in RecipeList RecipeStatus RecipeApply SkillNew; do
  grep -q "\"name\":\"$t\"" <<<"$recipe_list_resp" \
    || fail "tools/list: $t missing"
  pass "tools/list: $t registered"
done

# 14b. RecipeList returns the v0.9 recipe set with category labels.
list_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"RecipeList","arguments":{}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

# Recipe names live inside structuredContent — same parse trick as
# the ToolSearch tests (§9): scope to the structuredContent line so
# JSONRPC envelope's serverInfo.name doesn't leak into the match.
recipe_payload=$(echo "$list_resp" | grep structuredContent)
for r in conventional-commits-ci license codeowners dependabot release-please goreleaser agent-claim brain gh-actions-test prettier golangci-lint devcontainer caveman superclaude claude-flow codex-bridge gemini-bridge opencode-bridge clawtool-relay; do
  grep -qF "\"name\":\"$r\"" <<<"$recipe_payload" \
    || fail "RecipeList: recipe $r missing"
done
pass "RecipeList: all v0.11 recipes present (incl. ADR-014 bridges + clawtool-relay runtime)"

# Category strings are part of the v1.0 contract — every category
# now has at least one recipe, so all 9 must surface.
for c in governance commits release ci quality supply-chain knowledge agents runtime; do
  grep -qF "\"category\":\"$c\"" <<<"$recipe_payload" \
    || fail "RecipeList: category $c missing"
done
pass "RecipeList: all 9 categories surfaced (the v1.0 taxonomy contract)"

# 14c. RecipeStatus on a single recipe in a tempdir reports absent.
RECIPE_TMP=$(mktemp -d)
trap 'rm -rf "$TMPCFG" "$RECIPE_TMP"' EXIT

status_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"RecipeStatus","arguments":{"name":"conventional-commits-ci","repo":"%s"}}}' "$RECIPE_TMP")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"status":"absent"' <<<"$(grep structuredContent <<<"$status_resp")" \
  || fail "RecipeStatus: empty tempdir should report status=absent — got: $status_resp"
pass "RecipeStatus: empty tempdir → status=absent for conventional-commits-ci"

# 14d. RecipeApply against the tempdir writes the workflow + reports verify_ok.
apply_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"RecipeApply","arguments":{"name":"conventional-commits-ci","repo":"%s"}}}' "$RECIPE_TMP")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"verify_ok":true' <<<"$(grep structuredContent <<<"$apply_resp")" \
  || fail "RecipeApply: verify_ok != true — got: $apply_resp"
pass "RecipeApply: verify_ok=true after applying conventional-commits-ci"

[[ -f "$RECIPE_TMP/.github/workflows/commit-format.yml" ]] \
  || fail "RecipeApply: workflow file not written"
pass "RecipeApply: .github/workflows/commit-format.yml present on disk"

grep -q "managed-by: clawtool" "$RECIPE_TMP/.github/workflows/commit-format.yml" \
  || fail "RecipeApply: marker missing in written file"
pass "RecipeApply: clawtool marker stamped in the workflow file"

# 14e. RecipeStatus after Apply reports applied.
status2_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"RecipeStatus","arguments":{"name":"conventional-commits-ci","repo":"%s"}}}' "$RECIPE_TMP")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"status":"applied"' <<<"$(grep structuredContent <<<"$status2_resp")" \
  || fail "RecipeStatus: post-Apply status != applied"
pass "RecipeStatus: post-Apply status=applied"

# 14f. RecipeApply with an unknown name surfaces an actionable error.
bad_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"RecipeApply","arguments":{"name":"not-a-real-recipe","repo":"%s"}}}' "$RECIPE_TMP")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF "unknown recipe" <<<"$bad_resp" \
  || fail "RecipeApply: unknown name should surface 'unknown recipe' message"
pass "RecipeApply: unknown name yields actionable error"

# ── 15. Bridge*/Agent* MCP tools (v0.10 surface, ADR-014 Phase 1) ────────
echo ""
echo "▶ test: Bridge* + Agent* MCP tools"

# 15a. BridgeList enumerates the 3 bridge families with status.
bridge_list_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"BridgeList","arguments":{}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

bridge_payload=$(echo "$bridge_list_resp" | grep structuredContent)
for fam in codex opencode gemini; do
  grep -qF "\"family\":\"$fam\"" <<<"$bridge_payload" \
    || fail "BridgeList: family $fam missing"
done
pass "BridgeList: codex+opencode+gemini families present"

# 15b. BridgeAdd with an unknown family surfaces a structured error.
bad_bridge=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"BridgeAdd","arguments":{"family":"ghost"}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF "unknown family" <<<"$bad_bridge" \
  || fail "BridgeAdd: unknown family should surface 'unknown family' error"
pass "BridgeAdd: unknown family yields actionable error"

# 15c. AgentList returns a structured registry snapshot. The supervisor
# synthesises one default per transport family even with no bridges
# installed (status=bridge-missing for absent binaries), so the
# response always carries a non-empty agents array.
agent_list_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"AgentList","arguments":{}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qF '"agents":' <<<"$(grep structuredContent <<<"$agent_list_resp")" \
  || fail "AgentList: structuredContent should carry an agents array"
pass "AgentList: structured snapshot returned"

# 15d. SendMessage without an agent + no callable instances surfaces a
# clean error rather than blocking. Validates the supervisor's
# resolution path under MCP.
send_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"SendMessage","arguments":{"prompt":"hello","agent":"ghost-instance"}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qE "not found|no callable|not callable|bridge add" <<<"$send_resp" \
  || fail "SendMessage: ghost instance should surface a resolution / bridge-missing error — got: $send_resp"
pass "SendMessage: actionable error when target unreachable"

# 15e. SendMessage with an unknown tag surfaces 'no callable instance carries tag' (ADR-014 Phase 4).
tag_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"SendMessage","arguments":{"prompt":"hi","tag":"non-existent-tag"}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 10 "$BIN" serve 2>/dev/null)

grep -qE "carries tag|no callable" <<<"$tag_resp" \
  || fail "SendMessage tag-routed: unknown tag should surface 'no callable instance carries tag' — got: $tag_resp"
pass "SendMessage: tag-routed dispatch errors actionably on unknown tag (Phase 4)"

# ── 16. HTTP gateway (ADR-014 Phase 2, v0.11) ────────────────────────────
echo ""
echo "▶ test: clawtool serve --listen HTTP gateway"

# Pick a random high port to avoid conflicts.
HTTP_PORT=$(awk 'BEGIN{srand(); print int(40000+rand()*20000)}')
HTTP_TOKEN_FILE="$TMPCFG/listener-token"

# 16a. init-token writes a 0600 file with a 64-char hex token.
"$BIN" serve init-token "$HTTP_TOKEN_FILE" >/dev/null
[[ -f "$HTTP_TOKEN_FILE" ]] || fail "init-token: file not created"
HTTP_TOKEN=$(cat "$HTTP_TOKEN_FILE" | tr -d '\n')
[[ ${#HTTP_TOKEN} -eq 64 ]] || fail "init-token: token should be 64 hex chars, got ${#HTTP_TOKEN}"
pass "init-token: writes 64-char hex token"

# Some shells / Linux distros leave the file group-readable by umask;
# our InitTokenFile forces 0600 — verify the bit landed.
mode=$(stat -c '%a' "$HTTP_TOKEN_FILE" 2>/dev/null || stat -f '%Lp' "$HTTP_TOKEN_FILE")
[[ "$mode" == "600" ]] || fail "init-token: file mode is $mode, expected 600"
pass "init-token: file mode is 0600"

# 16b. Boot the gateway in the background, wait for it to start.
XDG_CONFIG_HOME="$TMPCFG" "$BIN" serve --listen ":$HTTP_PORT" --token-file "$HTTP_TOKEN_FILE" >/dev/null 2>&1 &
HTTP_PID=$!
trap 'kill $HTTP_PID 2>/dev/null || true; rm -rf "$TMPCFG" "$RECIPE_TMP" 2>/dev/null || true' EXIT

# Wait up to 5s for the listener to come up.
for _ in $(seq 1 50); do
  if curl -sS -o /dev/null "http://127.0.0.1:$HTTP_PORT/v1/health" 2>/dev/null; then
    break
  fi
  sleep 0.1
done

# 16c. Unauthenticated request rejected.
status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$HTTP_PORT/v1/health")
[[ "$status" == "401" ]] || fail "unauth /v1/health: expected 401, got $status"
pass "/v1/health: rejects requests without bearer token"

# 16d. Authenticated /v1/health returns 200 + JSON.
health=$(curl -sS -H "Authorization: Bearer $HTTP_TOKEN" "http://127.0.0.1:$HTTP_PORT/v1/health")
grep -qF '"status":"ok"' <<<"$health" || fail "/v1/health body: $health"
pass "/v1/health: 200 with status=ok"

# 16e. /v1/agents returns the registry snapshot with count + agents.
agents=$(curl -sS -H "Authorization: Bearer $HTTP_TOKEN" "http://127.0.0.1:$HTTP_PORT/v1/agents")
grep -qF '"agents":' <<<"$agents" || fail "/v1/agents body: $agents"
grep -qF '"count":' <<<"$agents" || fail "/v1/agents missing count: $agents"
pass "/v1/agents: registry snapshot returned"

# 16f. /v1/send_message rejects empty prompt with 400.
bad=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $HTTP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"instance":"claude"}' \
  "http://127.0.0.1:$HTTP_PORT/v1/send_message")
[[ "$bad" == "400" ]] || fail "/v1/send_message empty prompt: expected 400, got $bad"
pass "/v1/send_message: 400 on missing prompt"

# 16f-bis. /v1/send_message accepts the top-level `tag` shortcut (Phase 4).
# An unknown tag still 400s with a clear message — but the request must
# at least be parsed without error.
bad=$(curl -sS -w '%{http_code}' -o /tmp/clawtool_tag_resp \
  -H "Authorization: Bearer $HTTP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"hi","tag":"non-existent-tag"}' \
  "http://127.0.0.1:$HTTP_PORT/v1/send_message")
[[ "$bad" == "400" ]] || fail "/v1/send_message tag-routed unknown tag: expected 400, got $bad"
grep -qE "carries tag|no callable" /tmp/clawtool_tag_resp \
  || fail "/v1/send_message tag-routed: error body should mention the missing tag"
rm -f /tmp/clawtool_tag_resp
pass "/v1/send_message: top-level 'tag' field routes through tag-routed dispatch (Phase 4)"

# 16g. Wrong token rejected.
status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer wrong-token" \
  "http://127.0.0.1:$HTTP_PORT/v1/health")
[[ "$status" == "401" ]] || fail "wrong token /v1/health: expected 401, got $status"
pass "/v1/health: rejects wrong token"

# 16h. Unknown path 404.
status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $HTTP_TOKEN" \
  "http://127.0.0.1:$HTTP_PORT/v1/no-such-endpoint")
[[ "$status" == "404" ]] || fail "unknown path: expected 404, got $status"
pass "unknown path: 404"

# 16i. /v1/recipes returns the catalog (Phase 4-bis).
recipes=$(curl -sS -H "Authorization: Bearer $HTTP_TOKEN" "http://127.0.0.1:$HTTP_PORT/v1/recipes")
grep -qF '"recipes":' <<<"$recipes" || fail "/v1/recipes body: $recipes"
grep -qF '"name":"license"' <<<"$recipes" || fail "/v1/recipes should include license recipe"
grep -qF '"name":"codex-bridge"' <<<"$recipes" || fail "/v1/recipes should include codex-bridge"
pass "/v1/recipes: catalog enumerated (license + codex-bridge present)"

# 16j. /v1/recipe/apply happy path against a tempdir.
RECIPE_HTTP_TMP=$(mktemp -d)
apply_status=$(curl -sS -w '%{http_code}' -o /tmp/clawtool_recipe_apply \
  -H "Authorization: Bearer $HTTP_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"conventional-commits-ci\",\"repo\":\"$RECIPE_HTTP_TMP\"}" \
  "http://127.0.0.1:$HTTP_PORT/v1/recipe/apply")
[[ "$apply_status" == "200" ]] || fail "/v1/recipe/apply: expected 200, got $apply_status (body: $(cat /tmp/clawtool_recipe_apply))"
grep -qF '"verify_ok":true' /tmp/clawtool_recipe_apply \
  || fail "/v1/recipe/apply: verify_ok != true"
[[ -f "$RECIPE_HTTP_TMP/.github/workflows/commit-format.yml" ]] \
  || fail "/v1/recipe/apply: workflow file not written"
rm -rf "$RECIPE_HTTP_TMP" /tmp/clawtool_recipe_apply
pass "/v1/recipe/apply: applies recipe + writes file on disk"

# 16k. /v1/recipe/apply rejects missing repo.
bad=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $HTTP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"license"}' \
  "http://127.0.0.1:$HTTP_PORT/v1/recipe/apply")
[[ "$bad" == "400" ]] || fail "/v1/recipe/apply missing repo: expected 400, got $bad"
pass "/v1/recipe/apply: refuses missing repo"

# Clean shutdown.
kill $HTTP_PID 2>/dev/null
wait $HTTP_PID 2>/dev/null || true

# ── 17. clawtool serve --listen --mcp-http (MCP-over-HTTP transport) ─────
echo ""
echo "▶ test: --mcp-http StreamableHTTPServer"

MCP_HTTP_PORT=$(awk 'BEGIN{srand(); print int(40000+rand()*20000)}')

XDG_CONFIG_HOME="$TMPCFG" "$BIN" serve --listen ":$MCP_HTTP_PORT" --token-file "$HTTP_TOKEN_FILE" --mcp-http >/dev/null 2>&1 &
MCP_HTTP_PID=$!
trap 'kill $HTTP_PID 2>/dev/null || true; kill $MCP_HTTP_PID 2>/dev/null || true; rm -rf "$TMPCFG" "$RECIPE_TMP" 2>/dev/null || true' EXIT

for _ in $(seq 1 50); do
  if curl -sS -o /dev/null "http://127.0.0.1:$MCP_HTTP_PORT/v1/health" 2>/dev/null; then
    break
  fi
  sleep 0.1
done

# 17a. /mcp endpoint exists when --mcp-http set; rejects unauth.
status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$MCP_HTTP_PORT/mcp")
[[ "$status" == "401" ]] || fail "/mcp without auth: expected 401, got $status"
pass "/mcp: rejects unauthenticated requests"

# 17b. /mcp accepts an MCP initialize request when bearer token is supplied.
# We don't speak the full JSON-RPC handshake here; just verify the endpoint
# responds with something non-401/404 to the auth-stamped request.
status=$(curl -sS -o /tmp/clawtool_mcp_resp -w '%{http_code}' \
  -X POST \
  -H "Authorization: Bearer $HTTP_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}' \
  "http://127.0.0.1:$MCP_HTTP_PORT/mcp")
case "$status" in
  200|202)
    pass "/mcp: streamable-HTTP transport responds to authenticated initialize ($status)"
    ;;
  *)
    fail "/mcp: expected 200/202 from auth'd initialize, got $status (body: $(cat /tmp/clawtool_mcp_resp))"
    ;;
esac
rm -f /tmp/clawtool_mcp_resp

kill $MCP_HTTP_PID 2>/dev/null || true
wait $MCP_HTTP_PID 2>/dev/null || true

# ── 18. Verify MCP tool (ADR-014 T4) ─────────────────────────────────────
echo ""
echo "▶ test: Verify MCP tool"

VERIFY_TMP=$(mktemp -d)
# A tiny passing Go module
cat > "$VERIFY_TMP/go.mod" <<EOF
module verify_e2e

go 1.25
EOF
cat > "$VERIFY_TMP/x_test.go" <<EOF
package verify_e2e

import "testing"

func TestPasses(t *testing.T) {}
EOF

verify_resp=$(printf '%s\n%s\n%s\n' \
  "$initialize_msg" \
  "$initialized_notification" \
  "$(printf '{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"Verify\",\"arguments\":{\"repo\":\"%s\"}}}' "$VERIFY_TMP")" \
  | XDG_CONFIG_HOME="$TMPCFG" $TIMEOUT_BIN 60 "$BIN" serve 2>/dev/null)

grep -qF '"overall":"pass"' <<<"$(grep structuredContent <<<"$verify_resp")" \
  || fail "Verify: expected overall=pass — got: $verify_resp"
pass "Verify: detects go module + reports pass"

grep -qF '"name":"go test ./..."' <<<"$(grep structuredContent <<<"$verify_resp")" \
  || fail "Verify: expected runner name 'go test ./...'"
pass "Verify: runner name carried in response"

rm -rf "$VERIFY_TMP"

# ── done ──────────────────────────────────────────────────────────────────

echo ""
echo "✓ all e2e tests passed"
