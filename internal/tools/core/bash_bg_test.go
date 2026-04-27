package core

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func waitTaskTerminal(t *testing.T, id string, deadline time.Duration) BashTaskSnapshot {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		snap, ok := GetBashTask(id)
		if !ok {
			t.Fatalf("task %s missing from registry", id)
		}
		if snap.Status != BashTaskActive {
			return snap
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal status within %s", id, deadline)
	return BashTaskSnapshot{}
}

// TestBashBg_Success — short command runs to completion, status transitions
// active → done, stdout captured.
func TestBashBg_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash background mode is unix-only")
	}
	ResetBashTasksForTest()

	id, err := SubmitBackgroundBash(context.Background(),
		"printf hello-bg", t.TempDir(), 5_000)
	if err != nil {
		t.Fatalf("SubmitBackgroundBash: %v", err)
	}
	if id == "" {
		t.Fatal("empty task_id")
	}

	snap := waitTaskTerminal(t, id, 2*time.Second)
	if snap.Status != BashTaskDone {
		t.Errorf("status = %q, want %q", snap.Status, BashTaskDone)
	}
	if snap.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", snap.ExitCode)
	}
	if !strings.Contains(snap.Stdout, "hello-bg") {
		t.Errorf("stdout = %q, want to contain 'hello-bg'", snap.Stdout)
	}
	if snap.TimedOut {
		t.Error("timed_out = true, want false")
	}
}

// TestBashBg_Kill — long-running task is cancelled mid-flight via
// KillBashTask; status reflects `cancelled`.
func TestBashBg_Kill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash background mode is unix-only")
	}
	ResetBashTasksForTest()

	id, err := SubmitBackgroundBash(context.Background(),
		"sleep 30", t.TempDir(), 60_000)
	if err != nil {
		t.Fatalf("SubmitBackgroundBash: %v", err)
	}

	// Give the process a moment to actually spawn before killing.
	time.Sleep(100 * time.Millisecond)
	snap, ok := KillBashTask(id)
	if !ok {
		t.Fatal("KillBashTask returned ok=false for existing id")
	}
	_ = snap

	final := waitTaskTerminal(t, id, 2*time.Second)
	if final.Status != BashTaskCancelled {
		t.Errorf("status = %q, want %q", final.Status, BashTaskCancelled)
	}
}

// TestBashBg_Timeout — process exceeds the per-task timeout; status =
// failed with timed_out=true.
func TestBashBg_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash background mode is unix-only")
	}
	ResetBashTasksForTest()

	id, err := SubmitBackgroundBash(context.Background(),
		"sleep 30", t.TempDir(), 200) // 200ms hard timeout
	if err != nil {
		t.Fatalf("SubmitBackgroundBash: %v", err)
	}

	final := waitTaskTerminal(t, id, 3*time.Second)
	if final.Status != BashTaskFailed {
		t.Errorf("status = %q, want %q", final.Status, BashTaskFailed)
	}
	if !final.TimedOut {
		t.Error("timed_out = false, want true")
	}
}

// TestBashBg_GetUnknown — Get/Kill return ok=false for unknown ids
// without panicking.
func TestBashBg_GetUnknown(t *testing.T) {
	ResetBashTasksForTest()
	if _, ok := GetBashTask("nope"); ok {
		t.Error("GetBashTask returned ok=true for unknown id")
	}
	if _, ok := KillBashTask("nope"); ok {
		t.Error("KillBashTask returned ok=true for unknown id")
	}
}

// TestBashBg_ListNewestFirst — multiple tasks come back ordered by
// StartedAt descending (lazy insertion-sort in ListBashTasks).
func TestBashBg_ListNewestFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash background mode is unix-only")
	}
	ResetBashTasksForTest()

	first, _ := SubmitBackgroundBash(context.Background(), "printf one", "", 5_000)
	time.Sleep(10 * time.Millisecond)
	second, _ := SubmitBackgroundBash(context.Background(), "printf two", "", 5_000)
	time.Sleep(10 * time.Millisecond)
	third, _ := SubmitBackgroundBash(context.Background(), "printf three", "", 5_000)

	list := ListBashTasks(0)
	if len(list) != 3 {
		t.Fatalf("ListBashTasks len = %d, want 3", len(list))
	}
	if list[0].ID != third || list[1].ID != second || list[2].ID != first {
		t.Errorf("order = [%s, %s, %s], want [%s, %s, %s]",
			list[0].ID, list[1].ID, list[2].ID,
			third, second, first)
	}

	// Cleanup so the other tests don't see lingering active sleeps.
	ResetBashTasksForTest()
}
