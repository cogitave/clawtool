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
	"github.com/cogitave/clawtool/internal/telemetry"
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

// BiamStore returns the process-wide BIAM store, or nil if the
// daemon never wired one (CLI / test paths). Callers that need the
// store for a read-only path (e.g. /v1/biam/subscribe's task-existence
// check) consult this getter instead of importing the unexported
// package var.
func BiamStore() *biam.Store { return biamStore }

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
				"Dispatch a prompt to another AI coding-agent CLI (claude / codex / "+
					"opencode / gemini) and return its reply. Use when the operator "+
					"says \"ask codex to ...\" or \"have gemini review ...\" or to "+
					"fan a task out to a peer CLI. Synchronous by default (buffered "+
					"single payload); pair with `bidi=true` + TaskWait / TaskNotify "+
					"for non-blocking dispatch when the upstream may take >10s. "+
					"clawtool wraps each upstream's published headless mode (codex "+
					"exec, opencode run, gemini -p, claude -p) — we don't "+
					"re-implement agent loops. Routing rule: code-writing → codex "+
					"or gemini; opencode reserved for read-only investigation. "+
					"NOT for sending shell commands — use Bash. Run AgentList "+
					"first to enumerate live instances.",
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
				mcp.Description("Async BIAM mode. When true, returns a task_id immediately and persists the upstream stream into the BIAM store; pair with TaskGet / TaskWait. Default false (synchronous, buffered single payload).")),
			mcp.WithString("from_instance",
				mcp.Description("BIAM envelope sender label. Override when a non-default host (codex / gemini / opencode) is dispatching back through the shared daemon — the resulting envelope's `from` field reflects the actual sender, so reply threading + audit trails stay accurate. Empty = use the daemon's own identity.")),
			mcp.WithString("mode",
				mcp.Description("Routing mode. 'peer-prefer' (default, empty) routes to a registered live BIAM peer when one matches the resolved family; falls back to spawning a fresh subprocess when no peer is online. 'peer-only' fails when no peer matches (use to guarantee the prompt lands in the operator's open pane). 'spawn-only' skips the peer registry and always spawns (legacy behavior).")),
			mcp.WithString("from_peer_id",
				mcp.Description("Caller's a2a peer_id. Used by peer-prefer to skip self-dispatch (we won't route a prompt back to the peer that sent it). Empty = no anti-self check; usually fine.")),
			mcp.WithBoolean("auto_close",
				mcp.Description("Per-task auto-close override for the resolved peer's tmux pane. Default true: when this dispatch lands in an auto-spawned pane and the task hits a terminal status, clawtool closes the pane. Pass false to pin the pane for this specific task — the lifecycle hook never sees a link for this task_id, so the pane stays alive for inspection or re-use by a follow-up dispatch. Has no effect on user-attached panes (they're never auto-closed regardless).")),
			mcp.WithArray("tools",
				mcp.Description("Optional curated subset of clawtool MCP tool names (e.g. [\"Bash\",\"Read\",\"Grep\"]) to surface to the upstream peer's MCP namespace for this dispatch only. Empty / absent (default) = upstream sees its usual full tools/list with no filtering. Names are validated against the local registry; an unknown name fails the call BEFORE dispatch. Phase 4 in-band wiring: the subset is attached to the BIAM envelope's body.extras.tools_subset and threaded through opts; upstream-side tools/list filtering is wired progressively (per-bridge), so today this primarily records the operator's intent in the envelope audit trail."),
				mcp.Items(map[string]any{"type": "string"}),
			),
		),
		runSendMessage,
	)

	s.AddTool(
		mcp.NewTool(
			"AgentList",
			mcp.WithDescription(
				"Enumerate every configured AI coding-agent instance the "+
					"supervisor knows about — family, bridge name, callable / "+
					"status, auth scope. Use BEFORE SendMessage to discover "+
					"which instance names (claude-personal, codex1, gemini-work, "+
					"...) are actually live, or when the operator asks \"what "+
					"agents do I have?\". NOT for probing one specific adapter's "+
					"detect / claim state — use AgentDetect for that. Same shape "+
					"as `clawtool send --list` and HTTP GET /v1/agents. Read-only.",
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
	fromInstance := strings.TrimSpace(req.GetString("from_instance", ""))
	mode := strings.TrimSpace(req.GetString("mode", ""))
	fromPeerID := strings.TrimSpace(req.GetString("from_peer_id", ""))
	// auto_close is read with a default of true (current
	// behaviour). We thread it through opts only when the caller
	// explicitly set it (raw arg present) so the legacy code path
	// — opts without an "auto_close" key — keeps working byte for
	// byte. The supervisor's autoCloseFromOpts treats missing key
	// as true regardless, so the default still wins; threading on
	// presence-only just keeps the dispatch opts minimal.
	_, autoCloseSet := req.GetArguments()["auto_close"]
	autoClose := req.GetBool("auto_close", true)

	// ADR-014 §Resolved (2026-05-02) Phase 4: explicit opt-in
	// curated tool subset. Validated against the local registry
	// BEFORE dispatch so a typo'd name surfaces here, not as a
	// silent missing-tool on the upstream side. Empty / absent
	// (default) = no filtering, full back-compat.
	toolsSubset, terr := parseToolsSubsetArg(req.GetArguments()["tools"])
	if terr != nil {
		out := sendMessageResult{BaseResult: BaseResult{Operation: "SendMessage", Engine: "supervisor"}}
		out.ErrorReason = terr.Error()
		return resultOf(out), nil
	}

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
	if fromInstance != "" {
		opts["from_instance"] = fromInstance
	}
	if mode != "" {
		opts["mode"] = mode
	}
	if fromPeerID != "" {
		opts["from_peer_id"] = fromPeerID
	}
	if autoCloseSet {
		opts["auto_close"] = autoClose
	}
	if len(toolsSubset) > 0 {
		opts["tools_subset"] = toolsSubset
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
	emitAgentDispatchEvent(out.Family, out.DurationMs, out.IsError(), bidi)
	return resultOf(out), nil
}

// emitAgentDispatchEvent fires after every SendMessage dispatch.
// Allow-listed shape: family only (never instance), duration,
// success/error outcome, sync vs bidi.
func emitAgentDispatchEvent(family string, durMs int64, isErr, bidi bool) {
	tc := telemetry.Get()
	if tc == nil || !tc.Enabled() {
		return
	}
	outcome := "success"
	if isErr {
		outcome = "error"
	}
	flags := "sync"
	if bidi {
		flags = "bidi"
	}
	tc.Track("agent.dispatch", map[string]any{
		"agent":       family,
		"duration_ms": durMs,
		"outcome":     outcome,
		"flags":       flags,
	})
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

// parseToolsSubsetArg coerces the SendMessage `tools` MCP argument
// into a validated []string. Empty / absent (nil) returns (nil, nil)
// — back-compat default. Each name is validated against the local
// BuildManifest() registry; an unknown name returns an error
// listing the closest known names so the operator's typo is
// obvious. ADR-014 Phase 4 (2026-05-02): explicit opt-in subset
// forwarding to the upstream's MCP namespace.
func parseToolsSubsetArg(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("tools: expected array, got %T", raw)
	}
	if len(arr) == 0 {
		return nil, nil
	}
	known := map[string]struct{}{}
	for _, n := range BuildManifest().Names() {
		known[n] = struct{}{}
	}
	out := make([]string, 0, len(arr))
	seen := map[string]bool{}
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("tools[%d]: expected string, got %T", i, v)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("tools[%d]: empty string", i)
		}
		if _, ok := known[s]; !ok {
			return nil, fmt.Errorf(
				"tools[%d]: unknown tool name %q — must be one of the local registry's MCP tools (run AgentList? no — see ToolSearch / `clawtool tools list`)",
				i, s)
		}
		if seen[s] {
			continue // de-dup quietly
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
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
