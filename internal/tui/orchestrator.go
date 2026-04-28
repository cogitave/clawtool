// Package tui — orchestrator TUI (Phase 2 of ADR-028). Split-pane
// Bubble Tea view that auto-spawns one pane per active BIAM
// dispatch, fades panes 5 seconds after they hit terminal so the
// operator sees the final tail before the layout reflows. Feeds
// from the daemon's task-watch Unix socket — no SQLite poll.
//
// Layout: lipgloss horizontal+vertical join, square-ish grid sized
// to the terminal viewport. With 4 active dispatches, 2x2; with 6,
// 3x2; with 1, the single pane spans the whole window.
//
// Pane content per task:
//
//	┌─ task abc12345 (codex) ─────┐
//	│ [21:47:01] active  · 12 msg │
//	│                             │
//	│ > applying patch 3/12       │
//	└─────────────────────────────┘
//
// Pane fades to dim style 5s after terminal so the operator
// notices the transition; pane closes 5s after fade so the layout
// reflows around remaining live dispatches.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

const (
	orchTickInterval   = 500 * time.Millisecond
	orchPaneFadeAfter  = 5 * time.Second
	orchPaneCloseAfter = 10 * time.Second
)

// orchPane is one tracked dispatch.
type orchPane struct {
	task     biam.Task
	terminal time.Time // zero until task hits terminal
}

// OrchModel is the orchestrator's Bubble Tea state.
type OrchModel struct {
	width  int
	height int

	panes map[string]*orchPane
	err   error
}

// NewOrchestrator constructs a fresh orchestrator model. The
// watch-socket dial happens in Init so a transient daemon outage
// at construct time doesn't cancel the model.
func NewOrchestrator() OrchModel {
	return OrchModel{panes: map[string]*orchPane{}}
}

func (m OrchModel) Init() tea.Cmd {
	return tea.Batch(
		orchSubscribeCmd(),
		orchTickCmd(),
	)
}

type orchTickMsg time.Time

func (m OrchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			// Reconnect to the watch socket — useful after a
			// daemon restart.
			m.err = nil
			return m, orchSubscribeCmd()
		}

	case watchEventMsg:
		// Replace or insert the pane.
		p, ok := m.panes[msg.task.TaskID]
		if !ok {
			p = &orchPane{}
			m.panes[msg.task.TaskID] = p
		}
		p.task = msg.task
		// Mark terminal time on first transition; preserve
		// the existing timestamp so the fade clock is stable
		// even if the daemon re-emits the row.
		if p.terminal.IsZero() && msg.task.Status.IsTerminal() {
			p.terminal = time.Now()
		}
		return m, watchReadCmd(msg.dec, msg.conn)

	case watchClosedMsg:
		m.err = fmt.Errorf("watch socket disconnected — press r to reconnect")
		return m, nil

	case orchTickMsg:
		// Sweep panes that have aged past the close deadline so
		// the layout reflows around remaining live ones.
		now := time.Now()
		for id, p := range m.panes {
			if !p.terminal.IsZero() && now.Sub(p.terminal) > orchPaneCloseAfter {
				delete(m.panes, id)
			}
		}
		return m, orchTickCmd()
	}
	return m, nil
}

func (m OrchModel) View() string {
	var b strings.Builder
	b.WriteString(orchBanner(len(m.panes), m.err))
	b.WriteByte('\n')
	if len(m.panes) == 0 {
		body := dimStyle.Render("No active dispatches. Run `clawtool send --async <agent> \"...\"` and the pane appears here.")
		b.WriteString(body)
		b.WriteByte('\n')
		b.WriteString(orchFooter())
		return b.String()
	}

	// Sort by task_id so layout is stable across renders.
	ids := make([]string, 0, len(m.panes))
	for id := range m.panes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	cols, rows := orchGridShape(len(ids))
	paneWidth := orchPaneWidth(m.width, cols)
	paneHeight := orchPaneHeight(m.height, rows)

	rendered := make([]string, len(ids))
	now := time.Now()
	for i, id := range ids {
		rendered[i] = renderOrchPane(m.panes[id], paneWidth, paneHeight, now)
	}
	// Build the grid row-major.
	var rowsRendered []string
	for r := 0; r < rows; r++ {
		start := r * cols
		end := start + cols
		if end > len(rendered) {
			end = len(rendered)
		}
		rowsRendered = append(rowsRendered, lipgloss.JoinHorizontal(lipgloss.Top, rendered[start:end]...))
	}
	b.WriteString(lipgloss.JoinVertical(lipgloss.Left, rowsRendered...))
	b.WriteByte('\n')
	b.WriteString(orchFooter())
	return b.String()
}

