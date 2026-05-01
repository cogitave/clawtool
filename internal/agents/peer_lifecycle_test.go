package agents

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

// recordCloseFn returns a close-pane stub that captures every paneID
// passed to it. The lifecycle test exercises the agents-side hook in
// isolation by binding this stub via SetCloseTmuxPaneFn — production
// internal/cli's KillTmuxPane never gets called from these tests.
func recordCloseFn(t *testing.T) (*[]string, *sync.Mutex) {
	t.Helper()
	var (
		mu     sync.Mutex
		closed []string
	)
	prev := getCloseTmuxPaneFn()
	SetCloseTmuxPaneFn(func(paneID string) error {
		mu.Lock()
		defer mu.Unlock()
		closed = append(closed, paneID)
		return nil
	})
	t.Cleanup(func() { SetCloseTmuxPaneFn(prev) })
	return &closed, &mu
}

// TestAutoClosePane_OnTerminalStatus asserts the happy path:
// auto-spawn registers a peer with MetaAutoSpawned=true and
// TmuxPane=%42, tryPeerRoute links the taskID → peerID, and on
// terminal status MaybeAutoClosePane fires kill-pane with the
// correct pane id. Models the user's "şişer" scenario: a SendMessage
// auto-spawns a fresh codex pane, the codex agent finishes, the
// pane closes — tmux window list stays bounded.
func TestAutoClosePane_OnTerminalStatus(t *testing.T) {
	resetPeerLifecycleStateForTest()
	t.Cleanup(resetPeerLifecycleStateForTest)

	reg := a2a.NewRegistry(filepath.Join(t.TempDir(), "peers.json"))
	peer, err := reg.Register(a2a.RegisterInput{
		DisplayName: "codex:auto-spawn",
		Backend:     "codex",
		TmuxPane:    "%42",
		Metadata:    map[string]string{MetaAutoSpawned: "true"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	closed, mu := recordCloseFn(t)
	LinkTaskToPeer("task-abc", peer.PeerID)

	if err := MaybeAutoClosePane("task-abc", reg); err != nil {
		t.Fatalf("MaybeAutoClosePane: %v", err)
	}
	mu.Lock()
	got := append([]string(nil), (*closed)...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "%42" {
		t.Fatalf("expected one kill-pane on %%42, got %v", got)
	}

	// Calling MaybeAutoClosePane a second time for the same task is
	// a no-op — the link was consumed by the first call. Without
	// this, a redundant terminal-status hook fire would attempt to
	// double-close and surface a stale-pane error.
	if err := MaybeAutoClosePane("task-abc", reg); err != nil {
		t.Errorf("second MaybeAutoClosePane: %v", err)
	}
	mu.Lock()
	got2 := append([]string(nil), (*closed)...)
	mu.Unlock()
	if len(got2) != 1 {
		t.Errorf("expected exactly ONE kill-pane across two hook fires, got %v", got2)
	}
}

// TestAutoClosePane_SkipsUserAttachedPanes asserts the safety
// invariant: a peer registered without MetaAutoSpawned (i.e. an
// operator's manually-attached SessionStart pane) never gets
// closed even if some lifecycle code path linked its taskID. The
// metadata flag is the gate — without it, kill-pane is never
// fired. Also verifies the empty-link case (taskID never linked
// because the peer wasn't auto-spawned in the first place).
func TestAutoClosePane_SkipsUserAttachedPanes(t *testing.T) {
	resetPeerLifecycleStateForTest()
	t.Cleanup(resetPeerLifecycleStateForTest)

	reg := a2a.NewRegistry(filepath.Join(t.TempDir(), "peers.json"))
	userPeer, err := reg.Register(a2a.RegisterInput{
		DisplayName: "claude-pane",
		Backend:     "claude-code",
		TmuxPane:    "%7",
		// Note: NO MetaAutoSpawned — this is a user-attached pane.
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	closed, mu := recordCloseFn(t)

	// Case 1: taskID was never linked (the realistic flow — tryPeerRoute's
	// IsAutoSpawnedPeer guard returns false for user-attached peers).
	if err := MaybeAutoClosePane("task-without-link", reg); err != nil {
		t.Fatalf("MaybeAutoClosePane (unlinked): %v", err)
	}
	mu.Lock()
	if len(*closed) != 0 {
		t.Errorf("expected zero kill-pane calls on unlinked task, got %v", *closed)
	}
	mu.Unlock()

	// Case 2: defence-in-depth — even if some code path mistakenly
	// linked a user-attached peer, the metadata recheck blocks the
	// kill-pane. This guards against a bug regression in the
	// linker (e.g. an over-eager IsAutoSpawnedPeer that returns true
	// for the wrong peer).
	LinkTaskToPeer("task-misconfigured", userPeer.PeerID)
	if err := MaybeAutoClosePane("task-misconfigured", reg); err != nil {
		t.Fatalf("MaybeAutoClosePane (misconfigured): %v", err)
	}
	mu.Lock()
	got := append([]string(nil), (*closed)...)
	mu.Unlock()
	if len(got) != 0 {
		t.Errorf("metadata check failed to block kill-pane on user-attached peer: %v", got)
	}
}

// TestAutoClosePane_SendMessageEndToEnd is the full-flow simulation
// the spec calls for: stub the close-pane seam, drive a SendMessage
// dispatch through tryPeerRoute (with an auto-spawned peer in the
// registry), flip the BIAM task to TaskDone, and assert the close
// hook fired with the expected pane id. Models the user's complete
// "şişer" recovery: SendMessage auto-spawns → agent finishes → pane
// closes — no operator action required.
func TestAutoClosePane_SendMessageEndToEnd(t *testing.T) {
	resetPeerLifecycleStateForTest()
	t.Cleanup(resetPeerLifecycleStateForTest)
	resetAutoSpawnTracker()
	t.Cleanup(resetAutoSpawnTracker)

	// Real registry + the production a2aRouter so IsAutoSpawnedPeer
	// hits the metadata path. The auto-spawned peer is pre-seeded
	// (we don't run tmux new-window in tests) with the same shape
	// peer_spawn.go's EnsurePeer would produce.
	reg := a2a.NewRegistry(filepath.Join(t.TempDir(), "peers.json"))
	peer, err := reg.Register(a2a.RegisterInput{
		DisplayName: "codex:auto-spawn",
		Backend:     "codex",
		TmuxPane:    "%42",
		Metadata:    map[string]string{MetaAutoSpawned: "true"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	router := NewA2APeerRouter(reg)

	// Wire the close-pane recorder.
	closed, mu := recordCloseFn(t)

	// Spin up an in-memory BIAM store + install the close hook
	// EXACTLY the way internal/server/server.go does. The store
	// path is t.TempDir-rooted so the SQLite file goes away on
	// cleanup.
	store, err := biam.OpenStore(filepath.Join(t.TempDir(), "biam.db"))
	if err != nil {
		t.Fatalf("biam.OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.SetTaskCloseHook(func(taskID string) {
		_ = MaybeAutoClosePane(taskID, reg)
	})

	// Build a supervisor with the real router but no spawner —
	// peer is already registered, so tryPeerRoute hits the
	// FindOnlinePeer-success path and skips auto-spawn.
	sup := newPeerSupervisor(t, router, map[string]bool{"codex": true})
	sup.transports["codex"] = noSpawnTransport{family: "codex"}

	// Create a BIAM task row so SetTaskStatus has something to
	// flip; the dispatch path in production runs through the BIAM
	// runner which CreateTask's the row first.
	const taskID = "task-end-to-end-1"
	if err := store.CreateTask(context.Background(), taskID, "claude/test", "codex"); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// SendMessage carries the task_id via opts["env"] (mirrors
	// what the BIAM runner injects). tryPeerRoute reads this to
	// register the lifecycle link.
	opts := map[string]any{
		"env": map[string]string{"CLAWTOOL_TASK_ID": taskID},
	}
	rc, err := sup.Send(context.Background(), "codex", "do the thing", opts)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !strings.Contains(string(body), "[peer-route]") {
		t.Fatalf("expected peer-route ack, got %q", body)
	}

	// Pane is still open — terminal status hasn't fired yet.
	mu.Lock()
	if len(*closed) != 0 {
		t.Errorf("kill-pane fired before terminal status: %v", *closed)
	}
	mu.Unlock()

	// Flip the task to TaskDone. The store's close hook chain
	// (SetTaskCloseHook) calls MaybeAutoClosePane which consults
	// the link table, the registry metadata, and finally fires
	// the recorded close-pane stub.
	if err := store.SetTaskStatus(context.Background(), taskID, biam.TaskDone, "ok"); err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), (*closed)...)
	mu.Unlock()
	if len(got) != 1 || got[0] != peer.TmuxPane {
		t.Errorf("expected one kill-pane on %s, got %v", peer.TmuxPane, got)
	}
}

// TestAutoClosePane_GateDisabled asserts SetAutoClosePanes(false)
// short-circuits the hook even when every other condition is met.
// Maps to cfg.Peer.AutoClosePanes = false in the daemon — power
// users who want auto-spawned panes to stick around for forensics.
func TestAutoClosePane_GateDisabled(t *testing.T) {
	resetPeerLifecycleStateForTest()
	t.Cleanup(resetPeerLifecycleStateForTest)

	reg := a2a.NewRegistry(filepath.Join(t.TempDir(), "peers.json"))
	peer, err := reg.Register(a2a.RegisterInput{
		DisplayName: "codex:auto-spawn",
		Backend:     "codex",
		TmuxPane:    "%99",
		Metadata:    map[string]string{MetaAutoSpawned: "true"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	closed, mu := recordCloseFn(t)
	SetAutoClosePanes(false)
	LinkTaskToPeer("task-disabled", peer.PeerID)

	if err := MaybeAutoClosePane("task-disabled", reg); err != nil {
		t.Fatalf("MaybeAutoClosePane: %v", err)
	}
	mu.Lock()
	got := append([]string(nil), (*closed)...)
	mu.Unlock()
	if len(got) != 0 {
		t.Errorf("disabled gate failed to block kill-pane: got %v", got)
	}
}
