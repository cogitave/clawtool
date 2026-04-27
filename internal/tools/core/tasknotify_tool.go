// Package core — TaskNotify MCP tool. Edge-triggered completion
// push that pairs with SendMessage(bidi=true). Subscribes to the
// in-process biam.Notifier so the caller wakes the instant ANY of
// the watched tasks reaches a terminal state — no SQLite poll, no
// external CLI hooks.
//
// Architecture: the runner publishes a *biam.Task to Notifier when
// it flips a row to a terminal state (see internal/agents/biam/
// runner.go). Here we register one channel per task_id, then
// `select` across all of them + the timeout context. First task
// wins; the rest stay subscribed until the caller polls them
// with TaskGet (their slot decays at next Publish or process exit).
//
// Already-terminal tasks: we eagerly check the store BEFORE
// blocking, so a TaskNotify call against a task that already
// finished returns immediately rather than waiting for a Publish
// that already happened.
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

// taskNotifyResult is the JSON envelope. Only the FIRST task that
// reaches a terminal state is reported; the operator polls the
// others via TaskGet if they care.
type taskNotifyResult struct {
	BaseResult
	WatchedIDs   []string        `json:"watched_ids"`
	FinishedID   string          `json:"finished_id,omitempty"`
	FinishedTask *biam.Task      `json:"finished_task,omitempty"`
	Messages     []biam.Envelope `json:"messages,omitempty"`
	TimedOut     bool            `json:"timed_out"`
}

func (r taskNotifyResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(strings.Join(r.WatchedIDs, ","))
	}
	var b strings.Builder
	if r.TimedOut {
		fmt.Fprintf(&b, "no terminal transition for %d task(s) within timeout\n",
			len(r.WatchedIDs))
		for _, id := range r.WatchedIDs {
			fmt.Fprintf(&b, "  - %s (still active)\n", id)
		}
		b.WriteByte('\n')
		b.WriteString(r.FooterLine("timed_out"))
		return b.String()
	}
	if r.FinishedTask != nil {
		fmt.Fprintf(&b, "task %s finished: %s · agent=%s\n",
			r.FinishedID, r.FinishedTask.Status, r.FinishedTask.Agent)
		if r.FinishedTask.LastMessage != "" {
			fmt.Fprintf(&b, "last: %s\n", r.FinishedTask.LastMessage)
		}
		for _, e := range r.Messages {
			fmt.Fprintf(&b, "─ %s · %s · %s\n",
				e.MessageID[:8], e.Kind, truncateForRender(e.Body.Text, 200))
		}
		// Surface the IDs still in flight so the caller can decide
		// whether to keep polling them or stop watching.
		var pending []string
		for _, id := range r.WatchedIDs {
			if id != r.FinishedID {
				pending = append(pending, id)
			}
		}
		if len(pending) > 0 {
			fmt.Fprintf(&b, "\nstill active: %s\n", strings.Join(pending, ", "))
		}
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine())
	return b.String()
}

const (
	taskNotifyDefaultTimeoutS = 600   // 10 min
	taskNotifyMaxTimeoutS     = 3600  // 1 hour
	taskNotifyMaxIDs          = 64
)

// RegisterTaskNotify wires the TaskNotify tool. Idempotent.
func RegisterTaskNotify(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"TaskNotify",
			mcp.WithDescription(
				"Block until ANY of the watched BIAM task_ids reaches a terminal "+
					"state, then return that task's snapshot + every message. "+
					"Cheaper than TaskWait when you have multiple tasks in flight: "+
					"one round-trip wakes you on the first finisher instead of "+
					"polling each one. Edge-triggered via the in-process notifier — "+
					"no SQLite poll. Tasks already terminal at call time return "+
					"immediately.",
			),
			mcp.WithArray("task_ids",
				mcp.Required(),
				mcp.Description("List of task UUIDs (max 64) to watch."),
				mcp.Items(map[string]any{"type": "string"}),
			),
			mcp.WithNumber("timeout_s",
				mcp.Description("Block ceiling in seconds. Default 600 (10 min); hard cap 3600.")),
		),
		runTaskNotify,
	)
}

