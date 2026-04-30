package cli

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestOnboardModel_BuildsAllSteps confirms newOnboardModel constructs
// the expected wizard step list when every conditional gate is open.
// Eight visible steps when bridges + MCP claims both apply.
func TestOnboardModel_BuildsAllSteps(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		MissingBridges: []string{"codex", "gemini"},
		MCPClaimable:   []string{"codex"},
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	if got := m.totalVisibleSteps(); got != 8 {
		t.Errorf("totalVisibleSteps = %d, want 8 (primary + bridges + mcp + daemon + identity + secrets + telemetry + init)", got)
	}
}

// TestOnboardModel_SkipsConditionalSteps confirms the bridges step
// drops out when MissingBridges is empty and the MCP step drops out
// when MCPClaimable is empty.
func TestOnboardModel_SkipsConditionalSteps(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true, "codex": true, "gemini": true, "opencode": true, "hermes": true},
		MissingBridges: nil, // nothing missing
		MCPClaimable:   nil, // nothing claimable
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	if got := m.totalVisibleSteps(); got != 6 {
		t.Errorf("totalVisibleSteps = %d, want 6 (primary + daemon + identity + secrets + telemetry + init)", got)
	}
}

// TestOnboardModel_BuildRunQueueOrder confirms the run-phase queue
// is assembled in the same order the linear path executes side
// effects: bridges → MCP → daemon → identity → secrets.
func TestOnboardModel_BuildRunQueueOrder(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		InstallBridges: []string{"codex", "gemini"},
		ClaimMCP:       []string{"codex"},
		StartDaemon:    true,
		CreateIdentity: true,
		InitSecrets:    true,
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	q := m.buildRunQueue()
	wantKinds := []stepKind{stepBridge, stepBridge, stepMCP, stepDaemon, stepIdentity, stepSecrets}
	if len(q) != len(wantKinds) {
		t.Fatalf("queue length = %d, want %d (queue: %+v)", len(q), len(wantKinds), q)
	}
	for i, want := range wantKinds {
		if q[i].kind != want {
			t.Errorf("queue[%d].kind = %v, want %v", i, q[i].kind, want)
		}
	}
}

// TestOnboardModel_StepResultMsg_AdvancesAndRecords confirms that a
// stepResultMsg from a completed step advances the queue cursor,
// appends a "done" / "fail" / "skip" log entry, and feeds the
// summary tracker.
func TestOnboardModel_StepResultMsg_AdvancesAndRecords(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		InstallBridges: []string{"codex"},
		StartDaemon:    true,
	}
	deps := onboardDeps{
		bridgeAdd:    func(string) error { return nil },
		ensureDaemon: func() (string, error) { return "http://127.0.0.1:9999", nil },
	}
	m := newOnboardModel(&state, deps, func(string, map[string]any) {})
	// buildWizardSteps sets InitSecrets=true as the secrets-step
	// default; turn it off here so the queue is exactly the two
	// steps this test wires (bridge + daemon).
	m.state.InitSecrets = false
	m.state.Telemetry = false
	m.stepIdx = len(m.steps) // skip wizard, jump straight to run phase
	m.startRunPhase()

	// First step is the codex bridge install. Simulate its
	// completion via stepResultMsg.
	if _, _ = m.handleStepResult(stepResultMsg{idx: 0}); len(m.summary) != 1 {
		t.Fatalf("summary should have 1 entry after first step; got %d", len(m.summary))
	}
	if got := m.summary[0]; got.Outcome != "ok" || got.Label != "bridge codex" {
		t.Errorf("summary[0] = %+v, want ok/bridge codex", got)
	}
	if m.queueIdx != 1 {
		t.Errorf("queueIdx = %d, want 1", m.queueIdx)
	}
	// Second step is daemon. Simulate its completion.
	model, _ := m.handleStepResult(stepResultMsg{idx: 1, detail: "http://127.0.0.1:9999"})
	if mm, ok := model.(*onboardModel); ok {
		// We expect a finishedMsg to be emitted; the model
		// stays in phaseRun until that message is processed.
		// Simulate the message arrival.
		mm.Update(finishedMsg{})
		if mm.phase != phaseDone {
			t.Errorf("after finishedMsg, phase = %v, want phaseDone", mm.phase)
		}
		// Telemetry summary row appended at finish.
		foundTelem := false
		for _, r := range mm.summary {
			if r.Label == "telemetry" {
				foundTelem = true
				break
			}
		}
		if !foundTelem {
			t.Errorf("missing telemetry summary row after finish; got %+v", mm.summary)
		}
	} else {
		t.Fatalf("handleStepResult should return *onboardModel")
	}
}

// TestOnboardModel_StepResultMsg_FailRecordedInSummary confirms an
// errored step renders as a fail row in the closing summary so the
// operator sees what didn't wire up.
func TestOnboardModel_StepResultMsg_FailRecordedInSummary(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		InstallBridges: []string{"codex"},
	}
	deps := onboardDeps{bridgeAdd: func(string) error { return errors.New("network down") }}
	m := newOnboardModel(&state, deps, func(string, map[string]any) {})
	m.stepIdx = len(m.steps)
	m.startRunPhase()
	m.handleStepResult(stepResultMsg{idx: 0, err: errors.New("network down")})
	if got := m.summary[0]; got.Outcome != "fail" {
		t.Errorf("summary[0].Outcome = %q, want fail; row = %+v", got.Outcome, got)
	}
	if !strings.Contains(m.summary[0].Detail, "network down") {
		t.Errorf("summary[0].Detail = %q, want substring 'network down'", m.summary[0].Detail)
	}
}

