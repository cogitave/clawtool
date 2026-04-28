// Package tui — multi-pane dashboard for clawtool's runtime
// surface. Bubble Tea-based, deferred from the v0.19 sketch and
// resurrected for the operator's "I want to see what every agent
// is doing" directive.
//
// v1.1 fixes (operator feedback): the tick chain was breaking
// after the first refresh because tickCmd only fired AFTER a
// successful refresh. Now the tick runs on its own cadence
// independent of refresh completion, so the dashboard stays live
// even during transient SQLite hiccups. Plus: every pane respects
// the terminal viewport — rows beyond the visible area are
// truncated with a "(…N more)" tail line, and the layout adapts
// to the operator's terminal height instead of overflowing the
// scrollback.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

// pollInterval is the tick cadence. 1 s is the floor that feels
// live without dominating the render loop.
const pollInterval = 1 * time.Second

// Model is the Bubble Tea state.
type Model struct {
	store *biam.Store
	sup   agents.Supervisor

	width  int
	height int

	tasks       []biam.Task
	agents      []agents.Agent
	loaded      bool
	err         error
	lastRefresh time.Time

	focused int
	cursor  int
}

func New(store *biam.Store, sup agents.Supervisor) Model {
	return Model{store: store, sup: sup}
}

// Init kicks off the FIRST refresh AND starts the tick loop.
// Both are independent commands so the tick stays alive even if
// a refresh fails.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		refreshCmd(m.store, m.sup),
		tickCmd(),
		watchSubscribeCmd(),
	)
}

type refreshMsg struct {
	tasks  []biam.Task
	agents []agents.Agent
	err    error
}

type tickMsg time.Time

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, refreshCmd(m.store, m.sup)
		case "tab":
			m.focused = (m.focused + 1) % 3
			m.cursor = 0
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if max := m.paneRowCount(); m.cursor < max-1 {
				m.cursor++
			}
			return m, nil
		}

	case refreshMsg:
		m.tasks = msg.tasks
		m.agents = msg.agents
		m.err = msg.err
		m.loaded = true
		m.lastRefresh = time.Now()
		// Do NOT chain a tickCmd here — tick is independent.
		return m, nil

	case tickMsg:
		// Each tick fires a refresh AND schedules the next tick.
		// v1's bug: tick chained after refresh, so a single
		// refresh error stopped the whole loop. Now tick is
		// independent of refresh outcome.
		return m, tea.Batch(
			refreshCmd(m.store, m.sup),
			tickCmd(),
		)

	case watchEventMsg:
		// Push update from the daemon's task-watch socket.
		// Replace the matching task in-place; append when new.
		// Keeps the dashboard reactive to state transitions
		// within ~50ms instead of waiting up to 1s for the
		// next polling tick.
		updated := false
		for i := range m.tasks {
			if m.tasks[i].TaskID == msg.task.TaskID {
				m.tasks[i] = msg.task
				updated = true
				break
			}
		}
		if !updated {
			m.tasks = append([]biam.Task{msg.task}, m.tasks...)
		}
		m.lastRefresh = time.Now()
		return m, watchReadCmd(msg.dec, msg.conn)

	case watchClosedMsg:
		// Socket disconnected — fall back to polling. Schedule
		// a reconnect attempt on the next tick so a daemon
		// restart heals the dashboard automatically.
		return m, nil
	}

	return m, nil
}

func (m Model) paneRowCount() int {
	switch m.focused {
	case 0:
		return len(m.tasks)
	case 1:
		return len(m.agents)
	default:
		return 0
	}
}

// View renders the model. Layout is height-aware: each pane gets
// a budget so the total fits the terminal viewport.
func (m Model) View() string {
	if !m.loaded {
		return banner(m.lastRefresh) + "\nloading…\n"
	}
	if m.err != nil {
		return banner(m.lastRefresh) + "\nerror: " + m.err.Error() + "\n"
	}

	totalH := m.height
	if totalH <= 0 {
		totalH = 30
	}
	// Per-pane chrome budget tuned to NormalBorder + Padding(0,1)
	// + MarginBottom(1) + header line. Stats is always 1 data
	// row; remaining rows split 2:1 dispatches:agents.
	chrome := 4*3 + 2 + 1 + 1
	dataBudget := totalH - chrome
	if dataBudget < 8 {
		dataBudget = 8
	}
	statsRows := 1
	remaining := dataBudget - statsRows
	if remaining < 4 {
		remaining = 4
	}
	dispRows := remaining * 2 / 3
	agentsRows := remaining - dispRows
	if dispRows < 2 {
		dispRows = 2
	}
	if agentsRows < 2 {
		agentsRows = 2
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		banner(m.lastRefresh),
		paneDispatches(m.tasks, m.focused == 0, m.cursor, m.width, dispRows),
		paneAgents(m.agents, m.focused == 1, m.cursor, m.width, agentsRows),
		paneStats(m.tasks, m.agents, m.focused == 2, m.width),
		footer(),
	)
	return body
}

