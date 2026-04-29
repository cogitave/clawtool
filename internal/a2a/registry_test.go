package a2a

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempRegistry returns a Registry whose state path lives
// under t.TempDir, so each test sees a clean slate without
// touching the operator's real ~/.config.
func withTempRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	return NewRegistry(filepath.Join(dir, "peers.json"))
}

func TestRegister_AssignsPeerIDAndPersists(t *testing.T) {
	r := withTempRegistry(t)
	p, err := r.Register(RegisterInput{
		DisplayName: "claude-laptop",
		Path:        t.TempDir(),
		Backend:     "claude-code",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if p.PeerID == "" {
		t.Error("expected non-empty peer_id")
	}
	if p.Status != PeerOnline {
		t.Errorf("Status = %q, want online", p.Status)
	}
	if p.Circle != "default" {
		t.Errorf("Circle = %q, want default fallback", p.Circle)
	}
	if p.Role != RoleAgent {
		t.Errorf("Role = %q, want agent fallback", p.Role)
	}

	// Save → fresh registry → Load roundtrip.
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	r2 := NewRegistry(r.statePath)
	if got := r2.Get(p.PeerID); got == nil {
		t.Errorf("peer lost across Save/Load roundtrip")
	}
}

func TestRegister_RejectsMissingFields(t *testing.T) {
	r := withTempRegistry(t)
	if _, err := r.Register(RegisterInput{Backend: "claude-code"}); err == nil {
		t.Error("missing display_name should error")
	}
	if _, err := r.Register(RegisterInput{DisplayName: "x"}); err == nil {
		t.Error("missing backend should error")
	}
}

func TestRegister_IdempotentOnIdentityTuple(t *testing.T) {
	r := withTempRegistry(t)
	dir := t.TempDir()
	a, _ := r.Register(RegisterInput{
		DisplayName: "claude-laptop",
		Path:        dir,
		Backend:     "claude-code",
		TmuxPane:    "%0",
	})
	b, _ := r.Register(RegisterInput{
		DisplayName: "claude-laptop-renamed", // ignored — existing row wins
		Path:        dir,
		Backend:     "claude-code",
		TmuxPane:    "%0",
	})
	if a.PeerID != b.PeerID {
		t.Errorf("re-register should collapse to same peer_id, got %q vs %q", a.PeerID, b.PeerID)
	}
	if got := r.List(ListFilter{}); len(got) != 1 {
		t.Errorf("expected 1 peer after idempotent re-register, got %d", len(got))
	}
}

func TestHeartbeat_RefreshesLastSeen(t *testing.T) {
	r := withTempRegistry(t)
	p, _ := r.Register(RegisterInput{DisplayName: "x", Backend: "claude-code"})
	original := p.LastSeen

	time.Sleep(10 * time.Millisecond)
	updated, err := r.Heartbeat(p.PeerID, PeerBusy)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if updated == nil {
		t.Fatal("Heartbeat returned nil for known peer")
	}
	if !updated.LastSeen.After(original) {
		t.Errorf("last_seen not advanced: original=%v new=%v", original, updated.LastSeen)
	}
	if updated.Status != PeerBusy {
		t.Errorf("Status = %q, want busy", updated.Status)
	}
}

func TestHeartbeat_UnknownPeerNilNil(t *testing.T) {
	r := withTempRegistry(t)
	got, err := r.Heartbeat("does-not-exist", PeerOnline)
	if err != nil || got != nil {
		t.Errorf("unknown peer should yield (nil, nil); got (%v, %v)", got, err)
	}
}

func TestDeregister_RemovesFromTable(t *testing.T) {
	r := withTempRegistry(t)
	p, _ := r.Register(RegisterInput{DisplayName: "x", Backend: "claude-code"})
	if got, _ := r.Deregister(p.PeerID); got == nil {
		t.Error("Deregister should return removed peer")
	}
	if r.Get(p.PeerID) != nil {
		t.Error("peer still present after deregister")
	}
}

func TestList_LazySweepFlipsStaleToOffline(t *testing.T) {
	r := withTempRegistry(t)
	p, _ := r.Register(RegisterInput{DisplayName: "stale", Backend: "claude-code"})
	// Reach into the registry to backdate last_seen so we don't
	// have to wait HeartbeatStaleAfter in the test. Pure
	// internal-package test so this is fine.
	r.mu.Lock()
	r.peers[p.PeerID].LastSeen = time.Now().Add(-2 * HeartbeatStaleAfter)
	r.mu.Unlock()

	list := r.List(ListFilter{})
	if len(list) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(list))
	}
	if list[0].Status != PeerOffline {
		t.Errorf("stale peer Status = %q, want offline", list[0].Status)
	}
}

func TestList_DropsPeersWithMissingPath(t *testing.T) {
	r := withTempRegistry(t)
	dir := t.TempDir()
	r.Register(RegisterInput{DisplayName: "live", Path: dir, Backend: "claude-code"})

	gone := filepath.Join(dir, "deleted")
	os.Mkdir(gone, 0o700)
	r.Register(RegisterInput{DisplayName: "doomed", Path: gone, Backend: "claude-code"})
	os.Remove(gone)

	got := r.List(ListFilter{})
	if len(got) != 1 {
		t.Fatalf("expected 1 peer (doomed dropped), got %d: %+v", len(got), got)
	}
	if got[0].DisplayName != "live" {
		t.Errorf("kept the wrong peer: %q", got[0].DisplayName)
	}
}

func TestList_FilterByBackendAndStatus(t *testing.T) {
	r := withTempRegistry(t)
	r.Register(RegisterInput{DisplayName: "c", Backend: "claude-code"})
	r.Register(RegisterInput{DisplayName: "x", Backend: "codex"})
	r.Register(RegisterInput{DisplayName: "g", Backend: "gemini"})

	if got := r.List(ListFilter{Backend: "codex"}); len(got) != 1 || got[0].DisplayName != "x" {
		t.Errorf("Backend filter: got %v", got)
	}
	if got := r.List(ListFilter{Status: PeerOnline}); len(got) != 3 {
		t.Errorf("Status=online filter: expected 3, got %d", len(got))
	}
	if got := r.List(ListFilter{Status: PeerOffline}); len(got) != 0 {
		t.Errorf("Status=offline filter: expected 0, got %d", len(got))
	}
}

func TestList_OnlineSortedBeforeOffline(t *testing.T) {
	r := withTempRegistry(t)
	// Distinct identity tuples so the idempotency-collapse path
	// in Register() doesn't merge them onto one row.
	r.Register(RegisterInput{DisplayName: "z-online", Backend: "claude-code", TmuxPane: "%0"})
	stale, _ := r.Register(RegisterInput{DisplayName: "a-stale", Backend: "claude-code", TmuxPane: "%1"})
	r.mu.Lock()
	r.peers[stale.PeerID].LastSeen = time.Now().Add(-2 * HeartbeatStaleAfter)
	r.mu.Unlock()

	got := r.List(ListFilter{})
	if len(got) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(got))
	}
	if got[0].Status != PeerOnline {
		t.Errorf("online peer should sort first, got order: %s, %s", got[0].DisplayName, got[1].DisplayName)
	}
}
