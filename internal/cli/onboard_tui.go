// internal/cli/onboard_tui.go — Bubble Tea wizard for `clawtool
// onboard`. Replaces the prior linear huh.NewForm(groups...) flow
// with a step-by-step wizard: each question gets its own focused
// viewport with a "Step X of Y" indicator, the rounded-box header
// stays pinned at the top, and the side-effect run phase renders
// as live progress inside the same alt-screen program.
//
// Why:
//
//   - Operator wanted bounded TUI ("vim/htop feel") instead of the
//     scroll-pollution we'd get from emitting a clear sequence and
//     dumping output below the prompt. tea.WithAltScreen() owns a
//     dedicated screen buffer; on exit the operator's terminal
//     state is restored exactly as it was.
//   - Stepwise progression makes the wizard feel structured. The
//     prior huh.NewForm rendered all groups in one continuous form;
//     the operator couldn't tell where they were in the sequence.
//
// Non-TTY / `--yes` invocations still run through the linear
// onboard() path so CI scripts, Dockerfiles, and the test harness
// keep their stable plain-text contract.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiPhase enumerates the top-level states of the onboard wizard.
type tuiPhase int

const (
	phaseSteps tuiPhase = iota // walking through wizard steps
	phaseRun                   // executing side-effects with live progress
	phaseDone                  // showing summary + next steps
)

// stepKind discriminates the run-phase queue entries so the
// dispatcher knows which dep callback to invoke.
type stepKind int

const (
	stepBridge stepKind = iota
	stepMCP
	stepDaemon
	stepIdentity
	stepSecrets
)

// runStep is one entry in the run-phase queue.
type runStep struct {
	kind   stepKind
	label  string // operator-visible label, e.g. "install bridge codex"
	target string // bridge family / host name; "" for daemon/identity/secrets
}

// logEntry is one rendered line in the run-phase log.
type logEntry struct {
	kind     string // "section" | "start" | "done" | "fail" | "skip" | "note"
	label    string
	detail   string
	duration time.Duration
}

// stepResultMsg is the tea.Msg that a queued runStep emits when its
// async dep callback returns. Carries the queue index so the
// dispatcher can correlate it with the originating step.
type stepResultMsg struct {
	idx    int
	err    error
	detail string // optional success suffix (e.g. claimed URL)
	skip   bool   // true when dep was nil → render as skip, not done
}

// finishedMsg signals all run-phase steps completed; the model
// transitions to phaseDone.
type finishedMsg struct{}

// wizardStep wraps one custom widget (Select / MultiSelect /
// Confirm) plus the apply hook that copies the widget's answer
// into onboardState. skipIf gates conditional steps (e.g. bridges
// question only shown when state.MissingBridges is non-empty).
//
// Widgets implement the stepWidget interface (Update / View /
// Done / Keybinds). On Done the wizard's outer model invokes apply
// to write back into onboardState, then advances to the next step.
type wizardStep struct {
	title  string
	widget stepWidget
	skipIf func(*onboardState) bool
	apply  func(*onboardState)
}

// onboardModel is the Bubble Tea model that drives the entire
// onboard wizard from welcome through summary.
type onboardModel struct {
	state *onboardState
	deps  onboardDeps

	width, height int

	phase    tuiPhase
	steps    []wizardStep
	stepIdx  int
	queue    []runStep
	queueIdx int

	log     []logEntry
	summary []SummaryRow

	style onboardStyles
	track func(string, map[string]any)

	phaseStartAt time.Time
	err          error
}

// newOnboardModel builds the wizard from onboardState + deps. The
// caller resolves these the same way the linear path does (host
// detection + dep wiring); we just consume them. startStep lets a
// resumed wizard skip ahead to the step the operator left off.
func newOnboardModel(state *onboardState, deps onboardDeps, track func(string, map[string]any)) *onboardModel {
	return newOnboardModelAt(state, deps, track, 0)
}

// newOnboardModelAt is the resume-aware constructor. startStep
// clamps to the step list bounds; out-of-range values reset to
// step 0 so a stale progress file (e.g. from a build with fewer
// steps) doesn't push the cursor off the end.
func newOnboardModelAt(state *onboardState, deps onboardDeps, track func(string, map[string]any), startStep int) *onboardModel {
	m := &onboardModel{
		state: state,
		deps:  deps,
		style: buildOnboardStyles(true), // we only run when TTY is true
		track: track,
		width: 80,
	}
	m.steps = buildWizardSteps(state)
	if startStep < 0 || startStep >= len(m.steps) {
		startStep = 0
	}
	m.stepIdx = startStep
	m.advanceStepCursor() // skip steps whose skipIf is already true
	return m
}

