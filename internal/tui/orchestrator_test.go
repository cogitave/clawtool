package tui

import (
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
)

func TestOrchGridShape(t *testing.T) {
	cases := []struct {
		n          int
		wantC, wantR int
	}{
		{1, 1, 1},
		{2, 2, 1},
		{3, 3, 1},
		{4, 2, 2},
		{5, 3, 2},
		{6, 3, 2},
		{7, 3, 3},
		{9, 3, 3},
		{10, 4, 3},
		{12, 4, 3},
		{13, 4, 4},
	}
	for _, tc := range cases {
		gotC, gotR := orchGridShape(tc.n)
		if gotC != tc.wantC || gotR != tc.wantR {
			t.Errorf("orchGridShape(%d) = (%d,%d); want (%d,%d)",
				tc.n, gotC, gotR, tc.wantC, tc.wantR)
		}
	}
}

func TestOrchModel_TickSweepsClosedPanes(t *testing.T) {
	m := NewOrchestrator()
	// Pane that hit terminal long enough ago to be swept.
	m.panes["a"] = &orchPane{
		task:     biam.Task{TaskID: "a", Status: biam.TaskDone},
		terminal: time.Now().Add(-2 * orchPaneCloseAfter),
	}
	// Pane still active — must survive the sweep.
	m.panes["b"] = &orchPane{task: biam.Task{TaskID: "b", Status: biam.TaskActive}}
	// Pane terminal but within grace window.
	m.panes["c"] = &orchPane{
		task:     biam.Task{TaskID: "c", Status: biam.TaskDone},
		terminal: time.Now(),
	}

	out, _ := m.Update(orchTickMsg(time.Now()))
	got := out.(OrchModel)
	if _, ok := got.panes["a"]; ok {
		t.Error("pane 'a' should have been swept after grace window")
	}
	if _, ok := got.panes["b"]; !ok {
		t.Error("active pane 'b' was incorrectly swept")
	}
	if _, ok := got.panes["c"]; !ok {
		t.Error("terminal-but-still-fresh pane 'c' was prematurely swept")
	}
}

func TestOrchModel_WatchEventReplaceInPlace(t *testing.T) {
	m := NewOrchestrator()
	m.panes["x"] = &orchPane{
		task: biam.Task{TaskID: "x", Status: biam.TaskActive, MessageCount: 1},
	}

	msg := watchEventMsg{task: biam.Task{TaskID: "x", Status: biam.TaskActive, MessageCount: 5}}
	out, _ := m.Update(msg)
	got := out.(OrchModel)
	if got.panes["x"].task.MessageCount != 5 {
		t.Errorf("expected message_count=5 after update, got %d",
			got.panes["x"].task.MessageCount)
	}
	if !got.panes["x"].terminal.IsZero() {
		t.Errorf("active task shouldn't set terminal timestamp")
	}
}

func TestOrchModel_WatchEventStampsTerminal(t *testing.T) {
	m := NewOrchestrator()
	msg := watchEventMsg{task: biam.Task{TaskID: "y", Status: biam.TaskDone}}
	out, _ := m.Update(msg)
	got := out.(OrchModel)
	if got.panes["y"].terminal.IsZero() {
		t.Error("terminal task didn't stamp the terminal timestamp")
	}
}
