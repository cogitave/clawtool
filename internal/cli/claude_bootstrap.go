package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runClaudeBootstrap is the entry point for the SessionStart hook
// bundled in `hooks/hooks.json`. Claude Code invokes:
//
//	clawtool claude-bootstrap --event session-start
//
// at the start of every fresh session, BEFORE the first user
// prompt is processed. The hook reads its event JSON from stdin
// and emits one JSON document on stdout with this shape:
//
//	{
//	  "hookSpecificOutput": {
//	    "hookEventName": "SessionStart",
//	    "additionalContext": "<text injected before user's first prompt>"
//	  }
//	}
//
// We detect a `.clawtool/` marker walking up from cwd. When
// present, the additionalContext primes Claude with: clawtool is
// available, the user prefers `mcp__clawtool__*` tools, and on the
// first response Claude should offer continue / fresh-setup / just-
// stay-aware paths.
//
// Why a CLI subcommand rather than an MCP tool: per Claude Code
// 2.1.121 docs, SessionStart fires BEFORE MCP servers finish
// connecting. A `command` hook is the only thing that's reliably
// available at that point.
func (a *App) runClaudeBootstrap(argv []string) int {
	fs := flag.NewFlagSet("claude-bootstrap", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	event := fs.String("event", "session-start", "Hook event name (currently only session-start is supported).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *event != "session-start" {
		// Forward-compat: future events (UserPromptSubmit,
		// SessionEnd, etc.) emit empty additionalContext rather
		// than refusing — keeps Claude Code's hook chain happy
		// while we incrementally add behaviour.
		emitBootstrapJSON(a.Stdout, "")
		return 0
	}

	// Drain stdin best-effort. Hook events ship the conversation
	// transcript path + cwd here, but we don't need the body — the
	// process's own working directory is enough. Reading drains the
	// pipe so Claude Code doesn't see a stalled child.
	if a.Stdin != nil {
		_, _ = io.Copy(io.Discard, a.Stdin)
	}

	cwd, err := os.Getwd()
	if err != nil {
		// No cwd means we can't detect markers; emit empty
		// context. The hook still succeeds — silent skip is
		// preferable to blocking the user's session start.
		emitBootstrapJSON(a.Stdout, "")
		return 0
	}

	root := findClawtoolRoot(cwd)
	ctx := buildBootstrapContext(root)
	emitBootstrapJSON(a.Stdout, ctx)
	return 0
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

// emitBootstrapJSON writes the SessionStart hook output. Always
// produces valid JSON even when context is empty, since Claude
// Code expects a structured response from command hooks.
func emitBootstrapJSON(w io.Writer, additionalContext string) {
	out := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext,omitempty"`
		} `json:"hookSpecificOutput"`
	}{}
	out.HookSpecificOutput.HookEventName = "SessionStart"
	out.HookSpecificOutput.AdditionalContext = additionalContext
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}