// buildWizardSteps materialises the step list. Each step wraps a
// minimal custom widget (Select / MultiSelect / Confirm — see
// onboard_widgets.go) instead of an embedded huh.Form. The
// widgets render every option every frame and integrate cleanly
// with our outer alt-screen layout (no internal viewports, no
// height clamps to fight, no "only cursor row visible" failure
// mode).
func buildWizardSteps(state *onboardState) []wizardStep {
	steps := []wizardStep{}

	// Step 1: Primary CLI — single-choice select.
	state.PrimaryCLI = primaryDefault(state.Found)
	primaryOpts := buildSelectOptions(primaryCLIOptionLabels(state.Found))
	primarySel := newSelectWidget(
		"Which CLI will you primarily use?",
		"Pick the agent you'll spend most of your time in. clawtool routes through that one as the primary; the others connect via MCP / bridge so you can dispatch across them.",
		primaryOpts, state.PrimaryCLI,
	)
	steps = append(steps, wizardStep{
		title:  "Primary CLI",
		widget: &selectAdapter{w: primarySel},
		apply: func(s *onboardState) {
			s.PrimaryCLI = primarySel.Value()
			// Smart default: pre-check the primary CLI's bridge
			// for install when it's missing and isn't claude-code.
			if s.PrimaryCLI != "" && s.PrimaryCLI != "claude-code" {
				for _, fam := range s.MissingBridges {
					if fam == s.PrimaryCLI {
						s.InstallBridges = []string{fam}
						break
					}
				}
			}
		},
	})

	// Step 2: Install missing bridges (conditional, multi-select).
	if len(state.MissingBridges) > 0 {
		opts := make([]widgetOption, 0, len(state.MissingBridges))
		for _, fam := range state.MissingBridges {
			opts = append(opts, widgetOption{Label: fam, Value: fam})
		}
		bridgesSel := newMultiSelectWidget(
			"Install missing bridges",
			"Toggle items with space; enter submits. Selected items run `clawtool bridge add <family>` after submit. Failures stay non-fatal. Your primary CLI's bridge is pre-checked.",
			opts, state.InstallBridges,
		)
		steps = append(steps, wizardStep{
			title:  "Install bridges",
			widget: &multiAdapter{w: bridgesSel},
			skipIf: func(s *onboardState) bool { return len(s.MissingBridges) == 0 },
			apply:  func(s *onboardState) { s.InstallBridges = bridgesSel.Values() },
		})
	}

	// Step 3: MCP host registration (conditional, multi-select).
	if len(state.MCPClaimable) > 0 {
		opts := make([]widgetOption, 0, len(state.MCPClaimable))
		for _, h := range state.MCPClaimable {
			opts = append(opts, widgetOption{Label: h, Value: h})
		}
		state.ClaimMCP = append([]string{}, state.MCPClaimable...)
		mcpSel := newMultiSelectWidget(
			"Register clawtool as an MCP server",
			"Toggle hosts with space; enter submits. Starts a single persistent local daemon (loopback HTTP + bearer auth) and points each selected host at it. Without this, hosts can't see clawtool tools.",
			opts, state.ClaimMCP,
		)
		steps = append(steps, wizardStep{
			title:  "MCP registration",
			widget: &multiAdapter{w: mcpSel},
			skipIf: func(s *onboardState) bool { return len(s.MCPClaimable) == 0 },
			apply:  func(s *onboardState) { s.ClaimMCP = mcpSel.Values() },
		})
	}

	// Step 4: Daemon.
	state.StartDaemon = true
	daemonConf := newConfirmWidget(
		"Start the persistent daemon now?",
		"`clawtool serve` is the single backend every host fans into. Default = on. Skip only if you'll start it later via `clawtool daemon start`.",
		"Start daemon", "Skip", true,
	)
	steps = append(steps, wizardStep{
		title:  "Daemon",
		widget: &confirmAdapter{w: daemonConf},
		apply:  func(s *onboardState) { s.StartDaemon = daemonConf.Value() },
	})

	// Step 5: Identity.
	identityConf := newConfirmWidget(
		"Create BIAM identity?",
		"Generates an Ed25519 keypair at ~/.config/clawtool/identity.ed25519 (mode 0600). Required for `clawtool send --async` and cross-host BIAM messaging.",
		"Generate", "Skip", true,
	)
	steps = append(steps, wizardStep{
		title:  "Identity",
		widget: &confirmAdapter{w: identityConf},
		apply:  func(s *onboardState) { s.CreateIdentity = identityConf.Value() },
	})

	// Step 6: Secrets store.
	state.InitSecrets = true
	secretsConf := newConfirmWidget(
		"Initialise the secrets store?",
		"Drops an empty 0600 secrets.toml at ~/.config/clawtool/secrets.toml so `clawtool source set-secret` writes without surprising you with a new file. Idempotent.",
		"Initialise", "Skip", true,
	)
	steps = append(steps, wizardStep{
		title:  "Secrets store",
		widget: &confirmAdapter{w: secretsConf},
		apply:  func(s *onboardState) { s.InitSecrets = secretsConf.Value() },
	})

	// Step 7: Telemetry.
	state.Telemetry = true
	telemetryConf := newConfirmWidget(
		"Anonymous telemetry (pre-1.0 default = on)",
		"Until v1.0.0 ships, telemetry is on by default — anonymous usage data tells us which paths get used. Emits ONLY: command/version/OS/arch/duration/exit code/error class/agent FAMILY/recipe names. NEVER: prompts, paths, file contents, secrets.",
		"Opt in", "No thanks", true,
	)
	steps = append(steps, wizardStep{
		title:  "Telemetry",
		widget: &confirmAdapter{w: telemetryConf},
		apply:  func(s *onboardState) { s.Telemetry = telemetryConf.Value() },
	})

	// Step 8: Project init.
	initConf := newConfirmWidget(
		"Run `clawtool init` after onboard?",
		"Project-level wizard that injects release-please / dependabot / commitlint / brain into the repo you're sitting in. Skip if you'd rather run it later in a different repo.",
		"Yes, set this repo up", "Skip", false,
	)
	steps = append(steps, wizardStep{
		title:  "Project init",
		widget: &confirmAdapter{w: initConf},
		apply:  func(s *onboardState) { s.RunInit = initConf.Value() },
	})

	return steps
}

