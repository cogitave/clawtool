// Package core — SetContext / GetContext MCP tools (octopus
// pattern: "ambient editor context"). Lets an agent (or an IDE
// integration that drives clawtool's MCP surface) tell the daemon
// "right now I'm editing X line Y, the user's intent is Z" — and
// have other tools / agents query that state without re-asking.
//
// Why this exists: clawtool sits between many agents and many
// tools, but the BIAM dispatch surface is request/response — there's
// no shared scratchpad for "things that are true right now in the
// user's editor." Without this every tool re-derives context from
// the prompt, and a second agent that wants to act on the same
// state has to be told it explicitly. SetContext is the small,
// boring storage layer that closes that gap.
//
// Not a CRDT, not a long-term store. The data lives in a process-
// local map keyed by session ID; daemon restart wipes it. That's
// the right scope for "what is the user looking at this minute" —
// older state would mislead more than it helps.
package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// EditorContext is the per-session ambient state every agent /
// tool call can read or write. All fields are optional; SetContext
// merges the supplied keys into the existing state instead of
// overwriting wholesale, so an agent that only updates the cursor
// position doesn't have to re-supply file_path + intent every
// call.
type EditorContext struct {
	FilePath    string    `json:"file_path,omitempty"`
	StartLine   int       `json:"start_line,omitempty"`
	EndLine     int       `json:"end_line,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	Intent      string    `json:"intent,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	UpdatedBy   string    `json:"updated_by,omitempty"`
}

// IsZero reports whether the context has no meaningful fields set.
// Used by GetContext to render "(no context set)" rather than an
// empty struct.
func (c EditorContext) IsZero() bool {
	return c.FilePath == "" && c.ProjectRoot == "" && c.Intent == "" &&
		c.StartLine == 0 && c.EndLine == 0
}

// contextStore is the process-wide registry. Single-process
// scope is intentional — daemon restart should wipe it (stale
// "user is editing X" from yesterday would mislead callers).
type contextStore struct {
	mu       sync.RWMutex
	sessions map[string]EditorContext
}

var contexts = &contextStore{sessions: map[string]EditorContext{}}

// ResetContextsForTest wipes the store. Test-only helper.
func ResetContextsForTest() {
	contexts.mu.Lock()
	defer contexts.mu.Unlock()
	contexts.sessions = map[string]EditorContext{}
}

const defaultContextSession = "default"

// setContextResult is the JSON envelope SetContext emits. Echoes
// the stored state back so the caller can verify the merge result
// in one round-trip.
type setContextResult struct {
	BaseResult
	SessionID string        `json:"session_id"`
	Context   EditorContext `json:"context"`
}

func (r setContextResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.SessionID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✓ context set for session %s\n", r.SessionID)
	if r.Context.FilePath != "" {
		fmt.Fprintf(&b, "  file:    %s\n", r.Context.FilePath)
	}
	if r.Context.StartLine > 0 || r.Context.EndLine > 0 {
		fmt.Fprintf(&b, "  lines:   %d–%d\n", r.Context.StartLine, r.Context.EndLine)
	}
	if r.Context.ProjectRoot != "" {
		fmt.Fprintf(&b, "  project: %s\n", r.Context.ProjectRoot)
	}
	if r.Context.Intent != "" {
		fmt.Fprintf(&b, "  intent:  %s\n", r.Context.Intent)
	}
	if r.Context.UpdatedBy != "" {
		fmt.Fprintf(&b, "  by:      %s\n", r.Context.UpdatedBy)
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(fmt.Sprintf("session: %s", r.SessionID)))
	return b.String()
}

type getContextResult struct {
	BaseResult
	SessionID string        `json:"session_id"`
	Context   EditorContext `json:"context"`
}

func (r getContextResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.SessionID)
	}
	var b strings.Builder
	if r.Context.IsZero() {
		fmt.Fprintf(&b, "(no context set for session %s)\n", r.SessionID)
		return b.String()
	}
	fmt.Fprintf(&b, "session %s\n", r.SessionID)
	if r.Context.FilePath != "" {
		fmt.Fprintf(&b, "  file:    %s\n", r.Context.FilePath)
	}
	if r.Context.StartLine > 0 || r.Context.EndLine > 0 {
		fmt.Fprintf(&b, "  lines:   %d–%d\n", r.Context.StartLine, r.Context.EndLine)
	}
	if r.Context.ProjectRoot != "" {
		fmt.Fprintf(&b, "  project: %s\n", r.Context.ProjectRoot)
	}
	if r.Context.Intent != "" {
		fmt.Fprintf(&b, "  intent:  %s\n", r.Context.Intent)
	}
	if !r.Context.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "  age:     %s\n", time.Since(r.Context.UpdatedAt).Round(time.Second))
	}
	if r.Context.UpdatedBy != "" {
		fmt.Fprintf(&b, "  by:      %s\n", r.Context.UpdatedBy)
	}
	return b.String()
}

