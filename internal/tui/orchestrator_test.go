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

// TestOrchModel_WatchEventStampsTerminal confirms the terminal
// timestamp lands on the first transition to a terminal status.
func TestOrchModel_WatchEventStampsTerminal(t *testing.T) {
	m := NewOrchestrator()
	out, _ := m.Update(watchEventMsg{task: biam.Task{TaskID: "y", Status: biam.TaskDone}})
	got := out.(OrchModel)
	if got.tasks["y"].terminal.IsZero() {
		t.Error("terminal task didn't stamp the terminal timestamp")
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