// buildSelectOptions converts a [][2]string list of (label, value)
// pairs to widgetOption. Helper to keep buildWizardSteps tight.
func buildSelectOptions(pairs [][2]string) []widgetOption {
	out := make([]widgetOption, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, widgetOption{Label: p[0], Value: p[1]})
	}
	return out
}

// primaryCLIOptionLabels mirrors primaryCLIOptions but returns
// (label, value) pairs for the custom selectWidget instead of
// huh.Option[string].
func primaryCLIOptionLabels(found map[string]bool) [][2]string {
	families := []string{"claude-code", "codex", "gemini", "opencode", "hermes"}
	out := [][2]string{}
	// Detected first.
	for _, fam := range families {
		key := fam
		if fam == "claude-code" {
			key = "claude"
		}
		if found[key] {
			out = append(out, [2]string{fam + " (✓ detected)", fam})
		}
	}
	for _, fam := range families {
		key := fam
		if fam == "claude-code" {
			key = "claude"
		}
		if !found[key] {
			out = append(out, [2]string{fam, fam})
		}
	}
	out = append(out, [2]string{"none / decide later", ""})
	return out
}

// advanceStepCursor walks the step cursor forward past any steps
// whose skipIf hook reports they should be hidden in the current
// state. Used both at construction (to skip step 0 if conditional)
// and after each step completion.
func (m *onboardModel) advanceStepCursor() {
	for m.stepIdx < len(m.steps) {
		s := m.steps[m.stepIdx]
		if s.skipIf != nil && s.skipIf(m.state) {
			m.stepIdx++
			continue
		}
		return
	}
}

// Init kicks off the wizard. Custom widgets don't need an Init cmd
// (they're synchronous renderers) so we just defend against the
// edge case where the step list is empty.
func (m *onboardModel) Init() tea.Cmd {
	if m.stepIdx >= len(m.steps) {
		return m.startRunPhase()
	}
	return nil
}

// Update routes incoming msgs to the current phase: form during
// phaseSteps, step-result handler during phaseRun, no-op during
// phaseDone (operator presses any key to exit).
func (m *onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Custom widgets don't need WindowSize forwarding —
		// they render every row at natural size, the surrounding
		// card grows to fit, the body container's Height absorbs
		// slack to push the footer to the bottom.
		return m, nil

	case tea.KeyMsg:
		// Global quit. Esc/Ctrl-C exit cleanly.
		if msg.String() == "ctrl+c" {
			m.err = errors.New("interrupted")
			return m, tea.Quit
		}
		if m.phase == phaseDone {
			// Operator dismisses the summary screen with any
			// key (enter / q / esc — all quit alt-screen).
			return m, tea.Quit
		}

	case stepResultMsg:
		return m.handleStepResult(msg)

	case finishedMsg:
		m.phase = phaseDone
		return m, nil
	}

	if m.phase == phaseSteps {
		return m.updateStep(msg)
	}
	return m, nil
}

