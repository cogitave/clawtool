package tui

import (
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

// TestOrchModel_DropsAlreadyTerminalSnapshot asserts the watch-
// socket snapshot pump doesn't flood the orchestrator with stale
// terminal tasks. Without this filter the sidebar would briefly
// show every historical row from biam.db before the post-grace
// reaper sweep cleared them — exactly the "shows 50 then drops to
// actives" glitch the operator reported.
func TestOrchModel_DropsAlreadyTerminalSnapshot(t *testing.T) {
	m := NewOrchestrator()
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "old-1", Status: biam.TaskDone}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "old-2", Status: biam.TaskFailed}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "old-3", Status: biam.TaskCancelled}})
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "old-4", Status: biam.TaskExpired}})
	if len(m.tasks) != 0 {
		t.Errorf("expected zero tasks (all snapshot rows already terminal), got %d", len(m.tasks))
	}
	if len(m.order) != 0 {
		t.Errorf("expected empty order, got %v", m.order)
	}

	// Live task lands as expected.
	m, _ = applyOrch(m, watchEventMsg{task: biam.Task{TaskID: "live", Status: biam.TaskActive}})
	if len(m.tasks) != 1 || m.order[0] != "live" {
		t.Errorf("live task should land; tasks=%d order=%v", len(m.tasks), m.order)
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
