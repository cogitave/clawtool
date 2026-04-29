package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cogitave/clawtool/internal/a2a"
)

func TestOrch_PeersTab_FetchedMsgPopulatesSlice(t *testing.T) {
	m := NewOrchestrator()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.(OrchModel).Update(peersFetchedMsg{
		peers: []a2a.Peer{
			{PeerID: "a1", DisplayName: "alice", Backend: "claude-code", Status: a2a.PeerOnline, LastSeen: time.Now()},
			{PeerID: "b2", DisplayName: "bob", Backend: "codex", Status: a2a.PeerBusy, LastSeen: time.Now()},
		},
	})
	om := updated.(OrchModel)
	if len(om.peers) != 2 {
		t.Fatalf("peers slice not populated: got %d", len(om.peers))
	}
	if om.peers[0].DisplayName != "alice" || om.peers[1].DisplayName != "bob" {
		t.Errorf("peers ordering: %+v", om.peers)
	}
}

func TestOrch_PeersTab_KeyboardSwitchAndCursor(t *testing.T) {
	m := NewOrchestrator()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.(OrchModel).Update(peersFetchedMsg{
		peers: []a2a.Peer{
			{PeerID: "a", DisplayName: "alice", Backend: "claude-code", Status: a2a.PeerOnline},
			{PeerID: "b", DisplayName: "bob", Backend: "codex", Status: a2a.PeerOnline},
		},
	})
	// '3' switches to the Peers tab.
	updated, _ = updated.(OrchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if updated.(OrchModel).tab != orchTabPeers {
		t.Fatal("'3' should select the Peers tab")
	}
	// Down arrow advances the peers cursor (NOT the tasks cursor).
	updated, _ = updated.(OrchModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	om := updated.(OrchModel)
	if om.peersCursor != 1 {
		t.Errorf("peersCursor=%d, want 1", om.peersCursor)
	}
	if om.cursor != 0 {
		t.Errorf("BIAM cursor leaked: got %d, want unchanged 0", om.cursor)
	}
}

func TestOrch_PeersTab_InboxKeyFiresFetchOnlyWhenOnPeersTab(t *testing.T) {
	m := NewOrchestrator()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.(OrchModel).Update(peersFetchedMsg{
		peers: []a2a.Peer{{PeerID: "p1", DisplayName: "p", Backend: "codex", Status: a2a.PeerOnline}},
	})
	// On the Active tab, 'i' is a silent no-op (no command).
	_, cmd := updated.(OrchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Errorf("'i' on Active tab should be a no-op, got cmd")
	}
	// Switch to Peers tab, 'i' now fires the inbox fetch.
	updated, _ = updated.(OrchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	_, cmd = updated.(OrchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Errorf("'i' on Peers tab should fire orchPeerInboxCmd")
	}
}

func TestOrch_PeersTab_InboxFetchedPopulatesView(t *testing.T) {
	m := NewOrchestrator()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.(OrchModel).Update(peerInboxFetchedMsg{
		peerID: "x",
		messages: []a2a.Message{
			{ID: "m1", FromPeer: "alice", Text: "hi", Type: a2a.MsgNotification, Timestamp: time.Now()},
		},
	})
	om := updated.(OrchModel)
	if len(om.peerInbox) != 1 || om.peerInbox[0].Text != "hi" {
		t.Errorf("inbox not populated: %+v", om.peerInbox)
	}
}

func TestOrch_PeersTab_RenderDoesNotPanicEmptyOrPopulated(t *testing.T) {
	m := NewOrchestrator()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated, _ = updated.(OrchModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	om := updated.(OrchModel)
	// Empty Peers tab should produce a non-panicking, non-empty view.
	if v := om.View(); v == "" {
		t.Fatal("empty peers tab View() returned empty string")
	}
	// Populated inbox + selected peer.
	updated, _ = om.Update(peersFetchedMsg{peers: []a2a.Peer{
		{PeerID: "p", DisplayName: "p", Backend: "codex", Status: a2a.PeerOnline, LastSeen: time.Now()},
	}})
	updated, _ = updated.(OrchModel).Update(peerInboxFetchedMsg{
		peerID: "p",
		messages: []a2a.Message{
			{ID: "m", FromPeer: "alice", Text: "hi", Type: a2a.MsgNotification, Timestamp: time.Now()},
		},
	})
	if v := updated.(OrchModel).View(); v == "" {
		t.Fatal("populated peers tab View() returned empty string")
	}
}