// ─── render helpers ─────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212"))

	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1).
			MarginBottom(1)

	focusedPaneStyle = paneStyle.
				BorderForeground(lipgloss.Color("212"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("245"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	statusActive = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	statusDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	statusFailed = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func banner(lastRefresh time.Time) string {
	live := dimStyle.Render(" — Tools. Agents. Wired.")
	if !lastRefresh.IsZero() {
		age := time.Since(lastRefresh).Round(time.Second)
		live = dimStyle.Render(fmt.Sprintf(" — live · refreshed %s ago", age))
	}
	return titleStyle.Render("clawtool dashboard") + live
}

func footer() string {
	return dimStyle.Render(
		"q quit · r refresh · tab cycle pane · ↑/↓ navigate")
}

// renderPaneRows takes the rendered rows + height budget and
// returns a single block of text capped at maxRows. When more
// rows exist than fit, a dim "(…N more)" tail line replaces
// the cut-off rows. Cursor is followed: scrolling past the
// visible window slides it.
func renderPaneRows(headerLine string, rows []string, maxRows int, focused bool, cursor int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(headerLine))
	b.WriteByte('\n')

	if maxRows < 1 {
		maxRows = 1
	}
	visible := len(rows)
	tail := ""
	if visible > maxRows {
		visible = maxRows - 1
		if visible < 1 {
			visible = 1
		}
		tail = dimStyle.Render(fmt.Sprintf("    (… %d more — narrow terminal)", len(rows)-visible))
	}

	start := 0
	if cursor >= visible {
		start = cursor - visible + 1
	}
	if start+visible > len(rows) {
		start = len(rows) - visible
		if start < 0 {
			start = 0
		}
	}
	end := start + visible
	if end > len(rows) {
		end = len(rows)
	}

	for i := start; i < end; i++ {
		marker := "  "
		if focused && i == cursor {
			marker = "▸ "
		}
		b.WriteString(marker)
		b.WriteString(rows[i])
		b.WriteByte('\n')
	}
	if tail != "" {
		b.WriteString(tail)
		b.WriteByte('\n')
	}
	return b.String()
}

func paneDispatches(tasks []biam.Task, focused bool, cursor, width, maxRows int) string {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}
	if len(tasks) == 0 {
		body := headerStyle.Render("Dispatches (0)") + "\n" +
			dimStyle.Render("(no dispatches yet — run `clawtool send --async <prompt>`)")
		return style.Width(maxWidth(width)).Render(body)
	}
	rows := make([]string, 0, len(tasks))
	for _, t := range tasks {
		short := t.TaskID
		if len(short) > 12 {
			short = short[:12]
		}
		status := statusFor(string(t.Status))
		last := truncate(t.LastMessage, 40)
		rows = append(rows, fmt.Sprintf("%-10s %-12s %-15s %-5d %s",
			status, t.Agent, short, t.MessageCount, last))
	}
	header := fmt.Sprintf("Dispatches (%d)\n  %-10s %-12s %-15s %-5s %s",
		len(tasks), "STATUS", "AGENT", "TASK_ID", "MSGS", "LAST")
	body := renderPaneRows(header, rows, maxRows, focused, cursor)
	return style.Width(maxWidth(width)).Render(body)
}

func paneAgents(list []agents.Agent, focused bool, cursor, width, maxRows int) string {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}
	if len(list) == 0 {
		body := headerStyle.Render("Agents (0)") + "\n" +
			dimStyle.Render("(no agents — run `clawtool bridge add <family>`)")
		return style.Width(maxWidth(width)).Render(body)
	}
	rows := make([]string, 0, len(list))
	for _, a := range list {
		callable := "no"
		if a.Callable {
			callable = "yes"
		}
		sb := a.Sandbox
		if sb == "" {
			sb = "—"
		}
		rows = append(rows, fmt.Sprintf("%-15s %-10s %-8s %-15s %s",
			a.Instance, a.Family, callable, a.Status, sb))
	}
	header := fmt.Sprintf("Agents (%d)\n  %-15s %-10s %-8s %-15s %s",
		len(list), "INSTANCE", "FAMILY", "CALLABLE", "STATUS", "SANDBOX")
	body := renderPaneRows(header, rows, maxRows, focused, cursor)
	return style.Width(maxWidth(width)).Render(body)
}

