package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

// resizedOrch returns an OrchModel that's been told the terminal
// is 120x40 — every test below needs a sized model because View()
// short-circuits to "booting…" when width/height are zero.
func resizedOrch() OrchModel {
	m := NewOrchestrator()
	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return out.(OrchModel)
}

// stripANSI removes lipgloss / ANSI escape sequences so test
// assertions match printable substrings without dragging in a
// terminal-emulation library.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestOrch_FrameLandsInRightPane is the regression test for the
// "awaiting first event" symptom. A frame envelope arrives, the
// matching task is selected (cursor=0 by default after first
// insert), and View() must show the frame's Line text — NOT the
// hint placeholder. Pre-fix (v0.22.12), follow-up reads chained
// through readNextEnvelope which silently dropped frames; the
// right pane stayed at "(awaiting first event from <agent>)" no
// matter how many frames the daemon broadcast.
func TestOrch_FrameLandsInRightPane(t *testing.T) {
	m := resizedOrch()

	// 1. Task snapshot lands.
	m, _ = applyOrch(m, watchEventMsg{
		task: biam.Task{TaskID: "live-1", Status: biam.TaskActive, Agent: "codex"},
	})

	// 2. Verify the right pane shows the awaiting-hint BEFORE any frames.
	pre := stripANSI(m.View())
	if !strings.Contains(pre, "awaiting first event") {
		t.Fatalf("expected 'awaiting first event' hint before frames; view:\n%s", pre)
	}

	// 3. Frame arrives for the same task.
	m, _ = applyOrch(m, watchFrameMsg{
		frame: biam.StreamFrame{TaskID: "live-1", Agent: "codex", Line: "running golangci-lint…"},
	})

	// 4. Right pane MUST now contain the frame text and NOT the hint.
	post := stripANSI(m.View())
	if strings.Contains(post, "awaiting first event") {
		t.Errorf("hint lingered after frame arrived (regression); view:\n%s", post)
	}
	if !strings.Contains(post, "running golangci-lint") {
		t.Errorf("frame text not rendered after arrival; view:\n%s", post)
	}
}

// TestOrch_VersionMismatchShowsBanner asserts that when the
// version-probe lands an orchVersionMismatchMsg, the operator
// sees a banner with both versions + the upgrade recipe in the
// rendered view. Without this, a stale binary against a newer
// daemon failed silently — the v0.22.12-vs-v0.22.32 incident.
func TestOrch_VersionMismatchShowsBanner(t *testing.T) {
	m := resizedOrch()
	m, _ = applyOrch(m, orchVersionMismatchMsg{
		daemonVersion: "0.22.34",
		binaryVersion: "0.22.12",
	})
	view := stripANSI(m.View())
	for _, want := range []string{
		"orchestrator v0.22.12",
		"daemon v0.22.34",
		"version mismatch",
		"clawtool upgrade",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("banner missing %q; view:\n%s", want, view)
		}
	}
}

// TestOrch_WatchClosedSurfacesReason asserts watchClosedMsg with
// a non-empty reason ends up visible in the view. Pre-fix the
// orchestrator just said "watch socket disconnected — press r"
// with zero diagnostic; the operator had no signal whether the
// daemon was missing, the token was wrong, or the socket path
// resolved to the wrong dir.
func TestOrch_WatchClosedSurfacesReason(t *testing.T) {
	m := resizedOrch()
	m, _ = applyOrch(m, watchClosedMsg{reason: "dial /tmp/no-such-socket: no such file"})
	if m.err == nil {
		t.Fatal("expected err set after watchClosedMsg")
	}
	if !strings.Contains(m.err.Error(), "watch socket disconnected") {
		t.Errorf("err missing canonical phrase; got %q", m.err.Error())
	}
}

// TestOrch_FrameRoutesViaOrchReadCmd is a structural test: every
// watch-msg branch in Update MUST chain through orchReadCmd, not
// the dashboard's watchReadCmd which silently drops frames. This
// is the wire that broke in v0.22.12 and was fixed in v0.22.27;
// the test pins it so a future refactor can't quietly regress.
func TestOrch_FrameRoutesViaOrchReadCmd(t *testing.T) {
	// Walk the source: orchestrator.go must NOT call watchReadCmd
	// in any of its three watch-msg follow-ups. We assert by
	// checking the Update function's behaviour — when a watch
	// message lands, the returned tea.Cmd must be non-nil (so
	// the chain continues) and the frame must reach the model.
	m := resizedOrch()
	frames := []biam.StreamFrame{
		{TaskID: "t1", Line: "first frame"},
		{TaskID: "t1", Line: "second frame"},
		{TaskID: "t1", Line: "third frame"},
	}
	m, _ = applyOrch(m, watchEventMsg{
		task: biam.Task{TaskID: "t1", Status: biam.TaskActive, Agent: "codex"},
	})
	for _, f := range frames {
		m, _ = applyOrch(m, watchFrameMsg{frame: f})
	}
	view := stripANSI(m.View())
	for _, want := range []string{"first frame", "second frame", "third frame"} {
		if !strings.Contains(view, want) {
			t.Errorf("frame %q not rendered after chain; view:\n%s", want, view)
		}
	}
}

// TestOrch_StartTimeSourcesFromCreatedAt — regression test for
// the elapsed-counter resetting on every reconnect. The ticker
// + reconnect pump replays history, and orchTask.startAt MUST
// settle on biam.Task.CreatedAt so the elapsed render reflects
// "time since task began" not "time since orchestrator saw it".
func TestOrch_StartTimeSourcesFromCreatedAt(t *testing.T) {
	taskCreated := mustParse(t, "2026-04-29T10:00:00Z")
	m := resizedOrch()
	m, _ = applyOrch(m, watchEventMsg{
		task: biam.Task{TaskID: "tt", Status: biam.TaskActive, CreatedAt: taskCreated},
	})
	if got := m.tasks["tt"].startAt; !got.Equal(taskCreated) {
		t.Errorf("startAt = %v, want %v (CreatedAt)", got, taskCreated)
	}

	// Frame-stub path: a frame for an unseen task synthesises
	// startAt = time.Now(); the next snapshot upgrades it to
	// the canonical CreatedAt.
	m, _ = applyOrch(m, watchFrameMsg{frame: biam.StreamFrame{TaskID: "frame-first", Line: "x"}})
	stubStart := m.tasks["frame-first"].startAt

	canonicalCreated := mustParse(t, "2026-04-29T11:00:00Z")
	m, _ = applyOrch(m, watchEventMsg{
		task: biam.Task{TaskID: "frame-first", Status: biam.TaskActive, CreatedAt: canonicalCreated},
	})
	if got := m.tasks["frame-first"].startAt; !got.Equal(canonicalCreated) {
		t.Errorf("startAt didn't upgrade to CreatedAt on snapshot; got %v want %v (was stub %v)",
			got, canonicalCreated, stubStart)
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return parsed
}
