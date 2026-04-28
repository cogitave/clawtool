// Package tui — orchestrator TUI (Phase 3 of ADR-028). The
// production "teammate panel" for clawtool: live byte stream from
// every active dispatch, scrollable per-task viewport, theme-aware
// adaptive colours, key hints rendered via bubbles/help. Inspired
// by lazygit / gh-dash / k9s layout conventions: sidebar + detail
// pane + status bar.
//
// Architecture:
//
//   - Left sidebar (sticky 28 col):  tasks list with status pills
//     and message counts. Arrow keys select, enter focuses, the
//     stream pane on the right reflects the selected task.
//   - Right detail pane (flex):  bubbles/viewport rendering the
//     selected task's StreamFrame ringbuffer line by line. Auto-
//     scroll-to-bottom when new frames arrive UNLESS the operator
//     scrolled up (tail-follow toggle).
//   - Header bar:  app banner + version + live indicator.
//   - Footer bar:  key bindings (q quit · ↑↓ select · pgup/pgdn
//     scroll · f tail-follow · r reconnect) + at-a-glance counts.
//
// The orchestrator subscribes to the daemon's WatchEnvelope socket;
// task transitions update sidebar rows, frames append to the per-
// task ringbuffer. A 5-second post-terminal grace window keeps the
// task visible after it finishes so the operator catches the final
// lines.
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

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/tui/theme"
)

const (
	orchTickInterval    = 500 * time.Millisecond
	orchPaneCloseAfter  = 30 * time.Minute // keep terminal panes browsable in the Done tab
	orchFrameRingMax    = 500              // ringbuffer cap per task
	orchOrderCap        = 200              // hard cap on tracked tasks — protects against snapshot floods on reconnect
	orchSystemBannerTTL = 30 * time.Second // how long a SystemNotification stays visible after arrival
	sidebarWidth        = 28
)

// orchTab enumerates the two sidebar sections. Active tasks (pending
// + active) render in the Active tab; terminal tasks (done / failed /
// cancelled / expired) accumulate in the Done tab. Tab is keyboard-
// switched (`tab` / `1` / `2`).
type orchTab int

const (
	orchTabActive orchTab = iota
	orchTabDone
)

// orchTask is the per-task state the orchestrator maintains.
type orchTask struct {
	task     biam.Task
	frames   []string  // ring of recent stream lines
	terminal time.Time // zero until task hits terminal
	startAt  time.Time // first time we saw this task
}

// OrchModel is the orchestrator's Bubble Tea state.
type OrchModel struct {
	width  int
	height int

	tasks   map[string]*orchTask
	order   []string // task ID order — newest first
	cursor  int      // index into the active visible list for the selected task
	tab     orchTab  // which sidebar tab is in focus
	stream  viewport.Model
	follow  bool // auto-scroll to bottom on new frames
	err     error
	connAt  time.Time
	frameCt int

	// systemBanner is the most-recent SystemNotification the
	// daemon broadcast (e.g. "clawtool update available") plus
	// the timestamp it arrived. We render it inline above the
	// sidebar/detail panes for orchSystemBannerTTL, then it
	// auto-fades — operator either clicked the action or moved on.
	systemBanner   *biam.SystemNotification
	systemBannerAt time.Time

	theme *theme.Theme
}

