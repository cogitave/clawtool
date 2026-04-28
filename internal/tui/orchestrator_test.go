package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
)

// TestOrchModel_WatchEventInsertsTask asserts a new Task envelope
// creates an entry in the tasks map + the order slice.
func TestOrchModel_WatchEventInsertsTask(t *testing.T) {
	m := NewOrchestrator()
	msg := watchEventMsg{task: biam.Task{TaskID: "abc", Status: biam.TaskActive, Agent: "codex"}}
	out, _ := m.Update(msg)
	got := out.(OrchModel)
	if _, ok := got.tasks["abc"]; !ok {
		t.Fatal("expected task abc to be inserted")
	}
	if len(got.order) != 1 || got.order[0] != "abc" {
		t.Errorf("expected order=[abc], got %v", got.order)
	}
}

// TestOrchModel_WatchEventStampsTerminalOnTransition confirms the
// terminal timestamp lands when a LIVE task transitions to a
// terminal state during this orchestrator session. Tasks that
// arrive already-terminal (snapshot from the watch socket on
// connect) are dropped, so the stamp test inserts the task as
// active first, then sends the terminal transition.
func TestOrchModel_WatchEventStampsTerminalOnTransition(t *testing.T) {
	m := NewOrchestrator()
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "y", Status: biam.TaskActive}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "y", Status: biam.TaskDone}})
	if m.tasks["y"].terminal.IsZero() {
		t.Error("terminal transition didn't stamp the terminal timestamp")
	}
}

// TestOrchModel_TerminalSnapshotsLandInDoneTab asserts already-
// terminal task snapshots from the watch-socket replay go into the
// Done tab and are HIDDEN on the Active tab — the operator can
// browse history without it flooding live work. Inverse of the
// "shows 50 then drops to actives" glitch.
func TestOrchModel_TerminalSnapshotsLandInDoneTab(t *testing.T) {
	m := NewOrchestrator()
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "old-1", Status: biam.TaskDone}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "old-2", Status: biam.TaskFailed}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "live", Status: biam.TaskActive}})

	if len(m.tasks) != 3 {
		t.Errorf("expected 3 tasks tracked, got %d", len(m.tasks))
	}
	// Active tab: only the live row.
	m.tab = orchTabActive
	if got := m.visibleIDs(); len(got) != 1 || got[0] != "live" {
		t.Errorf("Active tab should show only live, got %v", got)
	}
	// Done tab: the two terminal rows.
	m.tab = orchTabDone
	got := m.visibleIDs()
	if len(got) != 2 {
		t.Fatalf("Done tab should show 2 terminal rows, got %d (%v)", len(got), got)
	}
	want := map[string]bool{"old-1": true, "old-2": true}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected id in Done tab: %q", id)
		}
	}
	if m.activeCount() != 1 || m.doneCount() != 2 {
		t.Errorf("counts mismatch: active=%d done=%d", m.activeCount(), m.doneCount())
	}
}

// TestOrchModel_TickSweepsClosedPanes asserts the periodic tick
// drops tasks past their grace window.
func TestOrchModel_TickSweepsClosedPanes(t *testing.T) {
	m := NewOrchestrator()
	m.tasks["a"] = &orchTask{
		task:     biam.Task{TaskID: "a", Status: biam.TaskDone},
		terminal: time.Now().Add(-2 * orchPaneCloseAfter),
		startAt:  time.Now().Add(-time.Minute),
	}
	m.tasks["b"] = &orchTask{
		task:    biam.Task{TaskID: "b", Status: biam.TaskActive},
		startAt: time.Now(),
	}
	m.tasks["c"] = &orchTask{
		task:     biam.Task{TaskID: "c", Status: biam.TaskDone},
		terminal: time.Now(),
		startAt:  time.Now().Add(-30 * time.Second),
	}
	m.order = []string{"a", "b", "c"}

	out, _ := m.Update(orchTickMsg(time.Now()))
	got := out.(OrchModel)
	if _, ok := got.tasks["a"]; ok {
		t.Error("task 'a' should have been swept after grace window")
	}
	if _, ok := got.tasks["b"]; !ok {
		t.Error("active task 'b' was incorrectly swept")
	}
	if _, ok := got.tasks["c"]; !ok {
		t.Error("terminal-but-still-fresh task 'c' was prematurely swept")
	}
}