// updateStep forwards the msg to the active widget. If the widget
// reports Done (operator pressed enter), apply the answer back to
// onboardState, persist progress, and advance to the next step.
// When all steps are exhausted, transition to the run phase.
func (m *onboardModel) updateStep(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.stepIdx >= len(m.steps) {
		return m, m.startRunPhase()
	}
	step := m.steps[m.stepIdx]
	w, cmd := step.widget.Update(msg)
	m.steps[m.stepIdx].widget = w
	if w.Done() {
		if step.apply != nil {
			step.apply(m.state)
		}
		m.stepIdx++
		m.advanceStepCursor()
		_ = saveOnboardProgress(m.stepIdx, m.state, versionShortForOnboard())
		if m.stepIdx >= len(m.steps) {
			return m, m.startRunPhase()
		}
		return m, nil
	}
	return m, cmd
}

// startRunPhase builds the run queue from finalized state and emits
// the first step's command. Returns a tea.Cmd because the caller is
// driving the model from inside Update.
func (m *onboardModel) startRunPhase() tea.Cmd {
	m.track("clawtool.onboard", map[string]any{
		"event_kind": "host_detect",
		"agent":      m.state.PrimaryCLI,
	})
	m.phase = phaseRun
	m.queue = m.buildRunQueue()
	if len(m.queue) == 0 {
		return func() tea.Msg { return finishedMsg{} }
	}
	m.queueIdx = 0
	m.appendSection(sectionFor(m.queue[0].kind))
	m.appendStart(m.queue[0].label)
	m.phaseStartAt = time.Now()
	return m.dispatchStep(0)
}

// buildRunQueue lowers the captured wizard answers into the linear
// list of side-effect steps. Mirrors the dispatcher in onboard()
// (the linear path) so both code paths execute the same operations
// in the same order.
func (m *onboardModel) buildRunQueue() []runStep {
	q := []runStep{}
	for _, fam := range m.state.InstallBridges {
		q = append(q, runStep{kind: stepBridge, label: fmt.Sprintf("install bridge %s", fam), target: fam})
	}
	for _, h := range m.state.ClaimMCP {
		q = append(q, runStep{kind: stepMCP, label: fmt.Sprintf("register %s", h), target: h})
	}
	if m.state.StartDaemon {
		q = append(q, runStep{kind: stepDaemon, label: "start persistent daemon"})
	}
	if m.state.CreateIdentity {
		q = append(q, runStep{kind: stepIdentity, label: "generate BIAM Ed25519 keypair"})
	}
	if m.state.InitSecrets {
		q = append(q, runStep{kind: stepSecrets, label: "initialise empty secrets.toml"})
	}
	return q
}

// dispatchStep returns a tea.Cmd that runs the indexed step's dep
// callback off the main goroutine and emits a stepResultMsg when it
// completes.
func (m *onboardModel) dispatchStep(idx int) tea.Cmd {
	step := m.queue[idx]
	deps := m.deps
	return func() tea.Msg {
		switch step.kind {
		case stepBridge:
			err := deps.bridgeAdd(step.target)
			return stepResultMsg{idx: idx, err: err}
		case stepMCP:
			if deps.claimMCPHost == nil {
				return stepResultMsg{idx: idx, skip: true, detail: "not wired (test build?)"}
			}
			url, err := deps.claimMCPHost(step.target)
			return stepResultMsg{idx: idx, err: err, detail: url}
		case stepDaemon:
			if deps.ensureDaemon == nil {
				return stepResultMsg{idx: idx, skip: true}
			}
			url, err := deps.ensureDaemon()
			return stepResultMsg{idx: idx, err: err, detail: url}
		case stepIdentity:
			err := deps.createIdentity()
			return stepResultMsg{idx: idx, err: err, detail: "~/.config/clawtool/identity.ed25519, mode 0600"}
		case stepSecrets:
			if deps.initSecrets == nil {
				return stepResultMsg{idx: idx, skip: true}
			}
			err := deps.initSecrets()
			return stepResultMsg{idx: idx, err: err, detail: "~/.config/clawtool/secrets.toml, mode 0600"}
		}
		return stepResultMsg{idx: idx, err: fmt.Errorf("unknown step kind")}
	}
}