// NewOrchestrator constructs a fresh orchestrator model.
func NewOrchestrator() OrchModel {
	t := theme.Default()
	vp := viewport.New(40, 10)
	vp.Style = t.Body
	return OrchModel{
		tasks:  map[string]*orchTask{},
		stream: vp,
		follow: true,
		theme:  t,
	}
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
		m.resizeStream()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.err = nil
			m.connAt = time.Time{}
			return m, orchSubscribeCmd()
		case "f":
			m.follow = !m.follow
			return m, nil
		case "tab":
			m.tab = (m.tab + 1) % 2
			m.cursor = 0
			m.refreshStreamForSelection()
			return m, nil
		case "1":
			m.tab = orchTabActive
			m.cursor = 0
			m.refreshStreamForSelection()
			return m, nil
		case "2":
			m.tab = orchTabDone
			m.cursor = 0
			m.refreshStreamForSelection()
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.refreshStreamForSelection()
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.visibleIDs())-1 {
				m.cursor++
				m.refreshStreamForSelection()
			}
			return m, nil
		case "pgup", "ctrl+u":
			m.stream.HalfPageUp()
			m.follow = false
			return m, nil
		case "pgdown", "ctrl+d":
			m.stream.HalfPageDown()
			return m, nil
		case "home", "g":
			m.stream.GotoTop()
			m.follow = false
			return m, nil
		case "end", "G":
			m.stream.GotoBottom()
			m.follow = true
			return m, nil
		}

	case watchEventMsg:
		// Task snapshot — upsert. Both Active and Done tabs
		// accept inserts; the snapshot pump replays history,
		// terminal rows simply land in the Done tab instead of
		// flooding Active. Per-tab visibility filtering happens
		// at render time via visibleIDs().
		t, ok := m.tasks[msg.task.TaskID]
		if !ok {
			t = &orchTask{
				task:    msg.task,
				startAt: time.Now(),
			}
			m.tasks[msg.task.TaskID] = t
			m.order = append([]string{msg.task.TaskID}, m.order...)
			// Cap order/tasks to protect against snapshot floods
			// on reconnect — without this a daemon with 1000 rows
			// in biam.db would replay all of them on every `r`,
			// blowing the orchestrator's memory + render budget.
			// Drop oldest tail entries past the cap.
			if len(m.order) > orchOrderCap {
				dropped := m.order[orchOrderCap:]
				for _, id := range dropped {
					delete(m.tasks, id)
				}
				m.order = m.order[:orchOrderCap]
			}
		} else {
			t.task = msg.task
		}
		// Stamp terminal time on the first transition / first
		// sight as terminal — needed so the orchTickMsg sweep
		// has a "this row went terminal at T" reference even
		// for snapshots that arrived already-done.
		if t.terminal.IsZero() && msg.task.Status.IsTerminal() {
			t.terminal = time.Now()
		}
		// Initialise cursor when the visible list goes from 0
		// to 1, regardless of which tab is in focus — first
		// row is always selected by default.
		if len(m.visibleIDs()) == 1 {
			m.cursor = 0
		}
		m.refreshStreamForSelection()
		return m, watchReadCmd(msg.dec, msg.conn)

	case watchFrameMsg:
		t, ok := m.tasks[msg.frame.TaskID]
		if !ok {
			// Frame for an unseen task — synthesise a stub
			// so the line isn't lost; the next snapshot
			// hydrates the rest.
			t = &orchTask{
				task:    biam.Task{TaskID: msg.frame.TaskID, Agent: msg.frame.Agent, Status: biam.TaskActive},
				startAt: time.Now(),
			}
			m.tasks[msg.frame.TaskID] = t
			m.order = append([]string{msg.frame.TaskID}, m.order...)
			if len(m.order) == 1 {
				m.cursor = 0
			}
		}
		t.frames = append(t.frames, msg.frame.Line)
		if len(t.frames) > orchFrameRingMax {
			t.frames = t.frames[len(t.frames)-orchFrameRingMax:]
		}
		m.frameCt++
		// Only re-render the stream when the affected task is the
		// selected one — avoids unnecessary paints.
		if m.selectedTaskID() == msg.frame.TaskID {
			m.renderStream(t)
			if m.follow {
				m.stream.GotoBottom()
			}
		}
		return m, watchReadCmd(msg.dec, msg.conn)

	case watchSystemMsg:
		// Latch the banner; the ticker will sweep it after
		// orchSystemBannerTTL. Replacing on every event means
		// a fresher notification (e.g. update_available with a
		// new tag) overwrites the older one — the operator
		// always sees the most-recent system event.
		n := msg.notification
		m.systemBanner = &n
		m.systemBannerAt = time.Now()
		return m, watchReadCmd(msg.dec, msg.conn)

	case watchClosedMsg:
		m.err = fmt.Errorf("watch socket disconnected — press r to reconnect")
		return m, nil

	case orchTickMsg:
		// Sweep terminal panes past grace window so the Done
		// tab doesn't grow unboundedly. Active tab is unaffected
		// (only terminal rows have a non-zero terminal stamp).
		// Re-pick cursor when the selected task disappears.
		now := time.Now()
		// Fade the system banner past TTL.
		if m.systemBanner != nil && now.Sub(m.systemBannerAt) > orchSystemBannerTTL {
			m.systemBanner = nil
			m.systemBannerAt = time.Time{}
		}
		removed := false
		newOrder := make([]string, 0, len(m.order))
		selID := m.selectedTaskID()
		for _, id := range m.order {
			t := m.tasks[id]
			if t == nil {
				continue
			}
			if !t.terminal.IsZero() && now.Sub(t.terminal) > orchPaneCloseAfter {
				delete(m.tasks, id)
				removed = true
				continue
			}
			newOrder = append(newOrder, id)
		}
		m.order = newOrder
		if removed {
			vis := m.visibleIDs()
			m.cursor = 0
			for i, id := range vis {
				if id == selID {
					m.cursor = i
					break
				}
			}
			if m.cursor >= len(vis) {
				if len(vis) == 0 {
					m.cursor = 0
				} else {
					m.cursor = len(vis) - 1
				}
			}
			m.refreshStreamForSelection()
		}
		return m, orchTickCmd()
	}
	// Forward to viewport for any unhandled msg (mouse events etc.)
	var cmd tea.Cmd
	m.stream, cmd = m.stream.Update(msg)
	return m, cmd
}