// orchGridShape returns (cols, rows) for n panes — square-ish so
// 1→1x1, 2→2x1, 3→3x1, 4→2x2, 5–6→3x2, 7–9→3x3, 10–12→4x3.
func orchGridShape(n int) (int, int) {
	switch {
	case n <= 1:
		return 1, 1
	case n <= 3:
		return n, 1
	case n == 4:
		return 2, 2
	case n <= 6:
		return 3, 2
	case n <= 9:
		return 3, 3
	}
	return 4, (n + 3) / 4
}

func orchPaneWidth(termW, cols int) int {
	if termW <= 0 {
		termW = 100
	}
	w := termW / cols
	if w < 24 {
		w = 24
	}
	return w - 2
}

func orchPaneHeight(termH, rows int) int {
	if termH <= 0 {
		termH = 30
	}
	chrome := 4
	avail := (termH - chrome) / rows
	if avail < 4 {
		avail = 4
	}
	return avail
}

func renderOrchPane(p *orchPane, width, height int, now time.Time) string {
	style := paneStyle
	if !p.terminal.IsZero() && now.Sub(p.terminal) > orchPaneFadeAfter {
		style = paneStyle.Foreground(lipgloss.Color("245"))
	}
	short := p.task.TaskID
	if len(short) > 8 {
		short = short[:8]
	}
	header := fmt.Sprintf("task %s (%s)", short, p.task.Agent)
	body := fmt.Sprintf("[%s] %s · %d msg",
		now.Local().Format("15:04:05"),
		statusFor(string(p.task.Status)),
		p.task.MessageCount)
	tail := truncate(p.task.LastMessage, width-4)
	if tail == "" {
		tail = dimStyle.Render("(awaiting first event)")
	} else {
		tail = "> " + tail
	}
	content := titleStyle.Render(header) + "\n" +
		body + "\n\n" +
		tail
	// Pad content to height so all panes in a row align.
	lines := strings.Split(content, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return style.Width(width).Render(strings.Join(lines, "\n"))
}

func orchBanner(active int, err error) string {
	tail := fmt.Sprintf("%d active dispatch(es)", active)
	if err != nil {
		tail += "  ·  " + statusFailed.Render(err.Error())
	}
	return titleStyle.Render("clawtool orchestrator") + "  " + dimStyle.Render(tail)
}

func orchFooter() string {
	return dimStyle.Render("(q quit · r reconnect socket)")
}

// ── async commands ─────────────────────────────────────────────

func orchSubscribeCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := biam.DialWatchSocket("")
		if err != nil {
			return watchClosedMsg{}
		}
		dec := json.NewDecoder(bufio.NewReader(conn))
		var t biam.Task
		if err := dec.Decode(&t); err != nil {
			_ = conn.Close()
			return watchClosedMsg{}
		}
		return watchEventMsg{task: t, dec: dec, conn: conn}
	}
}

func orchTickCmd() tea.Cmd {
	return tea.Tick(orchTickInterval, func(t time.Time) tea.Msg {
		return orchTickMsg(t)
	})
}

// Unused import guards (keep context + net for forward-compat with
// the planned bytes-stream side channel).
var (
	_ = context.Background
	_ net.Conn
)

// RunOrchestrator boots the Bubble Tea program. Invoked from the
// CLI dispatcher.
func RunOrchestrator() error {
	p := tea.NewProgram(NewOrchestrator(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