// handleStepResult records the most-recent step's outcome, advances
// the queue, and either dispatches the next step or transitions to
// phaseDone via finishedMsg.
func (m *onboardModel) handleStepResult(msg stepResultMsg) (tea.Model, tea.Cmd) {
	step := m.queue[msg.idx]
	dur := time.Since(m.phaseStartAt)
	switch {
	case msg.skip:
		m.appendSkip(msg.detail, dur)
		m.summary = append(m.summary, SummaryRow{Label: summaryLabelFor(step), Outcome: "skip", Detail: msg.detail})
		m.trackOutcome(step, "skipped")
	case msg.err != nil:
		m.appendFail(msg.err.Error(), dur)
		m.summary = append(m.summary, SummaryRow{Label: summaryLabelFor(step), Outcome: "fail", Detail: msg.err.Error()})
		m.trackOutcome(step, "error")
	default:
		m.appendDone(msg.detail, dur)
		m.summary = append(m.summary, SummaryRow{Label: summaryLabelFor(step), Outcome: "ok", Detail: msg.detail})
		m.trackOutcome(step, "success")
	}

	m.queueIdx++
	if m.queueIdx >= len(m.queue) {
		// Mirror the linear path's tail: telemetry preference summary
		// row + onboarded marker + finish event.
		if m.state.Telemetry {
			m.summary = append(m.summary, SummaryRow{Label: "telemetry", Outcome: "ok", Detail: "opted in"})
		} else {
			m.summary = append(m.summary, SummaryRow{Label: "telemetry", Outcome: "skip", Detail: "opted out"})
		}
		_ = writeOnboardedMarker()
		// Wizard finished cleanly — drop the resume file so the
		// next `clawtool onboard` hits the "already onboarded"
		// guard, not the resume prompt.
		_ = clearOnboardProgress()
		m.track("clawtool.onboard", map[string]any{"event_kind": "finish", "outcome": "success"})
		return m, func() tea.Msg { return finishedMsg{} }
	}

	// New section header when we transition into a new step kind.
	prevKind := m.queue[msg.idx].kind
	nextKind := m.queue[m.queueIdx].kind
	if prevKind != nextKind {
		m.appendSection(sectionFor(nextKind))
	}
	m.appendStart(m.queue[m.queueIdx].label)
	m.phaseStartAt = time.Now()
	return m, m.dispatchStep(m.queueIdx)
}

// trackOutcome emits the per-step telemetry event. Mirrors the
// linear path so both flows feed the same funnel.
func (m *onboardModel) trackOutcome(step runStep, outcome string) {
	props := map[string]any{"outcome": outcome}
	switch step.kind {
	case stepBridge:
		props["event_kind"] = "bridge_install"
		props["bridge"] = step.target
	case stepMCP:
		props["event_kind"] = "mcp_claim"
		props["agent"] = step.target
	case stepDaemon:
		props["event_kind"] = "daemon_start"
	case stepIdentity:
		props["event_kind"] = "identity_create"
	case stepSecrets:
		props["event_kind"] = "secrets_init"
	}
	m.track("clawtool.onboard", props)
}

// summaryLabelFor lowers a runStep into the human label used in the
// closing summary checklist.
func summaryLabelFor(s runStep) string {
	switch s.kind {
	case stepBridge:
		return "bridge " + s.target
	case stepMCP:
		return "MCP " + s.target
	case stepDaemon:
		return "daemon"
	case stepIdentity:
		return "BIAM identity"
	case stepSecrets:
		return "secrets store"
	}
	return s.label
}

// sectionFor maps a stepKind to its section banner title. Mirrors
// the linear path's ux.Section() calls.
func sectionFor(k stepKind) string {
	switch k {
	case stepBridge:
		return "Bridges"
	case stepMCP:
		return "MCP host registration"
	case stepDaemon:
		return "Daemon"
	case stepIdentity:
		return "Identity"
	case stepSecrets:
		return "Secrets store"
	}
	return ""
}

// onboardFixedCardWidth and onboardFixedCardHeight set the constant
// silhouette of the step card. Every step renders inside the same
// rectangle so the wizard's frame stays put as the operator
// advances — no jarring resize from one step to the next. Sized
// for ~74-col terminals; the card auto-clamps narrower if the
// viewport is tighter.
const (
	onboardFixedCardWidth  = 70
	onboardFixedCardHeight = 18
)