// selectedTaskID returns the task currently in focus within the
// active tab, or "" when the visible list is empty.
func (m *OrchModel) selectedTaskID() string {
	vis := m.visibleIDs()
	if m.cursor < 0 || m.cursor >= len(vis) {
		return ""
	}
	return vis[m.cursor]
}

// visibleIDs returns the task IDs that belong on the current tab,
// sorted newest-first. Active tab = pending + active rows; Done
// tab = every terminal row. Sort key is startAt for the Active tab
// (most-recently-dispatched on top) and the terminal stamp for the
// Done tab (most-recently-finished on top) so the eye lands on
// the freshest row in either case.
func (m *OrchModel) visibleIDs() []string {
	if len(m.order) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.order))
	for _, id := range m.order {
		t := m.tasks[id]
		if t == nil {
			continue
		}
		isTerminal := t.task.Status.IsTerminal()
		switch m.tab {
		case orchTabActive:
			if !isTerminal {
				out = append(out, id)
			}
		case orchTabDone:
			if isTerminal {
				out = append(out, id)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti := m.tasks[out[i]]
		tj := m.tasks[out[j]]
		switch m.tab {
		case orchTabDone:
			return ti.terminal.After(tj.terminal)
		default:
			return ti.startAt.After(tj.startAt)
		}
	})
	return out
}

// activeCount / doneCount are tiny helpers for header / tab labels.
func (m *OrchModel) activeCount() int {
	n := 0
	for _, t := range m.tasks {
		if !t.task.Status.IsTerminal() {
			n++
		}
	}
	return n
}

func (m *OrchModel) doneCount() int {
	n := 0
	for _, t := range m.tasks {
		if t.task.Status.IsTerminal() {
			n++
		}
	}
	return n
}

// resizeStream recalculates the viewport dimensions from the
// terminal size + sidebar width. Invoked on every WindowSizeMsg.
func (m *OrchModel) resizeStream() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	// chrome: header (3) + footer (1) + pane border (2) + spacing
	streamW := m.width - sidebarWidth - 4
	if streamW < 30 {
		streamW = 30
	}
	// Detail pane has Height(m.height-7); content = title line (1)
	// + viewport. Without subtracting the title, viewport.View()
	// rendered m.height-7 lines + 1 title = m.height-6 total — one
	// line past the pane border, so the bottom row never lined up
	// with the sidebar's bottom. -8 keeps both panes flush.
	streamH := m.height - 8
	if streamH < 6 {
		streamH = 6
	}
	m.stream.Width = streamW
	m.stream.Height = streamH
}

