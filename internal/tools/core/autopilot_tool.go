// Package core — AutopilotAdd / AutopilotNext / AutopilotDone /
// AutopilotSkip / AutopilotList / AutopilotStatus MCP tools.
//
// MCP mirror of the `clawtool autopilot` CLI verbs. Same TOML
// store (~/.config/clawtool/autopilot/queue.toml), same wire shape;
// CLI and MCP are interchangeable surfaces for the self-direction
// backlog. Operators / agents can mix-and-match.
//
// Why the cross-disambiguation matters: the surface looks adjacent
// to SendMessage / Spawn / TaskWait, but it's solving a different
// problem. SendMessage dispatches to a peer agent (codex / gemini /
// opencode). Autopilot is the SAME agent picking up its own
// backlog. The descriptions explicitly call this out so a calling
// model doesn't reach for SendMessage when it should be calling
// AutopilotNext.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/autopilot"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// autopilotAddResult is the wire shape returned by AutopilotAdd.
type autopilotAddResult struct {
	BaseResult
	Item autopilot.Item `json:"item"`
}

func (r autopilotAddResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Item.ID)
	}
	return r.SuccessLine(r.Item.ID,
		fmt.Sprintf("priority=%d", r.Item.Priority),
		fmt.Sprintf("status=%s", r.Item.Status))
}

// autopilotNextResult carries the claimed item or `Empty=true` when
// the queue is drained. Agents key off Empty to terminate the loop.
type autopilotNextResult struct {
	BaseResult
	Item  *autopilot.Item `json:"item,omitempty"`
	Empty bool            `json:"empty"`
}

func (r autopilotNextResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	if r.Empty {
		return r.SuccessLine("(queue empty — agent should stop the loop)")
	}
	return r.SuccessLine(r.Item.ID,
		fmt.Sprintf("priority=%d", r.Item.Priority),
		"prompt="+truncateForRender(r.Item.Prompt, 120))
}

// autopilotDoneResult covers Complete / Skip — the verb is encoded
// in BaseResult.Operation.
type autopilotDoneResult struct {
	BaseResult
	Item autopilot.Item `json:"item"`
}

func (r autopilotDoneResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Item.ID)
	}
	return r.SuccessLine(r.Item.ID, "status="+string(r.Item.Status))
}

// autopilotListResult holds the filtered listing.
type autopilotListResult struct {
	BaseResult
	Items []autopilot.Item `json:"items"`
}