// View renders the alt-screen payload as a responsive three-band
// layout that uses the full viewport: header pinned at the top,
// footer pinned at the bottom, body fills the gap. Width adapts to
// the terminal (no hard cap — the wizard expands on wide screens
// and contracts on narrow ones, while a soft floor of 60 cols
// keeps narrow terminals readable).
//
// Layout (using full viewport area):
//
//	HEADER (full width, pinned top)
//	──────────────────────────────────────
//
//	BODY (fills viewport - header - footer)
//	  Step indicator
//	  Progress dots
//	  ╭─────── form card (stretches) ──────╮
//	  │                                    │
//	  │   form contents                    │
//	  │                                    │
//	  ╰────────────────────────────────────╯
//
//	──────────────────────────────────────
//	FOOTER (full width, pinned bottom)
func (m *onboardModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "" // pre-WindowSizeMsg; nothing meaningful to render
	}

	// Outer margins: 1 col either side so content doesn't hug
	// the alt-screen edge. Top/bottom padding rolled into the
	// header / footer styles directly.
	contentW := m.width - 2
	if contentW < 60 {
		contentW = 60
	}

	header := m.renderHeader(contentW)
	footer := m.renderFooterCol(contentW)

	// Body fills viewport minus header + footer + the top
	// padding (2 rows) + bottom padding (1 row) the outer style
	// adds, plus 1 row breathing room either side of the body.
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 5
	if bodyH < 10 {
		bodyH = 10
	}

	var body string
	switch m.phase {
	case phaseSteps:
		body = m.renderStep(contentW, bodyH)
	case phaseRun:
		body = m.renderRunBody(contentW, bodyH)
	case phaseDone:
		body = m.renderDoneBody(contentW, bodyH)
	}

	// Stack: header → body (filled) → footer. No centring; the
	// body's Height() makes it consume the slack so footer pins
	// to the bottom. Top padding (2 rows) gives the wizard
	// breathing room above the header so it doesn't hug the
	// alt-screen top edge.
	stack := lipgloss.JoinVertical(lipgloss.Left,
		header,
		body,
		footer,
	)
	return lipgloss.NewStyle().Padding(2, 1, 1, 1).Render(stack)
}

// renderHeader renders the inline app banner: a 1-line monogram
// "logo" (clawtool brand mark in box-drawing) + tagline + credit
// + host-detection pills. Stays compact (~4 lines) so the wizard
// body owns the operator's vertical attention budget.
func (m *onboardModel) renderHeader(w int) string {
	monogram := m.style.headerTitle.Render("┏━╸  clawtool")
	version := m.style.dim.Render(fmt.Sprintf("v%s  ·  first-run setup wizard", versionShortForOnboard()))
	credit := m.style.dim.Render("from Cogitave  ·  @bahadirarda  ·  help@cogitave.com")

	families := []struct{ key, label string }{
		{"claude", "claude-code"},
		{"codex", "codex"},
		{"gemini", "gemini"},
		{"opencode", "opencode"},
		{"hermes", "hermes"},
	}
	var pills []string
	for _, f := range families {
		if m.state.Found[f.key] {
			pills = append(pills, m.style.tickOK.Render("●")+" "+f.label)
		} else {
			pills = append(pills, m.style.dim.Render("○ "+f.label))
		}
	}
	sep := m.style.dim.Render("  ·  ")
	pillRow := strings.Join(pills, sep)

	body := lipgloss.JoinVertical(lipgloss.Left,
		monogram+"   "+version,
		credit,
		"",
		pillRow,
	)
	return lipgloss.NewStyle().Width(w).PaddingLeft(2).Render(body)
}

// renderStep renders the active wizard step: indicator line +
// progress dots + form wrapped in a single rounded card. The card
// stretches to fill the available body height so the wizard
// occupies the full viewport (no scrollback feel) regardless of
// how short the form widget itself is.
func (m *onboardModel) renderStep(w, bodyH int) string {
	if m.stepIdx >= len(m.steps) {
		return ""
	}
	step := m.steps[m.stepIdx]
	cur := m.visibleStepNumber()
	total := m.totalVisibleSteps()

	indicator := m.style.dim.Render(fmt.Sprintf("Step %d of %d", cur, total)) +
		m.style.dim.Render("  ·  ") +
		m.style.sectionTitle.Render(step.title)

	dots := make([]string, total)
	for i := 1; i <= total; i++ {
		switch {
		case i < cur:
			dots[i-1] = m.style.tickOK.Render("●")
		case i == cur:
			dots[i-1] = m.style.headerTitle.Render("◉")
		default:
			dots[i-1] = m.style.dim.Render("○")
		}
	}
	progress := strings.Join(dots, " ")

	// Wrap the widget in a rounded-border card with a FIXED size
	// so every step renders the same visual silhouette — the
	// operator's eye doesn't have to re-locate the wizard's
	// frame each time it advances. Inside the card the widget's
	// view is centred both axes via lipgloss.Place so a 4-row
	// Confirm and a 12-row Select look equally polished.
	cardW := onboardFixedCardWidth
	if cardW > w-4 {
		cardW = w - 4
	}
	if cardW < 50 {
		cardW = 50
	}
	cardH := onboardFixedCardHeight
	// Padding(1, 3) eats 2 cols + 2 rows; border eats 2 cols + 2
	// rows. Inner content area is cardW-8 by cardH-4.
	innerW := cardW - 8
	innerH := cardH - 4
	if innerW < 30 {
		innerW = 30
	}
	if innerH < 6 {
		innerH = 6
	}
	centred := lipgloss.Place(innerW, innerH,
		lipgloss.Center, lipgloss.Center,
		step.widget.View(),
	)
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("212")).
		Padding(1, 3).
		Width(cardW).
		Height(cardH).
		Render(centred)

	body := lipgloss.JoinVertical(lipgloss.Left,
		indicator,
		"",
		progress,
		"",
		card,
	)
	return lipgloss.NewStyle().
		Width(w).
		Height(bodyH).
		PaddingLeft(2).
		Render(body)
}

