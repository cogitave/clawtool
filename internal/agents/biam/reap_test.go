package biam

import (
	"path/filepath"
	"testing"
	"time"
)

// TestReapStaleTasks_PendingOlderThanThreshold confirms pending rows
// past the cutoff flip to expired with the daemon-restart message.
// The store-level test bypasses the runner because the bug is
// orphaned rows from a *prior* daemon; the live runner never gets
// a chance to claim them, so the test must mirror that — write the
// row directly via CreateTask, advance no goroutine, then reap.
func TestReapStaleTasks_PendingOlderThanThreshold(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()

	if err := store.CreateTask(ctx, "fresh", "tester", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(ctx, "stale", "tester", "codex"); err != nil {
		t.Fatal(err)
	}
	// Backdate the "stale" row 5 minutes via a raw UPDATE. The
	// public API doesn't expose created_at writes by design;
	// tests get the privilege.
	old := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET created_at=? WHERE task_id=?`, old, "stale"); err != nil {
		t.Fatal(err)
	}

	n, err := store.ReapStaleTasks(ctx, time.Minute, 0)
	if err != nil {
		t.Fatalf("ReapStaleTasks: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row reaped, got %d", n)
	}

	stale, _ := store.GetTask(ctx, "stale")
	if stale == nil || stale.Status != TaskExpired {
		t.Errorf("stale row should be expired, got %+v", stale)
	}
	if stale.ClosedAt == nil {
		t.Errorf("expired row missing closed_at")
	}
	if stale.LastMessage == "" {
		t.Errorf("expired row missing last_message")
	}

	fresh, _ := store.GetTask(ctx, "fresh")
	if fresh == nil || fresh.Status != TaskPending {
		t.Errorf("fresh pending row should not be reaped, got %+v", fresh)
	}
}

func TestReapStaleTasks_ActiveOlderThanThreshold(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()

	if err := store.CreateTask(ctx, "running-fresh", "tester", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, "running-fresh", TaskActive, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(ctx, "running-stuck", "tester", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, "running-stuck", TaskActive, ""); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET created_at=? WHERE task_id=?`, old, "running-stuck"); err != nil {
		t.Fatal(err)
	}

	n, err := store.ReapStaleTasks(ctx, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 active row reaped, got %d", n)
	}
	stuck, _ := store.GetTask(ctx, "running-stuck")
	if stuck == nil || stuck.Status != TaskExpired {
		t.Errorf("stuck active row should be expired, got %+v", stuck)
	}
	fresh, _ := store.GetTask(ctx, "running-fresh")
	if fresh == nil || fresh.Status != TaskActive {
		t.Errorf("fresh active row should not be reaped, got %+v", fresh)
	}
}

// TestReapStaleTasks_LeavesTerminalRowsAlone confirms the reaper
// only touches non-terminal statuses. A previously expired or done
// row must not be re-touched (its closed_at would shift).
func TestReapStaleTasks_LeavesTerminalRowsAlone(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()

	if err := store.CreateTask(ctx, "done-old", "tester", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, "done-old", TaskDone, "all good"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-99 * time.Hour).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET created_at=? WHERE task_id=?`, old, "done-old"); err != nil {
		t.Fatal(err)
	}

	doneBefore, _ := store.GetTask(ctx, "done-old")
	closedBefore := doneBefore.ClosedAt

	n, err := store.ReapStaleTasks(ctx, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows reaped (terminal rows are off-limits), got %d", n)
	}
	doneAfter, _ := store.GetTask(ctx, "done-old")
	if doneAfter.Status != TaskDone {
		t.Errorf("done row mutated to %s", doneAfter.Status)
	}
	if doneAfter.LastMessage != "all good" {
		t.Errorf("done last_message changed: %q", doneAfter.LastMessage)
	}
	if doneAfter.ClosedAt == nil || closedBefore == nil || !doneAfter.ClosedAt.Equal(*closedBefore) {
		t.Errorf("done closed_at shifted")
	}
}

// TestReapStaleTasks_ZeroPendingThresholdReapsAll confirms the
// "treat every existing non-terminal row as orphan" mode works
// when the caller explicitly passes 0 — useful for offline
// recovery commands.
func TestReapStaleTasks_ZeroPendingThresholdReapsAll(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()

	if err := store.CreateTask(ctx, "p1", "tester", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(ctx, "p2", "tester", "gemini"); err != nil {
		t.Fatal(err)
	}

	n, err := store.ReapStaleTasks(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("zero threshold should reap every pending row, got %d", n)
	}
}
