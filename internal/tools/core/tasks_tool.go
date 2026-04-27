// Package core — TaskGet / TaskWait / TaskList MCP tools (ADR-015
// Phase 1). Surface the BIAM SQLite store the supervisor's async
// runner persists into, so a calling model can:
//
//  1. Fire SendMessage with bidi=true → receive task_id immediately.
//  2. Continue its own work without blocking on the upstream.
//  3. Pull back via TaskGet (snapshot) / TaskWait (block until terminal)
//     when it actually needs the result.
//
// All three tools are read-only and stateless beyond the BIAM store.
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// taskGetResult is the snapshot shape. `Messages` is every envelope
// persisted under task_id, oldest first.
type taskGetResult struct {
	BaseResult
	Task     *biam.Task      `json:"task"`
	Messages []biam.Envelope `json:"messages,omitempty"`
}

func (r taskGetResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	if r.Task == nil {
		return r.SuccessLine("(task not found)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "task %s · %s · %d msg(s) · agent=%s\n",
		r.Task.TaskID, r.Task.Status, r.Task.MessageCount, r.Task.Agent)
	if r.Task.LastMessage != "" {
		fmt.Fprintf(&b, "last: %s\n", r.Task.LastMessage)
	}
	for _, e := range r.Messages {
		fmt.Fprintf(&b, "─ %s · %s · %s\n", e.MessageID[:8], e.Kind, truncateForRender(e.Body.Text, 200))
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

type taskListResult struct {
	BaseResult
	Tasks []biam.Task `json:"tasks"`
}

func (r taskListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d task(s)\n\n", len(r.Tasks))
	if len(r.Tasks) == 0 {
		b.WriteString("(none — submit one via SendMessage --bidi)\n\n")
		b.WriteString(r.FooterLine())
		return b.String()
	}
	fmt.Fprintf(&b, "  %-36s %-10s %-15s %s\n", "TASK_ID", "STATUS", "AGENT", "LAST")
	for _, t := range r.Tasks {
		last := truncateForRender(t.LastMessage, 80)
		fmt.Fprintf(&b, "  %-36s %-10s %-15s %s\n", t.TaskID, t.Status, t.Agent, last)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterTaskTools wires TaskGet / TaskWait / TaskList. Idempotent —
// safe to call when the BIAM store wasn't initialised; per-call
// handlers surface the "not configured" error.
func RegisterTaskTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"TaskGet",
			mcp.WithDescription(
				"Snapshot of one BIAM task: status + every message persisted "+
					"under task_id, oldest first. Pair with SendMessage --bidi "+
					"to dispatch async and pull the result without blocking the "+
					"caller. Read-only.",
			),
			mcp.WithString("task_id", mcp.Required(),
				mcp.Description("Task UUID returned from SendMessage --bidi.")),
		),
		runTaskGet,
	)
	s.AddTool(
		mcp.NewTool(
			"TaskWait",
			mcp.WithDescription(
				"Block until the BIAM task reaches a terminal state "+
					"(done | failed | cancelled | expired) or the deadline "+
					"elapses. Returns the final task snapshot + all messages. "+
					"Use this when the caller has nothing else to do until the "+
					"upstream finishes.",
			),
			mcp.WithString("task_id", mcp.Required()),
			mcp.WithNumber("timeout_s",
				mcp.Description("Block ceiling in seconds. Default 300 (5 min); hard cap 3600.")),
		),
		runTaskWait,
	)
	s.AddTool(
		mcp.NewTool(
			"TaskList",
			mcp.WithDescription(
				"Recent BIAM tasks (default 50, max 1000). Use this to find "+
					"task_ids when the caller forgot one mid-conversation.",
			),
			mcp.WithNumber("limit",
				mcp.Description("Max rows returned. Default 50, hard cap 1000.")),
		),
		runTaskList,
	)
}

func runTaskGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: task_id"), nil
	}
	start := time.Now()
	out := taskGetResult{BaseResult: BaseResult{Operation: "TaskGet", Engine: "biam"}}

	if biamStore == nil {
		out.ErrorReason = errBIAMNotInit.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	t, err := biamStore.GetTask(ctx, taskID)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Task = t
	if t != nil {
		msgs, mErr := biamStore.MessagesFor(ctx, taskID)
		if mErr != nil {
			// Don't drop a corrupt-row signal — surface it so the
			// agent sees \"task_id valid, replay broken\" instead of
			// \"task_id valid, no replies yet\".
			out.ErrorReason = fmt.Sprintf("messages: %v", mErr)
		}
		out.Messages = msgs
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

func runTaskWait(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: task_id"), nil
	}
	timeoutS := int(req.GetFloat("timeout_s", 300))
	if timeoutS <= 0 {
		timeoutS = 300
	}
	if timeoutS > 3600 {
		timeoutS = 3600
	}

	start := time.Now()
	out := taskGetResult{BaseResult: BaseResult{Operation: "TaskWait", Engine: "biam"}}
	if biamStore == nil {
		out.ErrorReason = errBIAMNotInit.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()
	t, err := biamStore.WaitForTerminal(waitCtx, taskID, 250*time.Millisecond)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Task = t
	msgs, mErr := biamStore.MessagesFor(ctx, taskID)
	if mErr != nil {
		out.ErrorReason = fmt.Sprintf("messages: %v", mErr)
	}
	out.Messages = msgs
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

func runTaskList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := int(req.GetFloat("limit", 50))
	start := time.Now()
	out := taskListResult{BaseResult: BaseResult{Operation: "TaskList", Engine: "biam"}}
	if biamStore == nil {
		out.ErrorReason = errBIAMNotInit.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	tasks, err := biamStore.ListTasks(ctx, limit)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Tasks = tasks
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

var errBIAMNotInit = errors.New("biam: store not initialised; restart the server with `clawtool serve` to enable async dispatch")

// truncateForRender clamps prompt / message bodies to a single
// glanceable line for the human form. JSON shape gets the full body;
// only the textual render is trimmed.
func truncateForRender(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
