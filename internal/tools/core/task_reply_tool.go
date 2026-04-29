// Package core — TaskReply MCP tool (the back-channel that closes
// the BIAM fan-in loop). When clawtool dispatches a heavy task to a
// peer agent (codex / gemini / opencode / claude) via SendMessage
// --bidi, the runner buffers the upstream's stdout into ONE 4 MiB
// result envelope. For audits / synthesis / multi-finding work the
// reply is too large for the caller's MCP response cap and clawtool
// has to spill it to a file.
//
// TaskReply lets the dispatched agent push structured replies back
// in chunks while it works:
//
//  1. Subprocess spawn injects CLAWTOOL_TASK_ID + CLAWTOOL_FROM_INSTANCE
//     env vars (see internal/agents/biam/runner.go).
//  2. The peer's MCP client has clawtool registered as a server (via
//     `clawtool agent claim <family>`), so it can call
//     mcp__clawtool__TaskReply directly.
//  3. Each call appends one envelope to the parent task. The caller's
//     TaskGet / TaskWait sees the chunks land in real time without
//     ever buffering a 300 KB blob into the wire response.
//
// Idempotent — duplicate idempotency_key inserts are silently
// dropped at the store layer. Read-only signing identity is the
// daemon's own (tasks aren't cross-host today; A2A wraps that
// later). Token gate matches the rest of the BIAM surface — when
// the store isn't initialised, the handler returns the standard
// errBIAMNotInit error so the caller knows to launch `clawtool
// serve` first.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type taskReplyResult struct {
	BaseResult
	TaskID    string `json:"task_id"`
	MessageID string `json:"message_id"`
	Kind      string `json:"kind"`
}

func (r taskReplyResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.TaskID)
	}
	return r.SuccessLine(fmt.Sprintf("appended %s envelope %s to task %s",
		r.Kind, shortID(r.MessageID), shortID(r.TaskID)))
}

// RegisterTaskReply wires the TaskReply tool. Idempotent.
func RegisterTaskReply(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"TaskReply",
			mcp.WithDescription(
				"Append a structured reply envelope to an existing BIAM task. "+
					"Used by dispatched peer agents (codex / gemini / opencode / claude) "+
					"to push chunked findings back to their caller without dumping a "+
					"giant blob through stdout. Read CLAWTOOL_TASK_ID + "+
					"CLAWTOOL_FROM_INSTANCE from the process env when running as a "+
					"dispatched peer. Each call appends one message; emit progress "+
					"chunks as kind=\"progress\" and the final answer as kind=\"result\".",
			),
			mcp.WithString("task_id", mcp.Required(),
				mcp.Description("Parent task UUID. Read from CLAWTOOL_TASK_ID env when running as a dispatched peer.")),
			mcp.WithString("body", mcp.Required(),
				mcp.Description("The reply text. Bounded only by the daemon's per-message cap (4 MiB).")),
			mcp.WithString("kind",
				mcp.Description("Envelope kind: \"progress\" (default — interim chunk), \"result\" (final answer), \"clarification\" (question back to caller), \"error\" (peer hit a failure).")),
			mcp.WithString("from_instance",
				mcp.Description("Override the envelope's `from` address. Read from CLAWTOOL_FROM_INSTANCE env when running as a dispatched peer; the daemon's own identity is used otherwise.")),
		),
		runTaskReply,
	)
}

func runTaskReply(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: task_id"), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: body"), nil
	}
	kindStr := strings.TrimSpace(req.GetString("kind", "progress"))
	fromInstance := strings.TrimSpace(req.GetString("from_instance", ""))

	start := time.Now()
	out := taskReplyResult{
		BaseResult: BaseResult{Operation: "TaskReply", Engine: "biam"},
		TaskID:     taskID,
		Kind:       kindStr,
	}

	if biamStore == nil {
		out.ErrorReason = errBIAMNotInit.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Validate kind — keeping the surface small so peers don't
	// invent ad-hoc values that downstream consumers haven't seen.
	var kind biam.EnvelopeKind
	switch kindStr {
	case "", "progress":
		kind = biam.KindReply
	case "result":
		kind = biam.KindResult
	case "clarification":
		kind = biam.KindClarification
	case "error":
		kind = biam.KindError
	default:
		out.ErrorReason = fmt.Sprintf("unknown kind %q (want progress | result | clarification | error)", kindStr)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	parent, err := biamStore.GetTask(ctx, taskID)
	if err != nil {
		out.ErrorReason = fmt.Sprintf("look up parent task: %v", err)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	if parent == nil {
		out.ErrorReason = fmt.Sprintf("task %s not found — provide the task_id returned by SendMessage --bidi", taskID)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	from := biam.Address{HostID: "local", InstanceID: fromInstance}
	if fromInstance == "" {
		from.InstanceID = parent.Agent
	}
	to := biam.Address{HostID: "local", InstanceID: parent.InitiatedBy}

	env := biam.NewEnvelope(from, to, taskID, kind, biam.Body{Text: body})

	// Inbound = true so the message is bookkept as a peer-pushed
	// reply (matching the inbound semantics for dispatch results
	// at runner.recordResult). The store hook fires WatchHub
	// broadcast so live watchers see the reply land.
	if err := biamStore.PutEnvelope(ctx, env, true); err != nil {
		out.ErrorReason = fmt.Sprintf("persist envelope: %v", err)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	out.MessageID = env.MessageID
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

// shortID renders the leading 8 chars of a UUID for compact lines.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