// refreshStreamForSelection re-paints the viewport from the current
// selection's ringbuffer.
func (m *OrchModel) refreshStreamForSelection() {
	id := m.selectedTaskID()
	if id == "" {
		m.stream.SetContent("")
		return
	}
	t := m.tasks[id]
	if t == nil {
		m.stream.SetContent("")
		return
	}
	m.renderStream(t)
	if m.follow {
		m.stream.GotoBottom()
	}
}

func (m *OrchModel) renderStream(t *orchTask) {
	if len(t.frames) == 0 {
		hint := m.theme.Dim.Render("(awaiting first event from " + safeAgent(t.task.Agent) + ")")
		m.stream.SetContent(hint)
		return
	}
	var b strings.Builder
	caret := m.theme.StreamCaret.Render("▏")
	width := m.stream.Width
	if width < 30 {
		width = 30
	}
	for _, line := range t.frames {
		// Wrap long lines to the viewport width minus the caret.
		wrapped := wrapText(line, width-2)
		for _, sub := range wrapped {
			b.WriteString(caret)
			b.WriteByte(' ')
			b.WriteString(m.theme.StreamLine.Render(sub))
			b.WriteByte('\n')
		}
	}
	m.stream.SetContent(strings.TrimRight(b.String(), "\n"))
}