// RegisterSetContext registers SetContext + GetContext on the MCP
// server. The pair is wired together because they share storage
// — a runtime that opted into one without the other would surface
// a write-only or read-only context which is rarely useful.
func RegisterSetContext(s *server.MCPServer) {
	setTool := mcp.NewTool(
		"SetContext",
		mcp.WithDescription(
			"Store ambient editor context (file path, selected line range, project root, "+
				"task intent) for the current session so other tools / agents can read it via "+
				"GetContext. Merges with existing state — supplying just `start_line` updates the "+
				"cursor without clobbering the file path. Lifetime: process-local; daemon restart "+
				"wipes the store. Use this when the human's editor focus is meaningful to the "+
				"work in flight (refactor across N files, code review, debugging).",
		),
		mcp.WithString("file_path", mcp.Description("Absolute or repo-relative path to the file the user is currently focused on.")),
		mcp.WithNumber("start_line", mcp.Description("First line of the active selection (1-indexed). 0 = unset.")),
		mcp.WithNumber("end_line", mcp.Description("Last line of the active selection (1-indexed, inclusive). 0 = unset.")),
		mcp.WithString("project_root", mcp.Description("Absolute path to the repo root the work belongs to.")),
		mcp.WithString("intent", mcp.Description("Short human-readable description of what the user is trying to accomplish.")),
		mcp.WithString("session_id", mcp.Description("Logical session identifier. Default: \"default\" (single shared session).")),
		mcp.WithString("updated_by", mcp.Description("Free-form attribution: agent family, IDE name, or any tag the operator wants in audit logs.")),
	)
	s.AddTool(setTool, runSetContext)

	getTool := mcp.NewTool(
		"GetContext",
		mcp.WithDescription(
			"Read the ambient editor context previously set via SetContext. Returns the "+
				"merged state for the named session or an empty result when nothing has been "+
				"stored. Useful when an agent / tool needs to know what file / intent the "+
				"current operator session is focused on without re-asking.",
		),
		mcp.WithString("session_id", mcp.Description("Logical session identifier. Default: \"default\".")),
	)
	s.AddTool(getTool, runGetContext)
}

func runSetContext(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	session := strings.TrimSpace(req.GetString("session_id", defaultContextSession))
	if session == "" {
		session = defaultContextSession
	}

	contexts.mu.Lock()
	cur := contexts.sessions[session]
	if v := strings.TrimSpace(req.GetString("file_path", "")); v != "" {
		cur.FilePath = v
	}
	if v := int(req.GetFloat("start_line", 0)); v > 0 {
		cur.StartLine = v
	}
	if v := int(req.GetFloat("end_line", 0)); v > 0 {
		cur.EndLine = v
	}
	if v := strings.TrimSpace(req.GetString("project_root", "")); v != "" {
		cur.ProjectRoot = v
	}
	if v := strings.TrimSpace(req.GetString("intent", "")); v != "" {
		cur.Intent = v
	}
	if v := strings.TrimSpace(req.GetString("updated_by", "")); v != "" {
		cur.UpdatedBy = v
	}
	cur.UpdatedAt = time.Now()
	contexts.sessions[session] = cur
	contexts.mu.Unlock()

	out := setContextResult{
		BaseResult: BaseResult{
			Operation:  "SetContext",
			DurationMs: time.Since(start).Milliseconds(),
		},
		SessionID: session,
		Context:   cur,
	}
	return resultOf(out), nil
}

func runGetContext(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	session := strings.TrimSpace(req.GetString("session_id", defaultContextSession))
	if session == "" {
		session = defaultContextSession
	}

	contexts.mu.RLock()
	cur := contexts.sessions[session]
	contexts.mu.RUnlock()

	out := getContextResult{
		BaseResult: BaseResult{
			Operation:  "GetContext",
			DurationMs: time.Since(start).Milliseconds(),
		},
		SessionID: session,
		Context:   cur,
	}
	return resultOf(out), nil
}

// CurrentContext returns a snapshot of the named session's
// context for in-process callers (other tool handlers that want
// to read context without going through the MCP envelope). Pure
// Go API; no JSON round-trip.
func CurrentContext(session string) EditorContext {
	if session == "" {
		session = defaultContextSession
	}
	contexts.mu.RLock()
	defer contexts.mu.RUnlock()
	return contexts.sessions[session]
}