func runTaskNotify(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ids, err := requireStringList(req, "task_ids")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(ids) == 0 {
		return mcp.NewToolResultError("task_ids must not be empty"), nil
	}
	if len(ids) > taskNotifyMaxIDs {
		return mcp.NewToolResultError(
			fmt.Sprintf("task_ids: max %d ids per call, got %d", taskNotifyMaxIDs, len(ids))), nil
	}
	timeoutS := int(req.GetFloat("timeout_s", float64(taskNotifyDefaultTimeoutS)))
	if timeoutS <= 0 {
		timeoutS = taskNotifyDefaultTimeoutS
	}
	if timeoutS > taskNotifyMaxTimeoutS {
		timeoutS = taskNotifyMaxTimeoutS
	}

	start := time.Now()
	out := taskNotifyResult{
		BaseResult: BaseResult{Operation: "TaskNotify", Engine: "biam"},
		WatchedIDs: ids,
	}
	if biamStore == nil {
		out.ErrorReason = errBIAMNotInit.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Subscribe FIRST so a Publish that races with our store check
	// doesn't slip through the gap. Order: subscribe → eager check
	// → block. If the eager check finds an already-terminal task,
	// we Cancel the subs and return.
	subs := make(map[string]*biam.Sub, len(ids))
	for _, id := range ids {
		subs[id] = biam.Notifier.Subscribe(id)
	}
	defer func() {
		for _, sub := range subs {
			sub.Cancel()
		}
	}()

	// Eager check — already-terminal task wins immediately.
	for _, id := range ids {
		t, err := biamStore.GetTask(ctx, id)
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		if t == nil {
			out.ErrorReason = fmt.Sprintf("task %q not found", id)
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		if t.Status.IsTerminal() {
			finishTaskNotify(ctx, &out, id, t, start)
			return resultOf(out), nil
		}
	}

	// Block on the first finisher. Use reflect.Select equivalent
	// via fan-in goroutine because Go's select doesn't take a
	// dynamic case slice. The fan-in goroutine forwards the first
	// publish onto `done`; subsequent ones are dropped.
	done := make(chan biam.Task, 1)
	for _, sub := range subs {
		go func(ch <-chan biam.Task) {
			select {
			case t := <-ch:
				select {
				case done <- t:
				default:
					// Already-finished — drop quietly.
				}
			case <-ctx.Done():
			}
		}(sub.Ch)
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutS)*time.Second)
	defer cancel()

	select {
	case t := <-done:
		finishTaskNotify(ctx, &out, t.TaskID, &t, start)
	case <-waitCtx.Done():
		out.TimedOut = true
		out.DurationMs = time.Since(start).Milliseconds()
	}
	return resultOf(out), nil
}

// finishTaskNotify hydrates the result from a task snapshot —
// status + every message persisted under task_id. Re-queries the
// store so the snapshot is consistent if the Publish raced with
// the row update.
func finishTaskNotify(ctx context.Context, out *taskNotifyResult, taskID string, t *biam.Task, start time.Time) {
	out.FinishedID = taskID
	out.FinishedTask = t
	if msgs, err := biamStore.MessagesFor(ctx, taskID); err == nil {
		out.Messages = msgs
	} else {
		// Don't suppress a corrupt-row signal — surface it.
		out.ErrorReason = fmt.Sprintf("messages: %v", err)
	}
	out.DurationMs = time.Since(start).Milliseconds()
}

// requireStringList plucks an array argument from req and returns
// its values as []string. mcp-go decodes arrays as []any, so we
// have to type-assert per element.
func requireStringList(req mcp.CallToolRequest, name string) ([]string, error) {
	raw := req.GetArguments()[name]
	if raw == nil {
		return nil, fmt.Errorf("missing required argument: %s", name)
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected array, got %T", name, raw)
	}
	out := make([]string, 0, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected string, got %T", name, i, v)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("%s[%d]: empty string", name, i)
		}
		out = append(out, s)
	}
	return out, nil
}

// _ keeps errors imported even when the eager-terminal branch is
// the only consumer; defensive against future refactors.
var _ = errors.New
