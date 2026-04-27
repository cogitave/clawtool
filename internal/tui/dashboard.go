// Package tui — multi-pane dashboard for clawtool's runtime
// surface. Bubble Tea-based, deferred from the v0.19 sketch and
// resurrected for the operator's "I want to see what every agent
// is doing" directive.
//
// What ships now (v1):
//
//	Pane 1 — Dispatches: BIAM tasks (active first, then recent)
//	Pane 2 — Agents:     supervisor.Agents() snapshot
//	Pane 3 — Stats:      counters (total / done / failed / active)
//
// Refresh is via 1s poll over the BIAM SQLite store; real-time
// push hook (biam.Notifier subscription when launched alongside
// `clawtool serve`) lands in v1.1 alongside #185 (Unix socket
// push). Polling has the same negligible-cost SQLite WAL profile
// `clawtool task watch` uses.
//
// Keybindings:
//
//	q / esc / ctrl+c   exit
//	r                   force refresh (don't wait for tick)
//	tab                 cycle focused pane (cosmetic; v2 will
//	                    allow scroll inside the focused pane)
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

// Model is the Bubble Tea state. One per `clawtool dashboard`
// invocation; shut down on quit.
type Model struct {
	store *biam.Store
	sup   agents.Supervisor

	width  int
	height int

	tasks  []biam.Task
	agents []agents.Agent
	loaded bool
	err    error

	focused int // 0=dispatches 1=agents 2=stats
	cursor  int // row cursor inside focused pane
}

// New constructs a Model with the supplied BIAM store and
// supervisor. Call New(...).Run() from the CLI verb. A nil store
// yields a model that renders an empty Pane 1 — useful when the
// operator hasn't dispatched anything yet, so the CLI doesn't
// crash on first launch.
func New(store *biam.Store, sup agents.Supervisor) Model {
	return Model{store: store, sup: sup}
}

// Init kicks off the first refresh.
func (m Model) Init() tea.Cmd {
	return refreshCmd(m.store, m.sup)
}

// Update handles incoming messages. Three event sources:
//   - tea.WindowSizeMsg          — terminal resize
//   - tea.KeyMsg                  — operator keystrokes
//   - refreshMsg                  — async refresh result
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
			max := m.paneRowCount()
			if m.cursor < max-1 {
				m.cursor++
			}
			return m, nil
		}

	case refreshMsg:
		m.tasks = msg.tasks
		m.agents = msg.agents
		m.err = msg.err
		m.loaded = true
		return m, tickCmd()

	case tickMsg:
		return m, refreshCmd(m.store, m.sup)
	}

	return m, nil
}

// View is the render entry point.
func (m Model) View() string {
	if !m.loaded {
		return banner() + "\nloading…"
	}
	if m.err != nil {
		return banner() + "\nerror: " + m.err.Error()
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		banner(),
		paneDispatches(m.tasks, m.focused == 0, m.cursor, m.width),
		paneAgents(m.agents, m.focused == 1, m.cursor, m.width),
		paneStats(m.tasks, m.agents, m.focused == 2, m.width),
		footer(),
	)
	return body
}

// paneRowCount returns the row count in the currently-focused
// pane so up/down keys clamp correctly.
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

func banner() string {
	return titleStyle.Render("clawtool dashboard") +
		dimStyle.Render(" — Tools. Agents. Wired.")
}

func footer() string {
	return dimStyle.Render(
		"q quit · r refresh · tab cycle pane · ↑/↓ navigate")
}

func paneDispatches(tasks []biam.Task, focused bool, cursor, width int) string {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("Dispatches (%d)", len(tasks))))
	b.WriteString("\n")
	if len(tasks) == 0 {
		b.WriteString(dimStyle.Render("(no dispatches yet — run `clawtool send --async <prompt>`)"))
		return style.Width(maxWidth(width)).Render(b.String())
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-10s %-12s %-15s %-10s %s",
		"STATUS", "AGENT", "TASK_ID", "MSGS", "LAST")))
	b.WriteString("\n")
	for i, t := range tasks {
		marker := "  "
		if focused && i == cursor {
			marker = "▸ "
		}
		short := t.TaskID
		if len(short) > 12 {
			short = short[:12]
		}
		status := statusFor(string(t.Status))
		last := truncate(t.LastMessage, 40)
		b.WriteString(fmt.Sprintf("%s%-10s %-12s %-15s %-10d %s\n",
			marker, status, t.Agent, short, t.MessageCount, last))
	}
	return style.Width(maxWidth(width)).Render(b.String())
}

func paneAgents(list []agents.Agent, focused bool, cursor, width int) string {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("Agents (%d)", len(list))))
	b.WriteString("\n")
	if len(list) == 0 {
		b.WriteString(dimStyle.Render("(no agents — run `clawtool bridge add <family>`)"))
		return style.Width(maxWidth(width)).Render(b.String())
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-15s %-10s %-10s %-15s %s",
		"INSTANCE", "FAMILY", "CALLABLE", "STATUS", "SANDBOX")))
	b.WriteString("\n")
	for i, a := range list {
		marker := "  "
		if focused && i == cursor {
			marker = "▸ "
		}
		callable := "no"
		if a.Callable {
			callable = "yes"
		}
		sb := a.Sandbox
		if sb == "" {
			sb = "—"
		}
		b.WriteString(fmt.Sprintf("%s%-15s %-10s %-10s %-15s %s\n",
			marker, a.Instance, a.Family, callable, a.Status, sb))
	}
	return style.Width(maxWidth(width)).Render(b.String())
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
	var b strings.Builder
	b.WriteString(headerStyle.Render("Stats"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %d total dispatches · %s active · %s done · %s failed · %d cancelled · %d expired\n",
		len(tasks),
		statusActive.Render(fmt.Sprintf("%d", counts.active)),
		statusDone.Render(fmt.Sprintf("%d", counts.done)),
		statusFailed.Render(fmt.Sprintf("%d", counts.failed)),
		counts.cancelled, counts.expired)
	fmt.Fprintf(&b, "  %d/%d agents callable\n", callable, len(list))
	return style.Width(maxWidth(width)).Render(b.String())
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
	return w - 2 // leave room for borders
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

// tickCmd schedules the next poll. 1s cadence is the floor that
// feels live without dominating the UI's render loop.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Run is the entry point the CLI verb calls. Wraps Bubble Tea's
// program lifecycle.
func Run(store *biam.Store, sup agents.Supervisor) error {
	p := tea.NewProgram(New(store, sup), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
