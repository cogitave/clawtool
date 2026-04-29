// Package tui — orchestrator's Peers panel. The third sidebar tab
// (after Active/Done) shows live peers from the daemon's a2a
// registry plus per-peer inbox state. Replaces the "open another
// tmux window to spy on what other Claude Code sessions are doing"
// workflow with one always-on view.
//
// Data model:
//   - m.peers — last poll result from GET /v1/peers, refreshed every
//     orchPeersPollInterval.
//   - m.peerInbox — drained-or-peeked messages for the currently-
//     selected peer; rendered in the detail pane when on this tab.
//   - peersFetchedMsg / peerInboxFetchedMsg are the tea.Msg pumps
//     that ferry results back into Update().
//
// Why polling instead of subscribing: the daemon's watch socket
// today only ferries BIAM events; adding a second push channel
// for peer events is a Phase-2 task. Polling at 2s is fine for
// the local-host operator-facing case (the visible cost is a tiny
// HTTP hit; the visible win is "I see Bob just finished his task
// without alt-tabbing").
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/daemon"
)

const orchPeersPollInterval = 2 * time.Second

// peersFetchedMsg carries a fresh /v1/peers list. Errors fold into
// `err` so the orchestrator's error banner can surface a "daemon
// down" hint instead of crashing the tab.
type peersFetchedMsg struct {
	peers []a2a.Peer
	err   error
}

// peerInboxFetchedMsg carries the drained inbox for one peer. We
// drain (not peek) so the operator opening the panel sees fresh
// messages once and doesn't accumulate the same ones on every
// tick. If they want to keep messages queued for the recipient's
// own consumption, they should be using `clawtool peer inbox
// --peek` on the peer's own session, not this UI.
type peerInboxFetchedMsg struct {
	peerID   string
	messages []a2a.Message
	err      error
}

// orchPeersFetchCmd polls the daemon's /v1/peers endpoint. Same
// transport conventions as runA2APeers: read state + token from
// disk, 5s timeout, surface decode errors.
func orchPeersFetchCmd() tea.Cmd {
	return func() tea.Msg {
		state, err := daemon.ReadState()
		if err != nil {
			return peersFetchedMsg{err: fmt.Errorf("read daemon state: %w", err)}
		}
		if state == nil {
			return peersFetchedMsg{err: fmt.Errorf("no daemon running")}
		}
		tok, _ := daemon.ReadToken()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("http://127.0.0.1:%d/v1/peers", state.Port), nil)
		if err != nil {
			return peersFetchedMsg{err: err}
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return peersFetchedMsg{err: err}
		}
		defer resp.Body.Close()
		var body struct {
			Peers []a2a.Peer `json:"peers"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return peersFetchedMsg{err: err}
		}
		return peersFetchedMsg{peers: body.Peers}
	}
}

// orchPeersTickCmd is the periodic re-fetch driver. Bubble Tea's
// tick messages don't carry a payload we use, so wrap one as the
// pump and keep the model's tick loop separate from the BIAM tick.
func orchPeersTickCmd() tea.Cmd {
	return tea.Tick(orchPeersPollInterval, func(time.Time) tea.Msg {
		return peersTickMsg{}
	})
}

type peersTickMsg struct{}

// orchPeerInboxCmd peeks (does NOT consume) the selected peer's
// inbox for the orchestrator's read-only view. The peer itself is
// the rightful drain consumer; the orchestrator just observes.
func orchPeerInboxCmd(peerID string) tea.Cmd {
	return func() tea.Msg {
		state, err := daemon.ReadState()
		if err != nil || state == nil {
			return peerInboxFetchedMsg{peerID: peerID, err: fmt.Errorf("daemon unreachable")}
		}
		tok, _ := daemon.ReadToken()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		url := fmt.Sprintf("http://127.0.0.1:%d/v1/peers/%s/messages?peek=1", state.Port, peerID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return peerInboxFetchedMsg{peerID: peerID, err: err}
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return peerInboxFetchedMsg{peerID: peerID, err: err}
		}
		defer resp.Body.Close()
		var body struct {
			Messages []a2a.Message `json:"messages"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return peerInboxFetchedMsg{peerID: peerID, messages: body.Messages}
	}
}