// renderRunBody renders the run phase: indicator line + the
// accumulated phase log, no surrounding rounded box. The log
// already has its own per-line rhythm (✓/✗/· glyphs + section
// rules) which provides enough visual structure on its own.
func (m *onboardModel) renderRunBody(w, bodyH int) string {
	indicator := m.style.sectionTitle.Render("Setting things up …")
	body := lipgloss.JoinVertical(lipgloss.Left,
		indicator,
		"",
		m.renderRunLog(),
	)
	return lipgloss.NewStyle().
		Width(w).
		Height(bodyH).
		PaddingLeft(2).
		Render(body)
}

// renderDoneBody renders the post-finish view: indicator + summary
// checklist + next-steps. No outer box — the summary's own glyphs
// (✓ / · / ✗) carry the visual weight.
func (m *onboardModel) renderDoneBody(w, bodyH int) string {
	indicator := m.style.tickOK.Render("✓ All set.")
	body := lipgloss.JoinVertical(lipgloss.Left,
		indicator,
		"",
		m.renderSummary(),
	)
	return lipgloss.NewStyle().
		Width(w).
		Height(bodyH).
		PaddingLeft(2).
		Render(body)
}

// renderFooterCol renders the bottom hint line as dim text with
// bullet separators. Width-aligned to the column so it visually
// anchors the wizard. During phaseSteps the hint is widget-
// specific (Select shows different keys than MultiSelect or
// Confirm) so the footer asks the active widget what to advertise.
func (m *onboardModel) renderFooterCol(w int) string {
	var hint string
	switch m.phase {
	case phaseSteps:
		widgetHint := ""
		if m.stepIdx < len(m.steps) && m.steps[m.stepIdx].widget != nil {
			widgetHint = m.steps[m.stepIdx].widget.Keybinds()
		}
		parts := []string{}
		if widgetHint != "" {
			parts = append(parts, widgetHint)
		}
		parts = append(parts, "ctrl-c quit")
		hint = m.style.dim.Render(strings.Join(parts, "  ·  "))
	case phaseRun:
		hint = m.style.dim.Render(fmt.Sprintf("running %d/%d  ·  ctrl-c quit",
			m.queueIdx+1, len(m.queue)))
	case phaseDone:
		hint = m.style.dim.Render("press any key to exit")
	}
	return lipgloss.NewStyle().Width(w).PaddingLeft(2).Render(hint)
}
func (m *onboardModel) visibleStepNumber() int {
	n := 0
	for i := 0; i <= m.stepIdx && i < len(m.steps); i++ {
		s := m.steps[i]
		if s.skipIf != nil && s.skipIf(m.state) {
			continue
		}
		n++
	}
	return n
}

// totalVisibleSteps returns the count of steps the operator will
// actually see, after evaluating skipIf for each.
func (m *onboardModel) totalVisibleSteps() int {
	n := 0
	for _, s := range m.steps {
		if s.skipIf != nil && s.skipIf(m.state) {
			continue
		}
		n++
	}
	return n
}

// renderRunLog renders the accumulated phase log entries.
func (m *onboardModel) renderRunLog() string {
	var b strings.Builder
	for _, e := range m.log {
		switch e.kind {
		case "section":
			rule := m.style.dim.Render(strings.Repeat("─", max(20, m.width-4)))
			fmt.Fprintf(&b, "\n  %s\n  %s\n", m.style.sectionTitle.Render(e.label), rule)
		case "start":
			fmt.Fprintf(&b, "  %s %s\n", m.style.arrow.Render("→"), e.label)
		case "done":
			suffix := m.style.dim.Render(fmt.Sprintf("(%s)", e.duration.Round(time.Millisecond)))
			if e.detail != "" {
				suffix = m.style.dim.Render(fmt.Sprintf("(%s · %s)", e.duration.Round(time.Millisecond), e.detail))
			}
			fmt.Fprintf(&b, "  %s %s %s\n", m.style.tickOK.Render("✓"), e.label, suffix)
		case "fail":
			fmt.Fprintf(&b, "  %s %s\n", m.style.tickFail.Render("✗"), e.label)
			if e.detail != "" {
				fmt.Fprintf(&b, "    %s\n", m.style.tickFail.Render(e.detail))
			}
		case "skip":
			suffix := ""
			if e.detail != "" {
				suffix = "  " + m.style.dim.Render(e.detail)
			}
			fmt.Fprintf(&b, "  %s %s%s\n", m.style.dim.Render("·"), e.label, suffix)
		case "note":
			fmt.Fprintf(&b, "  %s %s\n", m.style.dim.Render("·"), m.style.dim.Render(e.label))
		}
	}
	return b.String()
}

