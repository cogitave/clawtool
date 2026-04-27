// Package core — SendMessage and AgentList MCP tools (ADR-014 Phase 1).
//
// SendMessage routes a prompt to the resolved agent's transport and
// buffers the streaming reply for the MCP response. Full HTTP-grade
// streaming arrives with `clawtool serve` in Phase 2; the MCP wire
// here is request/response so we accept the buffer cap.
//
// AgentList exposes the supervisor's registry snapshot — same shape
// as `clawtool send --list` and `GET /v1/agents`. Mirrors the v0.9
// `RecipeList` pattern (read-only, structured, BaseResult-shaped).
package core

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// biamStore is the process-wide BIAM SQLite handle shared with the
// agents/biam runner. Server boot calls SetBiamStore once init
// succeeds; the Task* MCP tools read from it. Nil store → tools
// return a "not configured" error.
var biamStore *biam.Store

// SetBiamStore registers the process-wide BIAM store. Idempotent.
func SetBiamStore(s *biam.Store) { biamStore = s }

const sendMessageBufferCapBytes = 5 * 1024 * 1024 // 5 MB cap on returned content

// ── shapes ─────────────────────────────────────────────────────────

type sendMessageResult struct {
	BaseResult
	Instance  string `json:"instance"`
	Family    string `json:"family"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Bidi      bool   `json:"bidi,omitempty"`
}

func (r sendMessageResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Instance)
	}
	if r.Bidi {
		return r.SuccessLine(fmt.Sprintf("submitted task %s · %s", r.TaskID, r.Instance),
			"async (use TaskGet / TaskWait to poll)")
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("%s · %s", r.Instance, r.Family)))
	b.WriteByte('\n')
	b.WriteString("───\n")
	b.WriteString(r.Content)
	if !strings.HasSuffix(r.Content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("───\n")
	if r.Truncated {
		b.WriteString(r.FooterLine("truncated"))
	} else {
		b.WriteString(r.FooterLine())
	}
	return b.String()
}

type agentListResult struct {
	BaseResult
	Agents []agents.Agent `json:"agents"`
}

func (r agentListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d agent(s) registered\n\n", len(r.Agents))
	if len(r.Agents) == 0 {
		b.WriteString("(none — run `BridgeAdd` to install one)\n\n")
		b.WriteString(r.FooterLine())
		return b.String()
	}
	fmt.Fprintf(&b, "  %-22s %-10s %-10s %-14s %s\n", "INSTANCE", "FAMILY", "CALLABLE", "STATUS", "AUTH SCOPE")
	for _, ag := range r.Agents {
		callable := "no"
		if ag.Callable {
			callable = "yes"
		}
		fmt.Fprintf(&b, "  %-22s %-10s %-10s %-14s %s\n", ag.Instance, ag.Family, callable, ag.Status, ag.AuthScope)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

// ── registration ───────────────────────────────────────────────────

// RegisterAgentTools adds SendMessage + AgentList to the MCP server.
func RegisterAgentTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"SendMessage",
			mcp.WithDescription(
				"Forward a prompt to a configured AI coding-agent CLI (claude / codex / "+
					"opencode / gemini) and return its streamed reply. Per ADR-014, "+
					"clawtool wraps each upstream's published headless mode (codex exec, "+
					"opencode run, gemini -p, claude -p) — we don't re-implement agent "+
					"loops. Use AgentList to enumerate available instances.",
			),
			mcp.WithString("agent",
				mcp.Description("Instance name (claude-personal, claude-work, codex1, …) or bare family name when only one instance of that family exists. Empty = sticky default.")),
			mcp.WithString("prompt", mcp.Required(),
				mcp.Description("The prompt to forward. Plain text.")),
			mcp.WithString("session",
				mcp.Description("Upstream session UUID for resume (claude / codex / opencode). Vendor-specific; ignored when unsupported.")),
			mcp.WithString("model",
				mcp.Description("Vendor-specific model name. Empty = upstream default.")),
			mcp.WithString("format",
				mcp.Description("Output format: text | json | stream-json. Pass-through; not all upstreams honor every value.")),
			mcp.WithString("cwd",
				mcp.Description("Working directory for the upstream CLI. Defaults to current process cwd.")),
			mcp.WithString("tag",
				mcp.Description("Tag-routed dispatch (Phase 4). When set, picks any callable instance whose tags include this label. Overrides the configured dispatch.mode for this call.")),
			mcp.WithBoolean("bidi",
				mcp.Description("ADR-015 BIAM async mode. When true, returns a task_id immediately + persists the upstream stream into the BIAM store; pair with TaskGet / TaskWait. Default false (synchronous, buffered single payload).")),
		),
		runSendMessage,
	)

	s.AddTool(
		mcp.NewTool(
			"AgentList",
			mcp.WithDescription(
				"Snapshot of the supervisor's agent registry — every configured "+
					"instance with family, bridge name, callable / status, and auth "+
					"scope. Same shape as `clawtool send --list` and the HTTP "+
					"GET /v1/agents response. Read-only.",
			),
		),
		runAgentList,
	)
}

// ── handlers ───────────────────────────────────────────────────────

func runSendMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: prompt"), nil
	}
	agentName := req.GetString("agent", "")
	session := req.GetString("session", "")
	model := req.GetString("model", "")
	format := req.GetString("format", "")
	cwd := req.GetString("cwd", "")
	tag := req.GetString("tag", "")
	bidi := req.GetBool("bidi", false)

	start := time.Now()
	out := sendMessageResult{BaseResult: BaseResult{Operation: "SendMessage", Engine: "supervisor"}}

	sup := agents.NewSupervisor()

	// Pre-resolve only when the caller pinned an instance and didn't
	// pass a tag. Tag-routed dispatch and round-robin pick instances
	// inside Supervisor.Send, so a pre-resolve here would either
	// short-circuit the policy or fail noisily on tag-only calls.
	if agentName != "" && tag == "" {
		resolved, rerr := sup.Resolve(ctx, agentName)
		if rerr != nil {
			out.ErrorReason = rerr.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Instance = resolved.Instance
		out.Family = resolved.Family
	}

	opts := map[string]any{}
	if session != "" {
		opts["session_id"] = session
	}
	if model != "" {
		opts["model"] = model
	}
	if format != "" {
		opts["format"] = format
	}
	if cwd != "" {
		opts["cwd"] = cwd
	}
	if tag != "" {
		opts["tag"] = tag
	}

	if bidi {
		taskID, err := sup.SubmitAsync(ctx, agentName, prompt, opts)
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.TaskID = taskID
		out.Bidi = true
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	rc, err := sup.Send(ctx, agentName, prompt, opts)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Read with cap. Anything beyond the cap gets truncated; the
	// MCP response stays a single payload (streaming arrives with
	// Phase 2's HTTP gateway).
	buf, truncated := readCapped(rc, sendMessageBufferCapBytes)
	out.Content = string(buf)
	out.Truncated = truncated

	// Surface upstream non-zero exit. streamingProcess.Close()
	// returns *exec.ExitError when the CLI crashed — without
	// folding it into the result the agent sees a truncated
	// reply as success. Keep the buffered content so the agent
	// can read the partial output for debugging.
	if closeErr := rc.Close(); closeErr != nil {
		out.ErrorReason = fmt.Sprintf("upstream exited non-zero: %v", closeErr)
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

func runAgentList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := agentListResult{BaseResult: BaseResult{Operation: "AgentList", Engine: "supervisor"}}
	sup := agents.NewSupervisor()
	all, err := sup.Agents(ctx)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Agents = all
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

// readCapped reads up to cap bytes from r. Returns the slice + a
// truncation flag set when the upstream had more bytes available.
func readCapped(r io.Reader, cap int) ([]byte, bool) {
	buf := make([]byte, 0, 16*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			if len(buf)+n > cap {
				take := cap - len(buf)
				if take > 0 {
					buf = append(buf, tmp[:take]...)
				}
				return buf, true
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return buf, false
		}
	}
}
