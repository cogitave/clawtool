#!/usr/bin/env bash
# Post-install cleanup for clawtool. Idempotent — safe to run on
# every `make install` and on first install alike.
#
# What it fixes
#   Some users end up with TWO clawtool MCP registrations:
#     - one under `claude mcp` (manual, set up before plugin existed)
#     - one auto-registered by the clawtool plugin
#   That doubles every tool name in the model's view (mcp__clawtool__*
#   and mcp__plugin_clawtool_clawtool__*). The fix is single-sourcing:
#   keep the plugin-managed one (it stays in sync with the marketplace
#   repo), drop the manual `claude mcp` entry.
#
#   The script:
#     1. Detects whether `claude` is on PATH. No-op when absent.
#     2. Probes `claude mcp list` for a `clawtool ` row (name-prefix).
#     3. Removes that row via `claude mcp remove clawtool` if found.
#     4. Probes `claude plugin list` for `clawtool@`. If absent, hints
#        the user to `claude plugin install clawtool@clawtool-marketplace`.
#
# What it does NOT do
#   - Doesn't touch the plugin install (the plugin is the single
#     source of truth; we never delete it here).
#   - Doesn't restart Claude Code (the user's session needs a manual
#     restart to pick up new MCP tools after a binary swap; we just
#     hint at it on the way out).
#   - Doesn't send any telemetry or hit network endpoints.

set -e

if ! command -v claude >/dev/null 2>&1; then
  # Claude CLI not on PATH — nothing to clean up. Likely a CI
  # runner or a user without Claude Code installed yet. Quietly
  # exit so the install target stays a one-liner.
  exit 0
fi

# 1. Drop manual MCP registration if one exists.
if claude mcp list 2>/dev/null | grep -qE '^clawtool([[:space:]]|$)'; then
  echo "  · removing duplicate manual MCP registration of clawtool"
  claude mcp remove clawtool >/dev/null 2>&1 || true
fi

# 2. Hint at plugin install if the plugin isn't there yet.
if ! claude plugin list 2>/dev/null | grep -q 'clawtool@'; then
  cat <<'EOF'

  ! clawtool plugin not detected in claude plugin list.
    Run once to wire the MCP server + skill + slash commands:

      claude plugin marketplace add cogitave/clawtool
      claude plugin install clawtool@clawtool-marketplace

EOF
fi

# 3. Restart hint — MCP tools land at server-start, so existing
# Claude Code sessions still have the old surface until restart.
echo "  · note: restart Claude Code to pick up any new mcp__clawtool__* tools"