func (m OrchModel) View() string {
	t := m.theme
	if m.width == 0 || m.height == 0 {
		return t.Body.Render("clawtool orchestrator — booting…")
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	sidebar := m.renderSidebar()
	detail := m.renderDetail()

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, detail)

	// System banner sits between header and body when active, so
	// it doesn't disturb the panes' geometry — they each compute
	// their height from m.height-7, and the banner adds at most
	// one row whose height is included in the global total via
	// JoinVertical's natural sum.
	if banner := m.renderSystemBanner(); banner != "" {
		return lipgloss.JoinVertical(lipgloss.Left, header, banner, body, footer)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderSystemBanner returns the inline banner row for the most
// recent SystemNotification, or empty when no banner is active.
// Width matches the terminal so the pill fills the line.
func (m *OrchModel) renderSystemBanner() string {
	if m.systemBanner == nil {
		return ""
	}
	t := m.theme
	style := t.HeaderBar
	switch m.systemBanner.Severity {
	case "warning":
		style = t.HeaderBar.Foreground(t.Warning.GetForeground())
	case "error":
		style = t.HeaderBar.Foreground(t.Error.GetForeground())
	}
	icon := "📦"
	switch m.systemBanner.Kind {
	case "warning":
		icon = "⚠"
	case "error":
		icon = "✘"
	}
	row := icon + " " + m.systemBanner.Title
	if m.systemBanner.ActionHint != "" {
		row += "  " + t.Dim.Render("→ "+m.systemBanner.ActionHint)
	}
	if m.width > 0 {
		return style.Width(m.width).Render(row)
	}
	return style.Render(row)
}

func (m *OrchModel) renderHeader() string {
	t := m.theme
	title := t.HeaderTitle.Render("◆ clawtool")
	subtitle := t.HeaderVersion.Render("orchestrator")
	dot := t.Success.Render("●")
	if m.err != nil {
		dot = t.Error.Render("●")
	}
	live := dot + " " + t.Dim.Render(fmt.Sprintf("%d frames · %d active · %d done", m.frameCt, m.activeCount(), m.doneCount()))
	leftBlock := title + "  " + subtitle
	right := live
	gap := m.width - lipgloss.Width(leftBlock) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	row := leftBlock + strings.Repeat(" ", gap) + right
	return t.HeaderBar.Render(row)
}

func (m *OrchModel) renderFooter() string {
	t := m.theme
	keys := []struct{ k, d string }{
		{"tab/1/2", "switch tab"},
		{"↑↓", "select"},
		{"pgup/pgdn", "scroll"},
		{"f", "follow"},
		{"r", "reconnect"},
		{"q", "quit"},
	}
	parts := make([]string, 0, len(keys))
	for _, kd := range keys {
		parts = append(parts, t.HelpKey.Render(kd.k)+" "+t.HelpDesc.Render(kd.d))
	}
	left := strings.Join(parts, t.HelpSep.Render(" · "))
	right := ""
	if m.err != nil {
		right = t.Error.Render(m.err.Error())
	} else if m.follow {
		right = t.Success.Render("● tail-follow on")
	} else {
		right = t.Warning.Render("○ tail-follow off")
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	row := left + strings.Repeat(" ", gap) + right
	return t.StatusBar.Render(row)
}

func (m *OrchModel) renderSidebar() string {
	t := m.theme

	// Inner height budget: total height minus header(3) +
	// footer(1) + pane border(2) chrome. Same arithmetic the
	// detail pane uses, so both panes line up.
	height := m.height - 7
	if height < 6 {
		height = 6
	}
	// Tab strip eats one row + a separator; row glyphs are 2
	// lines tall (pill+meta). The visible row budget is half
	// the remaining inner height so we never spill past the
	// pane border. Minimum 1 row so a tiny terminal still
	// shows something.
	tabRows := 2
	innerH := height - tabRows
	if innerH < 4 {
		innerH = 4
	}
	rowsPerTask := 2
	maxVisible := innerH / rowsPerTask
	if maxVisible < 1 {
		maxVisible = 1
	}

	// Tab strip: highlight the focused tab, dim the other one.
	activeLabel := fmt.Sprintf("Active (%d)", m.activeCount())
	doneLabel := fmt.Sprintf("Done (%d)", m.doneCount())
	var leftTab, rightTab string
	if m.tab == orchTabActive {
		leftTab = t.PaneTitle.Render(activeLabel)
		rightTab = t.Dim.Render(doneLabel)
	} else {
		leftTab = t.Dim.Render(activeLabel)
		rightTab = t.PaneTitle.Render(doneLabel)
	}
	tabStrip := leftTab + "  " + rightTab

	var b strings.Builder
	b.WriteString(tabStrip)
	b.WriteByte('\n')

	ids := m.visibleIDs()
	if len(ids) == 0 {
		switch m.tab {
		case orchTabActive:
			b.WriteString(t.Dim.Render("(no active dispatches)"))
			b.WriteByte('\n')
			b.WriteString(t.Dim.Render("run: clawtool send --async"))
		case orchTabDone:
			b.WriteString(t.Dim.Render("(no completed dispatches yet)"))
		}
	} else {
		// Window the visible list around the cursor so the
		// selected row is always on screen and the list never
		// spills past the pane border. Slide the window when
		// cursor moves out of the current frame.
		start := 0
		if m.cursor >= maxVisible {
			start = m.cursor - maxVisible + 1
		}
		if start+maxVisible > len(ids) {
			start = len(ids) - maxVisible
			if start < 0 {
				start = 0
			}
		}
		end := start + maxVisible
		if end > len(ids) {
			end = len(ids)
		}
		// Reserve a tail row for the overflow hint when there
		// are rows past the window — operator can scroll into
		// them via ↑↓.
		hasOverflow := len(ids) > maxVisible
		if hasOverflow && end-start == maxVisible {
			end-- // give up the last visible row for the hint
			if end <= start {
				end = start + 1
			}
		}
		for i := start; i < end; i++ {
			task := m.tasks[ids[i]]
			row := m.renderSidebarRow(task, i == m.cursor)
			b.WriteString(row)
			b.WriteByte('\n')
		}
		if hasOverflow {
			hidden := len(ids) - (end - start)
			b.WriteString(t.Dim.Render(fmt.Sprintf("  … %d more (↑↓)", hidden)))
		}
	}
	style := t.PaneBorder.Width(sidebarWidth).Height(height)
	return style.Render(b.String())
}

func (m *OrchModel) renderSidebarRow(o *orchTask, selected bool) string {
	t := m.theme
	short := o.task.TaskID
	if len(short) > 8 {
		short = short[:8]
	}
	pill := t.StatusPill(string(o.task.Status)).Render(strings.ToUpper(string(o.task.Status))[:min(4, len(string(o.task.Status)))])
	agent := o.task.Agent
	if agent == "" {
		agent = "—"
	}
	if len(agent) > 10 {
		agent = agent[:10]
	}
	line1 := pill + " " + t.Body.Render(agent)
	line2 := t.Dim.Render(short + "  " + fmt.Sprintf("%dmsg", o.task.MessageCount))
	full := line1 + "\n" + line2
	if selected {
		return t.SelectedRow.Render("▸ " + full)
	}
	return "  " + full
}

func (m *OrchModel) renderDetail() string {
	t := m.theme
	height := m.height - 7
	if height < 6 {
		height = 6
	}
	width := m.width - sidebarWidth - 4
	if width < 30 {
		width = 30
	}
	var titleLine string
	id := m.selectedTaskID()
	if id == "" {
		titleLine = t.PaneTitle.Render("Live stream") + "  " + t.Dim.Render("(select a dispatch on the left)")
	} else {
		o := m.tasks[id]
		short := id
		if len(short) > 8 {
			short = short[:8]
		}
		age := time.Since(o.startAt).Round(time.Second)
		titleLine = t.PaneTitle.Render("● task "+short) +
			"  " + t.PaneSubtitle.Render(safeAgent(o.task.Agent)+" · "+string(o.task.Status)+" · "+age.String()+" · "+fmt.Sprintf("%d msg", o.task.MessageCount))
	}
	body := titleLine + "\n" + m.stream.View()
	style := t.PaneBorder.Width(width).Height(height)
	return style.Render(body)
}

// ── async commands ─────────────────────────────────────────────

func orchSubscribeCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := biam.DialWatchSocket("")
		if err != nil {
			return watchClosedMsg{}
		}
		dec := json.NewDecoder(bufio.NewReader(conn))
		return readNextOrchEnvelope(dec, conn)
	}
}

// readNextOrchEnvelope returns either a watchEventMsg (Task) or a
// watchFrameMsg (StreamFrame) — whichever comes next on the socket.
func readNextOrchEnvelope(dec *json.Decoder, conn net.Conn) tea.Msg {
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
			if env.Frame == nil {
				continue
			}
			return watchFrameMsg{frame: *env.Frame, dec: dec, conn: conn}
		case "system":
			if env.System == nil {
				continue
			}
			return watchSystemMsg{notification: *env.System, dec: dec, conn: conn}
		}
	}
}

