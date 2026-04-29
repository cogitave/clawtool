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

func TestDashboard_WatchClosedSchedulesReconnect(t *testing.T) {
	// watchClosedMsg should produce a non-nil cmd (the backoff
	// timer) — pre-fix it returned `m, nil` and the dashboard
	// stayed disconnected forever after a daemon restart.
	m := New(nil, nil)
	updated, cmd := m.Update(watchClosedMsg{reason: "test"})
	if cmd == nil {
		t.Fatal("watchClosedMsg returned nil cmd; reconnect was not scheduled")
	}
	dm := updated.(Model)
	if dm.watchBackoff != watchReconnectBaseDelay {
		t.Fatalf("backoff not advanced: want %v, got %v",
			watchReconnectBaseDelay, dm.watchBackoff)
	}
}

func TestDashboard_WatchReconnectMsgFiresSubscribe(t *testing.T) {
	// The reconnect tick should produce a subscribe cmd. We
	// can't intercept which cmd it is without running the
	// runtime, but a non-nil return value (and no panic) is
	// enough to lock the contract.
	m := New(nil, nil)
	_, cmd := m.Update(watchReconnectMsg{})
	if cmd == nil {
		t.Fatal("watchReconnectMsg produced nil cmd; subscribe was not re-fired")
	}
}

func TestDashboard_SuccessResetsBackoff(t *testing.T) {
	// After a watchClosedMsg advances the backoff, a successful
	// watchEventMsg should reset it to zero so the next blip
	// starts again from the base delay (not whatever the latest
	// disconnect cooked up).
	m := New(nil, nil)
	updated, _ := m.Update(watchClosedMsg{reason: "blip"})
	updated, _ = updated.(Model).Update(watchClosedMsg{reason: "blip2"})
	dm := updated.(Model)
	if dm.watchBackoff <= watchReconnectBaseDelay {
		t.Fatalf("backoff should have advanced past base, got %v", dm.watchBackoff)
	}
	updated, _ = dm.Update(watchEventMsg{task: biam.Task{TaskID: "x"}})
	dm = updated.(Model)
	if dm.watchBackoff != 0 {
		t.Fatalf("backoff not reset on success: got %v", dm.watchBackoff)
	}
}

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
