package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/mark3labs/mcp-go/mcp"
)

// withTempBiamStore opens a fresh BIAM store under t.TempDir() and
// registers it as the process-wide singleton. Reverts on cleanup so
// other tests don't see leaked state.
func withTempBiamStore(t *testing.T) *biam.Store {
	t.Helper()
	prev := biamStore
	store, err := biam.OpenStore(filepath.Join(t.TempDir(), "biam.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	SetBiamStore(store)
	t.Cleanup(func() {
		_ = store.Close()
		SetBiamStore(prev)
		biam.Notifier.ResetForTest()
	})
	biam.Notifier.ResetForTest()
	return store
}

func mkNotifyReq(taskIDs []string, timeoutS int) mcp.CallToolRequest {
	args := map[string]any{
		"task_ids": toAnySlice(taskIDs),
	}
	if timeoutS > 0 {
		args["timeout_s"] = float64(timeoutS)
	}
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// TestTaskNotify_AlreadyTerminal — task already in done state when
// TaskNotify is called returns immediately via the eager-check path,
// not via Notifier (which is edge-triggered and missed the publish).
func TestTaskNotify_AlreadyTerminal(t *testing.T) {
	store := withTempBiamStore(t)
	ctx := context.Background()

	if err := store.CreateTask(ctx, "task-a", "test", "claude"); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.SetTaskStatus(ctx, "task-a", biam.TaskDone, "all done"); err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}

	res, err := runTaskNotify(ctx, mkNotifyReq([]string{"task-a"}, 5))
	if err != nil {
		t.Fatalf("runTaskNotify: %v", err)
	}
	if res.IsError {
		t.Fatalf("result is error: %+v", res)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "task-a finished") {
		t.Errorf("render missing 'task-a finished': %s", out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("render missing 'done': %s", out)
	}
}

// TestTaskNotify_PublishWakesCaller — task is active at call time,
// then transitions to done via SetTaskStatus + Notifier.Publish; the
// MCP handler must wake within the timeout.
func TestTaskNotify_PublishWakesCaller(t *testing.T) {
	store := withTempBiamStore(t)
	ctx := context.Background()

	if err := store.CreateTask(ctx, "task-b", "test", "codex"); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.SetTaskStatus(ctx, "task-b", biam.TaskActive, ""); err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = store.SetTaskStatus(ctx, "task-b", biam.TaskDone, "fin")
		// Mirror what runner.recordResult does after the row flip.
		if t, _ := store.GetTask(ctx, "task-b"); t != nil {
			biam.Notifier.Publish(*t)
		}
	}()

	start := time.Now()
	res, err := runTaskNotify(ctx, mkNotifyReq([]string{"task-b"}, 5))
	if err != nil {
		t.Fatalf("runTaskNotify: %v", err)
	}
	dur := time.Since(start)
	if dur > 2*time.Second {
		t.Errorf("TaskNotify slow: took %s, expected sub-second wake", dur)
	}
	if res.IsError {
		t.Fatalf("result is error: %+v", res)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "task-b finished") {
		t.Errorf("render missing finished marker: %s", out)
	}
}

// TestTaskNotify_RaceFirstFinisher — three tasks active, second one
// finishes first; TaskNotify reports task-2 and notes the others
// are still active.
func TestTaskNotify_RaceFirstFinisher(t *testing.T) {
	store := withTempBiamStore(t)
	ctx := context.Background()

	for _, id := range []string{"r1", "r2", "r3"} {
		if err := store.CreateTask(ctx, id, "test", "agent"); err != nil {
			t.Fatalf("CreateTask %s: %v", id, err)
		}
		if err := store.SetTaskStatus(ctx, id, biam.TaskActive, ""); err != nil {
			t.Fatalf("SetTaskStatus %s: %v", id, err)
		}
	}

	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = store.SetTaskStatus(ctx, "r2", biam.TaskDone, "winner")
		if tk, _ := store.GetTask(ctx, "r2"); tk != nil {
			biam.Notifier.Publish(*tk)
		}
	}()

	res, err := runTaskNotify(ctx, mkNotifyReq([]string{"r1", "r2", "r3"}, 5))
	if err != nil {
		t.Fatalf("runTaskNotify: %v", err)
	}
	if res.IsError {
		t.Fatalf("result is error: %+v", res)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "r2 finished") {
		t.Errorf("expected r2 winner: %s", out)
	}
	if !strings.Contains(out, "still active") {
		t.Errorf("expected 'still active' summary: %s", out)
	}
}

// TestTaskNotify_TimeoutWhenNobodyFinishes — every watched task stays
// active; TaskNotify must report timed_out=true within the bound.
func TestTaskNotify_TimeoutWhenNobodyFinishes(t *testing.T) {
	store := withTempBiamStore(t)
	ctx := context.Background()

	if err := store.CreateTask(ctx, "stuck", "test", "agent"); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.SetTaskStatus(ctx, "stuck", biam.TaskActive, ""); err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}

	// timeout_s minimum is 1 (we test the floor below); supply 1.
	req := mkNotifyReq([]string{"stuck"}, 1)
	start := time.Now()
	res, err := runTaskNotify(ctx, req)
	dur := time.Since(start)
	if err != nil {
		t.Fatalf("runTaskNotify: %v", err)
	}
	if dur < 800*time.Millisecond || dur > 2500*time.Millisecond {
		t.Errorf("TaskNotify duration = %s, want ~1s", dur)
	}
	if res.IsError {
		t.Fatalf("result is error: %+v", res)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "no terminal transition") {
		t.Errorf("render missing timeout marker: %s", out)
	}
}

// TestTaskNotify_RejectsUnknownID — pre-flight store lookup catches
// bogus task_ids before blocking, so the caller fails fast instead
// of waiting for a publish that never arrives.
func TestTaskNotify_RejectsUnknownID(t *testing.T) {
	withTempBiamStore(t)
	res, err := runTaskNotify(context.Background(), mkNotifyReq([]string{"does-not-exist"}, 5))
	if err != nil {
		t.Fatalf("runTaskNotify: %v", err)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "not found") {
		t.Errorf("expected not-found error in render: %s", out)
	}
}

// TestTaskNotify_RejectsEmptyArgs — task_ids must not be empty.
func TestTaskNotify_RejectsEmptyArgs(t *testing.T) {
	withTempBiamStore(t)

	res, err := runTaskNotify(context.Background(), mkNotifyReq(nil, 5))
	if err != nil {
		t.Fatalf("runTaskNotify: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for empty task_ids, got %+v", res)
	}
}

// mustRenderText walks the MCP CallToolResult content for the text
// payload (the rendered envelope). Tests use it to assert on the
// human-form lines.
func mustRenderText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}