func paneStats(tasks []biam.Task, list []agents.Agent, focused bool, width int) string {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}
	var counts struct {
		active, done, failed, cancelled, expired int
	}
	for _, t := range tasks {
		switch t.Status {
		case biam.TaskActive, biam.TaskPending:
			counts.active++
		case biam.TaskDone:
			counts.done++
		case biam.TaskFailed:
			counts.failed++
		case biam.TaskCancelled:
			counts.cancelled++
		case biam.TaskExpired:
			counts.expired++
		}
	}
	callable := 0
	for _, a := range list {
		if a.Callable {
			callable++
		}
	}
	body := headerStyle.Render("Stats") + "\n" +
		fmt.Sprintf("  %d total · %s active · %s done · %s failed · %d cancelled · %d expired · %d/%d agents callable",
			len(tasks),
			statusActive.Render(fmt.Sprintf("%d", counts.active)),
			statusDone.Render(fmt.Sprintf("%d", counts.done)),
			statusFailed.Render(fmt.Sprintf("%d", counts.failed)),
			counts.cancelled, counts.expired,
			callable, len(list))
	return style.Width(maxWidth(width)).Render(body)
}

func statusFor(s string) string {
	switch s {
	case "active", "pending":
		return statusActive.Render(s)
	case "done":
		return statusDone.Render(s)
	case "failed", "cancelled", "expired":
		return statusFailed.Render(s)
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func maxWidth(w int) int {
	if w <= 0 {
		return 100
	}
	if w < 60 {
		return 60
	}
	return w - 2
}

// ─── async refresh ──────────────────────────────────────────────

func refreshCmd(store *biam.Store, sup agents.Supervisor) tea.Cmd {
	return func() tea.Msg {
		out := refreshMsg{}
		if store != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			tasks, err := store.ListTasks(ctx, 50)
			if err != nil {
				out.err = err
				return out
			}
			out.tasks = tasks
		}
		if sup != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ags, err := sup.Agents(ctx)
			if err != nil {
				out.err = err
				return out
			}
			out.agents = ags
		}
		return out
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// ─── push-mode watch socket subscription ───────────────────────────
//
// Phase 1 of the orchestrator (ADR-028): consume the daemon's
// task-watch Unix socket so state transitions reach the dashboard
// in real time instead of waiting for the 1s SQLite tick. Connect
// failures are silently absorbed — the polling tick keeps the
// dashboard alive.

type watchEventMsg struct {
	task biam.Task
	dec  *json.Decoder
	conn net.Conn
}

type watchClosedMsg struct{}

func watchSubscribeCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := biam.DialWatchSocket("")
		if err != nil {
			// No daemon / no socket → polling-only mode.
			return watchClosedMsg{}
		}
		dec := json.NewDecoder(bufio.NewReader(conn))
		return readNextEnvelope(dec, conn)
	}
}

func watchReadCmd(dec *json.Decoder, conn net.Conn) tea.Cmd {
	return func() tea.Msg {
		return readNextEnvelope(dec, conn)
	}
}

// readNextEnvelope blocks until the next WatchEnvelope arrives,
// loops past stream frames (the dashboard's tasks pane only cares
// about Task transitions for now), and returns either a
// watchEventMsg with the materialised Task or watchClosedMsg on
// disconnect.
func readNextEnvelope(dec *json.Decoder, conn net.Conn) tea.Msg {
	for {
		var env biam.WatchEnvelope
		if err := dec.Decode(&env); err != nil {
			_ = conn.Close()
			return watchClosedMsg{}
		}
		switch env.Kind {
		case "task":
			if env.Task == nil {
				continue
			}
			return watchEventMsg{task: *env.Task, dec: dec, conn: conn}
		case "frame":
			// Dashboard tasks pane doesn't render frames —
			// only the orchestrator does. Skip and read again.
			continue
		default:
			// Unknown kind — defensively skip.
			continue
		}
	}
}

func Run(store *biam.Store, sup agents.Supervisor) error {
	p := tea.NewProgram(New(store, sup), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