func (r autopilotListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d item(s)\n", len(r.Items))
	for _, it := range r.Items {
		fmt.Fprintf(&b, "  %s · %s · prio=%d · %s\n",
			it.ID, it.Status, it.Priority,
			truncateForRender(it.Prompt, 80))
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

// autopilotStatusResult holds the histogram.
type autopilotStatusResult struct {
	BaseResult
	Counts autopilot.Counts `json:"counts"`
	Path   string           `json:"path"`
}

func (r autopilotStatusResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	return r.SuccessLine("queue",
		fmt.Sprintf("pending=%d", r.Counts.Pending),
		fmt.Sprintf("in_progress=%d", r.Counts.InProgress),
		fmt.Sprintf("done=%d", r.Counts.Done),
		fmt.Sprintf("skipped=%d", r.Counts.Skipped),
		fmt.Sprintf("total=%d", r.Counts.Total))
}

// RegisterAutopilotTools wires the six tools onto the MCP server.
// Idempotent — calling twice replaces by name.
func RegisterAutopilotTools(s *server.MCPServer) {
	registerAutopilotAdd(s)
	registerAutopilotNext(s)
	registerAutopilotDone(s)
	registerAutopilotSkip(s)
	registerAutopilotList(s)
	registerAutopilotStatus(s)
}

func registerAutopilotAdd(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutopilotAdd",
		mcp.WithDescription(
			"Append a new pending item to the agent's self-direction backlog "+
				"(~/.config/clawtool/autopilot/queue.toml). The agent dequeues "+
				"items via AutopilotNext to keep working without operator "+
				"re-prompting. Use when the operator gives multiple tasks at "+
				"once, or when finishing one task surfaces follow-ups. "+
				"AutopilotAdd queues SELF-work; SendMessage dispatches to a "+
				"peer agent — pick AutopilotAdd when the same agent should "+
				"handle the work later, SendMessage when a different runtime "+
				"(codex / gemini / opencode) should handle it now.",
		),
		mcp.WithString("prompt", mcp.Required(),
			mcp.Description("Free-text prompt the agent will read when it dequeues this item.")),
		mcp.WithNumber("priority",
			mcp.Description("Higher dequeues first (default 0). Use sparingly — most queues are FIFO.")),
		mcp.WithString("note",
			mcp.Description("Optional note attached to the item (e.g. linked PR, blocking question).")),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		prompt, err := req.RequireString("prompt")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: prompt"), nil
		}
		priority := int(req.GetFloat("priority", 0))
		note := req.GetString("note", "")

		start := time.Now()
		out := autopilotAddResult{
			BaseResult: BaseResult{Operation: "AutopilotAdd", Engine: "autopilot"},
		}
		it, err := autopilot.Open().Add(prompt, priority, note)
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Item = it
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}

func registerAutopilotNext(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutopilotNext",
		mcp.WithDescription(
			"Atomically claim the highest-priority pending item from the "+
				"agent's self-direction backlog. Marks it in_progress and "+
				"returns the item, or returns empty=true when the queue is "+
				"drained. Use when you've finished a task and want to know "+
				"what to do next without re-prompting the operator. The agent "+
				"should call this in a loop after each task to keep working: "+
				"call AutopilotNext → do the work → call AutopilotDone → call "+
				"AutopilotNext again. When empty=true the agent ends the loop. "+
				"Concurrency: two parallel calls will not return the same item "+
				"(file-locked claim).",
		),
	)
	s.AddTool(tool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		out := autopilotNextResult{
			BaseResult: BaseResult{Operation: "AutopilotNext", Engine: "autopilot"},
		}
		it, ok, err := autopilot.Open().Claim()
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		if !ok {
			out.Empty = true
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Item = &it
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}

func registerAutopilotDone(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutopilotDone",
		mcp.WithDescription(
			"Mark a backlog item done after the agent has completed the work. "+
				"Pair with AutopilotNext: claim → do the work → call AutopilotDone "+
				"with the same id → call AutopilotNext for the next item. "+
				"Use AutopilotSkip instead when the agent decides the item "+
				"shouldn't be worked on (out-of-scope, blocked, superseded).",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("Item id returned by AutopilotNext or AutopilotAdd.")),
		mcp.WithString("note",
			mcp.Description("Optional completion note (e.g. PR url, summary).")),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: id"), nil
		}
		note := req.GetString("note", "")

		start := time.Now()
		out := autopilotDoneResult{
			BaseResult: BaseResult{Operation: "AutopilotDone", Engine: "autopilot"},
		}
		it, err := autopilot.Open().Complete(id, note)
		if err != nil {
			out.ErrorReason = err.Error()
			out.Item = it
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Item = it
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}

func registerAutopilotSkip(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutopilotSkip",
		mcp.WithDescription(
			"Drop a backlog item without finishing it. Marks it skipped so it "+
				"won't be re-claimed by AutopilotNext. Use when the operator "+
				"told the agent to abandon the item, or when the agent "+
				"discovered the work is no longer needed (already done, "+
				"superseded, out-of-scope). AutopilotDone is for completed "+
				"work; AutopilotSkip is for abandoned work.",
		),
		mcp.WithString("id", mcp.Required(),
			mcp.Description("Item id returned by AutopilotNext or AutopilotAdd.")),
		mcp.WithString("note",
			mcp.Description("Optional reason for skipping (recorded with the item).")),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: id"), nil
		}
		note := req.GetString("note", "")

		start := time.Now()
		out := autopilotDoneResult{
			BaseResult: BaseResult{Operation: "AutopilotSkip", Engine: "autopilot"},
		}
		it, err := autopilot.Open().Skip(id, note)
		if err != nil {
			out.ErrorReason = err.Error()
			out.Item = it
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Item = it
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}

func registerAutopilotList(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutopilotList",
		mcp.WithDescription(
			"Read-only snapshot of every backlog item, optionally filtered by "+
				"status (pending|in_progress|done|skipped). Use when the agent "+
				"or operator wants visibility into the backlog without claiming "+
				"anything — AutopilotNext is destructive (it marks in_progress), "+
				"AutopilotList is not. Pair with AutopilotStatus for histogram "+
				"counts.",
		),
		mcp.WithString("status",
			mcp.Description("Filter: pending | in_progress | done | skipped. Empty = all.")),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filter := strings.TrimSpace(req.GetString("status", ""))

		start := time.Now()
		out := autopilotListResult{
			BaseResult: BaseResult{Operation: "AutopilotList", Engine: "autopilot"},
		}
		items, err := autopilot.Open().List(autopilot.Status(filter))
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Items = items
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}

func registerAutopilotStatus(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutopilotStatus",
		mcp.WithDescription(
			"Histogram of the autopilot backlog: counts of pending / "+
				"in_progress / done / skipped / total plus the queue file "+
				"path. Read-only. Use to decide whether to keep dispatching "+
				"AutopilotNext, or to surface backlog health to the operator "+
				"(\"3 pending, 1 stuck in_progress\").",
		),
	)
	s.AddTool(tool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		q := autopilot.Open()
		out := autopilotStatusResult{
			BaseResult: BaseResult{Operation: "AutopilotStatus", Engine: "autopilot"},
			Path:       q.Path(),
		}
		c, err := q.Status()
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Counts = c
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}
