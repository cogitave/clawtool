#!/usr/bin/env bash
# Multi-instance soak — spawns clawtool with 3 real upstream MCP servers
# from the built-in catalog and verifies aggregation + routing end-to-end.
# Per ADR-009 v1.0 gating criterion #5.
#
# This test is deliberately separate from `make e2e` (which uses a Go
# stub-server fixture and stays fast + deterministic). Real upstreams pull
# packages from npm on first run; CI runs this nightly, not on every PR.
#
# Prereqs:
#   - clawtool binary built (run `make build` first)
#   - npx on PATH (Node.js 18+; 22 is what CI uses)
#   - network access (npm registry, ~60s on first warm)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$REPO_ROOT/bin/clawtool"

[[ -x "$BIN" ]] || { echo "build first: make build"; exit 1; }
command -v npx >/dev/null 2>&1 || { echo "npx required (install Node.js)"; exit 1; }

fail() { echo "✘ $*" >&2; exit 1; }
pass() { echo "✓ $*"; }
info() { echo "▶ $*"; }

TMPCFG=$(mktemp -d)
TMPFS=$(mktemp -d)
trap 'rm -rf "$TMPCFG" "$TMPFS"' EXIT

mkdir -p "$TMPCFG/clawtool"

# ── Step 1: catalog adds (exercises the bare-name UX from ADR-008) ──────
info "adding 3 sources from the built-in catalog"
XDG_CONFIG_HOME="$TMPCFG" "$BIN" source add memory >/dev/null
XDG_CONFIG_HOME="$TMPCFG" "$BIN" source add sequentialthinking --as thinking >/dev/null
XDG_CONFIG_HOME="$TMPCFG" "$BIN" source add filesystem --as fs >/dev/null
pass "catalog adds: memory + thinking + fs (instance names differ from catalog names)"

# ── Step 2: source list shows the auth-readiness shape ──────────────────
info "source list (auth-readiness)"
XDG_CONFIG_HOME="$TMPCFG" "$BIN" source list

# ── Step 3: clawtool serve aggregates tools from every running source ──
info "spawning clawtool serve + tools/list"

INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"soak","version":"0.1"}}}'
INITED='{"jsonrpc":"2.0","method":"notifications/initialized"}'

list_resp=$(printf '%s\n%s\n%s\n' \
  "$INIT" "$INITED" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | XDG_CONFIG_HOME="$TMPCFG" FILESYSTEM_ROOT="$TMPFS" \
    timeout 180 "$BIN" serve 2>/dev/null)

echo "$list_resp" | grep -q '"name":"memory__'   || fail "memory source did not aggregate"
pass "memory tools aggregated under wire-form memory__*"

echo "$list_resp" | grep -q '"name":"thinking__' || fail "thinking source did not aggregate"
pass "thinking tools aggregated (instance 'thinking' ≠ catalog 'sequentialthinking')"

echo "$list_resp" | grep -q '"name":"fs__'       || fail "fs source did not aggregate (\${FILESYSTEM_ROOT} subst broken?)"
pass "fs tools aggregated (\${FILESYSTEM_ROOT} substituted into argv at spawn time)"

# Core tools coexist
echo "$list_resp" | grep -q '"name":"Bash"'       || fail "core Bash dropped while sources running"
pass "core Bash coexists"

echo "$list_resp" | grep -q '"name":"ToolSearch"' || fail "core ToolSearch dropped"
pass "core ToolSearch present (its index includes sourced tools)"

# ── Step 4: ToolSearch finds sourced tools by semantic query ────────────
info "ToolSearch query against sourced surface"

search_resp=$(printf '%s\n%s\n%s\n' \
  "$INIT" "$INITED" \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ToolSearch","arguments":{"query":"create knowledge graph entity","type":"sourced","limit":5}}}' \
  | XDG_CONFIG_HOME="$TMPCFG" FILESYSTEM_ROOT="$TMPFS" \
    timeout 180 "$BIN" serve 2>/dev/null)

echo "$search_resp" | grep -qF '\"engine\":\"bleve-bm25\"' \
  || fail "ToolSearch did not return bleve-bm25 envelope: $search_resp"
pass "ToolSearch ranked across sourced surface (bleve-bm25)"

echo "$search_resp" | grep -qF '\"type\":\"sourced\"' \
  || fail "ToolSearch type=sourced filter did not return any sourced result"
pass "ToolSearch type=sourced filter returns sourced tools (not core)"

# ── done ────────────────────────────────────────────────────────────────
echo ""
echo "✓ multi-instance soak passed"
echo "  upstreams verified: @modelcontextprotocol/server-memory,"
echo "                      @modelcontextprotocol/server-sequential-thinking,"
echo "                      @modelcontextprotocol/server-filesystem"
echo "  v1.0 gating criterion #5 (≥3 real upstream MCP servers): green ✅"
