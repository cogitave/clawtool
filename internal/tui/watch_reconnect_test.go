package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

func TestNextWatchBackoff_ProgressionAndCap(t *testing.T) {
	// First failure: jump straight to base.
	if got := nextWatchBackoff(0); got != watchReconnectBaseDelay {
		t.Fatalf("first backoff: want %v, got %v", watchReconnectBaseDelay, got)
	}
	// Doubles.
	d := watchReconnectBaseDelay
	for i := 0; i < 4; i++ {
		next := nextWatchBackoff(d)
		want := d * 2
		if want > watchReconnectMaxDelay {
			want = watchReconnectMaxDelay
		}
		if next != want {
			t.Fatalf("step %d: want %v, got %v (prev %v)", i, want, next, d)
		}
		d = next
	}
	// Capped — once at the max, stays at the max.
	if got := nextWatchBackoff(watchReconnectMaxDelay); got != watchReconnectMaxDelay {
		t.Fatalf("cap: want %v, got %v", watchReconnectMaxDelay, got)
	}
	// Defensive: negative input behaves like zero (jumps to base).
	if got := nextWatchBackoff(-1 * time.Second); got != watchReconnectBaseDelay {
		t.Fatalf("neg input: want base, got %v", got)
	}
}

// Pre-collapse this file also exercised the dashboard model's
// reconnect path. The dashboard TUI was retired in v0.22.36 in
// favour of a single canonical `clawtool orchestrator` window;
// the orchestrator-side cases below cover the same lifecycle.

func TestOrchestrator_WatchClosedSchedulesReconnect(t *testing.T) {
	m := NewOrchestrator()
	// Resize first so the View() / refreshStreamForSelection
	// path doesn't panic on zero-sized viewport during Update.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, cmd := updated.(OrchModel).Update(watchClosedMsg{})
	if cmd == nil {
		t.Fatal("orchestrator: watchClosedMsg returned nil cmd; reconnect not scheduled")
	}
	om := updated.(OrchModel)
	if om.watchBackoff != watchReconnectBaseDelay {
		t.Fatalf("orchestrator: backoff want %v, got %v",
			watchReconnectBaseDelay, om.watchBackoff)
	}
	if om.err == nil {
		t.Fatal("orchestrator: err banner not set on disconnect")
	}
}

func TestOrchestrator_SuccessResetsBackoff(t *testing.T) {
	m := NewOrchestrator()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.(OrchModel).Update(watchClosedMsg{})
	updated, _ = updated.(OrchModel).Update(watchEventMsg{task: biam.Task{TaskID: "y"}})
	om := updated.(OrchModel)
	if om.watchBackoff != 0 {
		t.Fatalf("orchestrator: backoff not reset, got %v", om.watchBackoff)
	}
	if om.err != nil {
		t.Fatalf("orchestrator: err banner not cleared, got %v", om.err)
	}
}
