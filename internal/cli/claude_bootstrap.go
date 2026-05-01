package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/telemetry"
	"github.com/cogitave/clawtool/internal/version"
)

// runClaudeBootstrap is the entry point for the clawtool-context
// hook bundled in `hooks/hooks.json`. Claude Code invokes one of:
//
//	clawtool claude-bootstrap --event user-prompt-submit   # canonical
//	clawtool claude-bootstrap --event session-start        # legacy / back-compat
//
// The canonical fire site is UserPromptSubmit (fires on the user's
// first prompt). SessionStart was the original site but Claude Code
// v2.1.126 introduced a regression where the hook runner bails with
// "ToolUseContext is required for prompt hooks" when additionalContext
// injection happens before any prompt exists. UserPromptSubmit always
// has a live ToolUseContext.
//
// The hook reads its event JSON from stdin and emits one JSON
// document on stdout with this shape:
//
//	{
//	  "hookSpecificOutput": {
//	    "hookEventName": "UserPromptSubmit" | "SessionStart",
//	    "additionalContext": "<text injected before user's prompt>"
//	  }
//	}
//
// Idempotency: UserPromptSubmit fires on EVERY prompt — we only want
// to inject context once per session. We stamp a marker file at
// /tmp/clawtool-claude-bootstrap-<session_id> on first fire and
// short-circuit (empty context) on subsequent fires. The session_id
// comes from the hook event JSON on stdin (Claude Code injects it)
// or the CLAUDE_SESSION_ID env var as a fallback.
//
// We detect a `.clawtool/` marker walking up from cwd. When
// present, the additionalContext primes Claude with: clawtool is
// available, the user prefers `mcp__clawtool__*` tools, and on the
// first response Claude should offer continue / fresh-setup / just-
// stay-aware paths.
//
// Why a CLI subcommand rather than an MCP tool: SessionStart used
// to fire BEFORE MCP servers finished connecting. A `command` hook
// is reliably available at any hook fire point.
func (a *App) runClaudeBootstrap(argv []string) int {
	fs := flag.NewFlagSet("claude-bootstrap", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	event := fs.String("event", "session-start", "Hook event name: session-start (legacy) or user-prompt-submit (canonical).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	// Normalize event name → hookEventName the response declares.
	// Anything outside the known set still emits empty context so
	// future Claude Code events don't break the hook chain.
	var hookEventName string
	switch *event {
	case "session-start":
		hookEventName = "SessionStart"
	case "user-prompt-submit":
		hookEventName = "UserPromptSubmit"
	default:
		// Forward-compat: unknown events emit empty
		// additionalContext rather than refusing — keeps Claude
		// Code's hook chain happy while we incrementally add
		// behaviour.
		emitBootstrapJSONFor(a.Stdout, "SessionStart", "")
		return 0
	}

	// Read stdin best-effort. Claude Code ships the hook event JSON
	// here including session_id. Capped at 64 KiB so a runaway
	// producer can't stall / OOM the hook.
	var stdinBody []byte
	if a.Stdin != nil {
		stdinBody, _ = io.ReadAll(io.LimitReader(a.Stdin, 64*1024))
	}

	// Idempotency: only inject context once per session for the
	// UserPromptSubmit event. SessionStart is "fires once" by
	// definition so it skips the marker dance — keeps back-compat
	// for hosts still wired the old way.
	if *event == "user-prompt-submit" {
		sid := resolveBootstrapSessionID(stdinBody)
		if sid != "" {
			marker := bootstrapMarkerPath(sid)
			if _, err := os.Stat(marker); err == nil {
				// Already fired this session — short-circuit.
				emitBootstrapJSONFor(a.Stdout, hookEventName, "")
				return 0
			}
			// Stamp the marker BEFORE emitting so a concurrent
			// re-fire (unlikely but possible if Claude Code
			// re-runs the hook chain) sees it. Best-effort —
			// any error here just means we may emit twice,
			// which is harmless.
			_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		// No cwd means we can't detect markers; emit empty
		// context. The hook still succeeds — silent skip is
		// preferable to blocking the user's session start.
		emitBootstrapJSONFor(a.Stdout, hookEventName, "")
		return 0
	}

	root := findClawtoolRoot(cwd)
	ctx := buildBootstrapContext(root)
	emitBootstrapJSONFor(a.Stdout, hookEventName, ctx)
	return 0
}

// bootstrapMarkerPath returns the per-session idempotency marker
// path. /tmp is canonical for ephemeral hook state — it's wiped on
// reboot, doesn't pollute the user's config dir, and survives the
// lifetime of a single Claude Code session.
func bootstrapMarkerPath(sessionID string) string {
	// Sanitize: only keep characters safe for a filename. Claude
	// Code's session IDs are UUIDs in practice but we don't want
	// to trust that.
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, sessionID)
	return filepath.Join(os.TempDir(), "clawtool-claude-bootstrap-"+safe)
}

// resolveBootstrapSessionID extracts the session_id from the hook's
// stdin JSON (Claude Code's canonical channel) and falls back to the
// CLAUDE_SESSION_ID env var. Returns empty when neither is available
// — caller skips the idempotency check in that case (no marker = we
// just always emit, which is safe).
func resolveBootstrapSessionID(stdinBody []byte) string {
	if len(stdinBody) > 0 {
		var ev struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(stdinBody, &ev); err == nil {
			if sid := strings.TrimSpace(ev.SessionID); sid != "" {
				return sid
			}
		}
	}
	return strings.TrimSpace(os.Getenv("CLAUDE_SESSION_ID"))
}

// fetchUpdate is a package-level seam so tests can stub the version
// check without spinning up a real GitHub round-trip. Production
// path uses the standard CheckForUpdate with a 500ms ctx — well
// inside the SessionStart hook's 2s budget.
var fetchUpdate = func() version.UpdateInfo {
	c, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return version.CheckForUpdate(c)
}

// findClawtoolRoot walks up from `start` looking for a directory
// containing `.clawtool/`. Returns the parent directory when
// found, empty string when not. Stops at the filesystem root.
func findClawtoolRoot(start string) string {
	dir := start
	for {
		if info, err := os.Stat(filepath.Join(dir, ".clawtool")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// buildBootstrapContext renders the additionalContext string for
// Claude Code. Empty `root` returns empty context — clawtool stays
// quiet outside its scope. When root is present we list detected
// markers (wiki, brain config, recent log entries) so Claude can
// decide whether to offer "continue" or "start fresh" on its first
// reply.
func buildBootstrapContext(root string) string {
	if root == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("clawtool is active in this directory (.clawtool/ marker detected at ")
	b.WriteString(root)
	b.WriteString(").\n\n")
	b.WriteString("Prefer `mcp__clawtool__*` tools when both clawtool and a native equivalent exist. ")
	b.WriteString("Available primitives include Bash / Read / Edit / Write / Glob / Grep / WebFetch / WebSearch / SendMessage (multi-agent dispatch) / Commit (Conventional Commits enforcement) / RulesCheck.\n\n")

	markers := detectClawtoolMarkers(root)
	if len(markers) > 0 {
		b.WriteString("Detected project layout:\n")
		for _, m := range markers {
			b.WriteString("  - ")
			b.WriteString(m)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("On your first response, briefly check whether the user wants to (a) continue from the last session — peek at `wiki/log.md` if present, (b) start a fresh task, or (c) just stay context-aware while they drive. Don't dump the wiki contents unless asked.\n")

	// Onboarded-marker nudge — telemetry shows install→onboard
	// drop-off, so when the project marker is present but the
	// global onboard hasn't been run, surface a one-liner so the
	// operator knows the wizard is one command away.
	if !IsOnboarded() {
		b.WriteString("\n⚠ **clawtool installed but not onboarded.** Run `clawtool onboard` to wire bridges, claim MCP hosts, and start the daemon.\n")
	}

	// Auto-update probe — surface "vX → vY available" inline when
	// the user's clawtool is behind cogitave/clawtool's latest
	// release. Fail-open: any error (network, parse, timeout)
	// returns HasUpdate=false and we skip the line silently. Cache
	// in version.CheckForUpdate keeps the round-trip rare.
	info := fetchUpdate()
	outcome := "up_to_date"
	switch {
	case info.Err != nil:
		outcome = "check_failed"
	case info.HasUpdate:
		outcome = "update_available"
		b.WriteString("\n📦 **clawtool update available: v")
		b.WriteString(info.Current)
		b.WriteString(" → ")
		b.WriteString(info.Latest)
		b.WriteString("**\n")
		b.WriteString("To upgrade, run: `clawtool upgrade`\n")
	}
	if tc := telemetry.Get(); tc != nil && tc.Enabled() {
		tc.Track("clawtool.update_check", map[string]any{
			"version":        version.Resolved(),
			"update_outcome": outcome,
		})
	}
	return b.String()
}

// detectClawtoolMarkers reports which clawtool surfaces are
// populated under `root`. Order is stable for deterministic
// rendering; missing entries just don't appear. Best-effort —
// stat errors map to "absent".
func detectClawtoolMarkers(root string) []string {
	var found []string

	// Wiki vault — the project-bound brain layer.
	if info, err := os.Stat(filepath.Join(root, "wiki")); err == nil && info.IsDir() {
		found = append(found, "wiki/ — project knowledge base")
		// Surface most-recent log entry timestamp so Claude can
		// estimate session continuity without a full read.
		if logInfo, err := os.Stat(filepath.Join(root, "wiki", "log.md")); err == nil {
			age := time.Since(logInfo.ModTime()).Round(time.Hour)
			found = append(found, fmt.Sprintf("wiki/log.md — last updated %s ago", age))
		}
	}

	// .clawtool/ contents.
	clawtoolDir := filepath.Join(root, ".clawtool")
	if entries, err := os.ReadDir(clawtoolDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			found = append(found, ".clawtool/"+e.Name())
		}
	}

	// CLAUDE.md presence — clawtool may have written one.
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err == nil {
		found = append(found, "CLAUDE.md — project memory")
	}

	return found
}

// emitBootstrapJSONFor writes the hook output, declaring the
// supplied hookEventName ("SessionStart" or "UserPromptSubmit").
// Always produces valid JSON even when context is empty, since
// Claude Code expects a structured response from command hooks.
func emitBootstrapJSONFor(w io.Writer, hookEventName, additionalContext string) {
	out := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext,omitempty"`
		} `json:"hookSpecificOutput"`
	}{}
	out.HookSpecificOutput.HookEventName = hookEventName
	out.HookSpecificOutput.AdditionalContext = additionalContext
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}