// TestOrchModel_WatchFrameAppendsToTask confirms a stream frame
// lands in the matching task's ringbuffer.
func TestOrchModel_WatchFrameAppendsToTask(t *testing.T) {
	m := NewOrchestrator()
	// Seed with a task first.
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "z", Status: biam.TaskActive}})

	frame := biam.StreamFrame{TaskID: "z", Line: "hello world", TS: time.Now()}
	m, _ = applyOrch(m, watchFrameMsg{frame: frame})
	if got := len(m.tasks["z"].frames); got != 1 {
		t.Fatalf("expected 1 frame, got %d", got)
	}
	if m.tasks["z"].frames[0] != "hello world" {
		t.Errorf("frame line wrong: %q", m.tasks["z"].frames[0])
	}
}

// TestOrchModel_VisibleIDsRespectsTab confirms tab switch swaps the
// visible list without losing tasks. Cursor reset on tab switch
// happens via Update; this test exercises the lower-level helper.
func TestOrchModel_VisibleIDsRespectsTab(t *testing.T) {
	m := NewOrchestrator()
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "a", Status: biam.TaskActive}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "b", Status: biam.TaskDone}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "c", Status: biam.TaskActive}})

	m.tab = orchTabActive
	if ids := m.visibleIDs(); len(ids) != 2 {
		t.Errorf("Active tab visibleIDs = %v, want 2 entries", ids)
	}
	m.tab = orchTabDone
	if ids := m.visibleIDs(); len(ids) != 1 || ids[0] != "b" {
		t.Errorf("Done tab visibleIDs = %v, want [b]", ids)
	}
}

// TestOrchModel_OrderCappedOnSnapshotFlood confirms the orchestrator
// drops oldest tail entries past `orchOrderCap` so a reconnect to a
// daemon with thousands of historical rows in biam.db doesn't blow
// the model's memory or render budget. Newest-first insert pattern
// means dropped entries are the longest-untouched terminal tasks.
func TestOrchModel_OrderCappedOnSnapshotFlood(t *testing.T) {
	m := NewOrchestrator()
	for i := 0; i < orchOrderCap+50; i++ {
		m, _ = applyOrch(m, watchEventMsg{task: biam.Task{
			TaskID: fmt.Sprintf("t-%04d", i),
			Status: biam.TaskActive,
		}})
	}
	if got := len(m.order); got != orchOrderCap {
		t.Errorf("expected order length %d after flood, got %d", orchOrderCap, got)
	}
	if got := len(m.tasks); got != orchOrderCap {
		t.Errorf("expected tasks map size %d after flood, got %d", orchOrderCap, got)
	}
	// The MOST RECENT insert (t-0249) should still be present;
	// the OLDEST (t-0000) should have been evicted.
	if _, ok := m.tasks["t-0249"]; !ok {
		t.Errorf("most-recent task evicted")
	}
	if _, ok := m.tasks["t-0000"]; ok {
		t.Errorf("oldest task should have been evicted past cap")
	}
}

// TestOrchModel_FrameRingbufferCap confirms the ringbuffer doesn't
// grow past orchFrameRingMax.
func TestOrchModel_FrameRingbufferCap(t *testing.T) {
	m := NewOrchestrator()
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "p"}})
	for i := 0; i < orchFrameRingMax+50; i++ {
		m, _ = applyOrch(m, watchFrameMsg{frame: biam.StreamFrame{TaskID: "p", Line: "line"}})
	}
	if got := len(m.tasks["p"].frames); got != orchFrameRingMax {
		t.Errorf("expected ringbuffer cap=%d, got %d", orchFrameRingMax, got)
	}
}

// applyOrch is the test-side reducer — runs Update + asserts the
// returned model matches OrchModel.
func applyOrch(m OrchModel, msg interface{}) (OrchModel, interface{}) {
	out, cmd := m.Update(msg)
	return out.(OrchModel), cmd
}