// renderPeersSidebar mirrors renderSidebar's geometry for the
// peers tab. Selected peer gets the SelectedRow treatment; status
// pills reuse the BIAM theme so the visual idiom stays consistent.
func (m *OrchModel) renderPeersSidebar(maxVisible int) string {
	t := m.theme
	if len(m.peers) == 0 {
		return t.Dim.Render("(no peers registered)") + "\n" +
			t.Dim.Render("hooks/hooks.json bundles claude-code\nautoregister; for codex/gemini/opencode\nrun: clawtool hooks install <runtime>")
	}
	start := 0
	if m.peersCursor >= maxVisible {
		start = m.peersCursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(m.peers) {
		end = len(m.peers)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		p := m.peers[i]
		row := m.renderPeerRow(p, i == m.peersCursor)
		b.WriteString(row)
		b.WriteByte('\n')
	}
	if hidden := len(m.peers) - (end - start); hidden > 0 {
		b.WriteString(t.Dim.Render(fmt.Sprintf("  … %d more (↑↓)", hidden)))
	}
	return b.String()
}

func (m *OrchModel) renderPeerRow(p a2a.Peer, selected bool) string {
	t := m.theme
	pill := t.StatusPill(string(p.Status)).Render(strings.ToUpper(string(p.Status))[:min(4, len(string(p.Status)))])
	name := p.DisplayName
	if len(name) > 11 {
		name = name[:11]
	}
	short := p.PeerID
	if len(short) > 8 {
		short = short[:8]
	}
	line1 := pill + " " + t.Body.Render(name)
	line2 := t.Dim.Render(short + "  " + p.Backend)
	full := line1 + "\n" + line2
	if selected {
		return t.SelectedRow.Render("▸ " + full)
	}
	return "  " + full
}

// renderPeerDetail prints the selected peer's metadata + its
// peeked inbox in the detail pane. Read-only: the orchestrator
// does not impersonate the peer or drain its mailbox.
func (m *OrchModel) renderPeerDetail() string {
	t := m.theme
	if len(m.peers) == 0 || m.peersCursor >= len(m.peers) {
		return t.Dim.Render("Select a peer with ↑↓.")
	}
	p := m.peers[m.peersCursor]
	var b bytes.Buffer
	fmt.Fprintln(&b, t.PaneTitle.Render(p.DisplayName))
	fmt.Fprintf(&b, "%s %s · %s\n",
		t.Dim.Render("backend"), p.Backend, t.StatusPill(string(p.Status)).Render(string(p.Status)))
	fmt.Fprintf(&b, "%s %s\n", t.Dim.Render("peer_id"), p.PeerID)
	if p.SessionID != "" {
		fmt.Fprintf(&b, "%s %s\n", t.Dim.Render("session"), p.SessionID)
	}
	if p.Path != "" {
		fmt.Fprintf(&b, "%s %s\n", t.Dim.Render("path   "), p.Path)
	}
	if p.Circle != "" {
		fmt.Fprintf(&b, "%s %s\n", t.Dim.Render("circle "), p.Circle)
	}
	if p.PID > 0 {
		fmt.Fprintf(&b, "%s %d\n", t.Dim.Render("pid    "), p.PID)
	}
	age := time.Since(p.LastSeen).Round(time.Second)
	fmt.Fprintf(&b, "%s %s ago\n", t.Dim.Render("seen   "), age)
	fmt.Fprintln(&b)
	if m.peerInboxErr != nil {
		fmt.Fprintln(&b, t.Error.Render("inbox: "+m.peerInboxErr.Error()))
	} else if len(m.peerInbox) == 0 {
		fmt.Fprintln(&b, t.Dim.Render("inbox: (empty) — press i to refresh"))
	} else {
		fmt.Fprintln(&b, t.PaneTitle.Render(fmt.Sprintf("inbox · %d msg(s)", len(m.peerInbox))))
		for _, msg := range m.peerInbox {
			from := msg.FromPeer
			if len(from) > 8 {
				from = from[:8]
			}
			fmt.Fprintf(&b, "  %s %s → %s\n",
				t.Dim.Render(msg.Timestamp.Format("15:04:05")),
				from,
				msg.Type)
			fmt.Fprintf(&b, "    %s\n", msg.Text)
		}
	}
	return lipgloss.NewStyle().Render(b.String())
}