// renderSummary renders the closing summary checklist + next-steps.
func (m *onboardModel) renderSummary() string {
	var b strings.Builder
	rule := m.style.dim.Render(strings.Repeat("─", max(20, m.width-4)))
	fmt.Fprintf(&b, "\n  %s\n  %s\n", m.style.sectionTitle.Render("Summary"), rule)
	for _, r := range m.summary {
		var marker string
		switch r.Outcome {
		case "ok":
			marker = m.style.tickOK.Render("✓")
		case "skip":
			marker = m.style.dim.Render("·")
		case "fail":
			marker = m.style.tickFail.Render("✗")
		default:
			marker = " "
		}
		detail := ""
		if r.Detail != "" {
			detail = "  " + m.style.dim.Render(r.Detail)
		}
		fmt.Fprintf(&b, "    %s %s%s\n", marker, r.Label, detail)
	}

	// Next steps panel.
	next := []string{}
	if m.state.PrimaryCLI != "" {
		next = append(next, fmt.Sprintf("Primary interface: %s", m.state.PrimaryCLI))
	}
	if m.state.RunInit {
		next = append(next, "clawtool init     drop project recipes (release-please / dependabot / brain) into this repo")
	}
	next = append(next,
		"clawtool send --list     see your callable agents",
		"clawtool overview        live state of daemon + active dispatches")
	fmt.Fprintf(&b, "\n  %s\n  %s\n", m.style.sectionTitle.Render("Next steps"), rule)
	for _, item := range next {
		fmt.Fprintf(&b, "    %s %s\n", m.style.bullet.Render("•"), item)
	}
	return b.String()
}

func (m *onboardModel) appendSection(title string) {
	m.log = append(m.log, logEntry{kind: "section", label: title})
}
func (m *onboardModel) appendStart(label string) {
	m.log = append(m.log, logEntry{kind: "start", label: label})
}
func (m *onboardModel) appendDone(detail string, dur time.Duration) {
	// Replace the trailing "start" entry with "done" so the log
	// reads as "✓ install bridge codex (123ms)" rather than two
	// lines (start + done).
	if n := len(m.log); n > 0 && m.log[n-1].kind == "start" {
		m.log[n-1] = logEntry{kind: "done", label: m.log[n-1].label, detail: detail, duration: dur}
		return
	}
	m.log = append(m.log, logEntry{kind: "done", detail: detail, duration: dur})
}
func (m *onboardModel) appendFail(reason string, dur time.Duration) {
	if n := len(m.log); n > 0 && m.log[n-1].kind == "start" {
		m.log[n-1] = logEntry{kind: "fail", label: m.log[n-1].label, detail: reason, duration: dur}
		return
	}
	m.log = append(m.log, logEntry{kind: "fail", detail: reason, duration: dur})
}
func (m *onboardModel) appendSkip(reason string, dur time.Duration) {
	if n := len(m.log); n > 0 && m.log[n-1].kind == "start" {
		m.log[n-1] = logEntry{kind: "skip", label: m.log[n-1].label, detail: reason, duration: dur}
		return
	}
	m.log = append(m.log, logEntry{kind: "skip", detail: reason, duration: dur})
}

// runOnboardTUI builds the model and runs it through a tea.Program
// configured with the alt-screen buffer. Returns the model's
// captured error (if any) so the caller can map it to the CLI exit
// code.
func runOnboardTUI(ctx context.Context, state *onboardState, deps onboardDeps, track func(string, map[string]any), startStep int) error {
	m := newOnboardModelAt(state, deps, track, startStep)
	prog := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	)
	final, err := prog.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(*onboardModel); ok && fm.err != nil {
		if errors.Is(fm.err, huh.ErrUserAborted) {
			return huh.ErrUserAborted
		}
		return fm.err
	}
	return nil
}

// max because Go's stdlib didn't ship a generic max until 1.21 and
// we keep this self-contained for the tests' minimal-build sake.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// keep lipgloss import even if unused after future edits — the
// model relies on it transitively through onboardStyles.
var _ = lipgloss.NewStyle