// watchFrameMsg carries a stream line + the decoder to keep reading.
type watchFrameMsg struct {
	frame biam.StreamFrame
	dec   *json.Decoder
	conn  net.Conn
}

// watchSystemMsg carries a daemon-level notification (e.g. update
// available) the WatchHub broadcasts independent of any task.
type watchSystemMsg struct {
	notification biam.SystemNotification
	dec          *json.Decoder
	conn         net.Conn
}

func orchTickCmd() tea.Cmd {
	return tea.Tick(orchTickInterval, func(t time.Time) tea.Msg {
		return orchTickMsg(t)
	})
}

// ── helpers ────────────────────────────────────────────────────

func safeAgent(a string) string {
	if a == "" {
		return "—"
	}
	return a
}

// wrapText breaks a long line at the given width without splitting
// inside word boundaries when avoidable. Falls back to hard-wrap on
// pathologically long tokens (URLs, hashes).
func wrapText(s string, width int) []string {
	if width <= 0 || len(s) <= width {
		return []string{s}
	}
	var out []string
	for len(s) > width {
		// Try to break at the last space before width.
		cut := strings.LastIndex(s[:width], " ")
		if cut < width/2 {
			cut = width
		}
		out = append(out, s[:cut])
		s = strings.TrimLeft(s[cut:], " ")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// _ keeps context import alive even if future refactors temporarily
// drop the use site.
var _ = context.Background

// RunOrchestrator boots the Bubble Tea program. Invoked from the
// CLI dispatcher.
func RunOrchestrator() error {
	p := tea.NewProgram(NewOrchestrator(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