// TestOnboardModel_StepResultMsg_SkipRecordedInSummary confirms a
// skipped step (e.g. claimMCPHost dep was nil) renders as skip, not
// fail, so a test build's missing dep doesn't masquerade as breakage.
func TestOnboardModel_StepResultMsg_SkipRecordedInSummary(t *testing.T) {
	state := onboardState{
		Found:    map[string]bool{"claude": true, "codex": true},
		ClaimMCP: []string{"codex"},
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	m.stepIdx = len(m.steps)
	m.startRunPhase()
	m.handleStepResult(stepResultMsg{idx: 0, skip: true, detail: "not wired (test build?)"})
	if got := m.summary[0]; got.Outcome != "skip" {
		t.Errorf("summary[0].Outcome = %q, want skip", got.Outcome)
	}
}

// TestOnboardModel_View_ContainsHeaderAndStep confirms the rendered
// frame includes the rounded-box header AND the current step's
// title + step indicator. Exercises the View() pipeline end-to-end.
func TestOnboardModel_View_ContainsHeaderAndStep(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		MissingBridges: nil,
		MCPClaimable:   nil,
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	// Simulate window-size so View() renders.
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	out := m.View()
	// Logo + tagline: ASCII banner uses box-drawing chars; the
	// tagline text remains plain.
	if !strings.Contains(out, "first-run setup") {
		t.Errorf("View should contain header tagline; got: %q", out)
	}
	if !strings.Contains(out, "from Cogitave") {
		t.Errorf("View should contain attribution; got: %q", out)
	}
	if !strings.Contains(out, "help@cogitave.com") {
		t.Errorf("View should contain support email; got: %q", out)
	}
	// Inline step indicator: "Step X of Y · <Title>".
	if !strings.Contains(out, "Step 1 of") {
		t.Errorf("View should contain step indicator; got: %q", out)
	}
	if !strings.Contains(out, "Primary CLI") {
		t.Errorf("View should contain first step title 'Primary CLI'; got: %q", out)
	}
}

// TestOnboardModel_View_RunPhaseShowsLog confirms the run phase
// renders the accumulated log entries (sections + phase markers).
func TestOnboardModel_View_RunPhaseShowsLog(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		InstallBridges: []string{"codex"},
	}
	deps := onboardDeps{bridgeAdd: func(string) error { return nil }}
	m := newOnboardModel(&state, deps, func(string, map[string]any) {})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m.stepIdx = len(m.steps)
	m.startRunPhase()
	out := m.View()
	if !strings.Contains(out, "Bridges") {
		t.Errorf("run-phase View should show 'Bridges' section header; got: %q", out)
	}
	if !strings.Contains(out, "install bridge codex") {
		t.Errorf("run-phase View should show step label; got: %q", out)
	}
}

// TestOnboardModel_BackKey_DecrementsStep confirms that pressing
// "b" while on step 2 sends the cursor back to step 1 and resets
// the now-active widget's done flag so a fresh enter advances
// without short-circuiting.
func TestOnboardModel_BackKey_DecrementsStep(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		MissingBridges: nil,
		MCPClaimable:   nil,
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	// Manually advance to step 2 and pretend the widget at the
	// new step is done (the "we just advanced" state).
	m.stepIdx = 2

	// Press "b" — should decrement to step 1.
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	mm, ok := model.(*onboardModel)
	if !ok {
		t.Fatalf("Update should return *onboardModel; got %T", model)
	}
	if mm.stepIdx != 1 {
		t.Errorf("stepIdx = %d, want 1 after `b`", mm.stepIdx)
	}
	if mm.steps[mm.stepIdx].widget.Done() {
		t.Errorf("widget at step 1 should be Reset (done=false) after back-step; got done=true")
	}
}

// TestOnboardModel_BackKey_NoopAtStepZero confirms `b` on the
// first step is a no-op — there's nowhere to go back.
func TestOnboardModel_BackKey_NoopAtStepZero(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		MissingBridges: nil,
		MCPClaimable:   nil,
	}
	m := newOnboardModel(&state, onboardDeps{}, func(string, map[string]any) {})
	if m.stepIdx != 0 {
		t.Fatalf("test setup expects stepIdx=0; got %d", m.stepIdx)
	}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	mm := model.(*onboardModel)
	if mm.stepIdx != 0 {
		t.Errorf("stepIdx should stay 0 after `b` at first step; got %d", mm.stepIdx)
	}
}

// TestSummaryLabelFor confirms the lookup returns the operator-
// visible label used in the closing checklist.
func TestSummaryLabelFor(t *testing.T) {
	cases := []struct {
		s    runStep
		want string
	}{
		{runStep{kind: stepBridge, target: "codex"}, "bridge codex"},
		{runStep{kind: stepMCP, target: "gemini"}, "MCP gemini"},
		{runStep{kind: stepDaemon}, "daemon"},
		{runStep{kind: stepIdentity}, "BIAM identity"},
		{runStep{kind: stepSecrets}, "secrets store"},
	}
	for _, c := range cases {
		if got := summaryLabelFor(c.s); got != c.want {
			t.Errorf("summaryLabelFor(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}
