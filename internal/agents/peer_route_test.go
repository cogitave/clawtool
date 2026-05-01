package agents

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/config"
)

// stubPeerRouter is a hand-rolled PeerRouter for the supervisor tests
// — avoids spinning up a real *a2a.Registry just to exercise the
// peer-prefer decision logic. The auto-spawn tests flip `online` to
// true after the linked spawner's EnsurePeer fires, simulating the
// "spawner just registered the new peer in the registry" race.
type stubPeerRouter struct {
	peerID      string
	displayName string
	online      bool
	autoSpawned bool // when true, IsAutoSpawnedPeer reports true for peerID
	enqueued    []enqueueCall
	enqueueErr  error
}

// stubPeerSpawner records EnsurePeer calls for the auto-spawn tests
// and lets each test pin TmuxAvailable + the err returned by
// EnsurePeer. When `linkedRouter` is set, a successful EnsurePeer
// flips the router to `online=true` so the supervisor's post-spawn
// FindOnlinePeer hits — same race the real a2a.Registry exhibits
// when the spawned agent's first heartbeat arrives.
type stubPeerSpawner struct {
	tmux           bool
	ensureErr      error
	ensuredFamily  []string
	linkedRouter   *stubPeerRouter
	spawnedPeerID  string
	spawnedDisplay string
}

func (s *stubPeerSpawner) TmuxAvailable() bool { return s.tmux }

func (s *stubPeerSpawner) EnsurePeer(family, fromPeerID string) (string, string, bool, error) {
	if s.ensureErr != nil {
		return "", "", false, s.ensureErr
	}
	s.ensuredFamily = append(s.ensuredFamily, family)
	if s.linkedRouter != nil {
		s.linkedRouter.online = true
		if s.spawnedPeerID != "" {
			s.linkedRouter.peerID = s.spawnedPeerID
		}
		if s.spawnedDisplay != "" {
			s.linkedRouter.displayName = s.spawnedDisplay
		}
	}
	return s.spawnedPeerID, s.spawnedDisplay, true, nil
}

type enqueueCall struct{ peerID, fromPeerID, prompt string }

func (s *stubPeerRouter) FindOnlinePeer(family, exclude string) (string, string, bool) {
	if !s.online {
		return "", "", false
	}
	if s.peerID == exclude {
		return "", "", false
	}
	return s.peerID, s.displayName, true
}

func (s *stubPeerRouter) EnqueueToPeer(peerID, fromPeerID, prompt string) (string, error) {
	if s.enqueueErr != nil {
		return "", s.enqueueErr
	}
	s.enqueued = append(s.enqueued, enqueueCall{peerID, fromPeerID, prompt})
	return "msg-" + peerID, nil
}

// IsAutoSpawnedPeer is the stub's pass-through for the lifecycle
// link check. The auto-spawn router test sets `autoSpawned` to true
// before calling Send so tryPeerRoute exercises the
// LinkTaskToPeer branch; default false keeps the legacy peer-prefer
// tests untouched (no link table mutations).
func (s *stubPeerRouter) IsAutoSpawnedPeer(peerID string) bool {
	return s.autoSpawned && peerID == s.peerID
}

// noSpawnTransport panics if Send is invoked — used to assert that a
// peer-route handoff didn't fall through to the spawn path.
type noSpawnTransport struct{ family string }

func (n noSpawnTransport) Family() string { return n.family }
func (n noSpawnTransport) Send(_ context.Context, _ string, _ map[string]any) (io.ReadCloser, error) {
	return nil, errors.New("noSpawnTransport: Send called when peer-route should have handled it")
}

func newPeerSupervisor(t *testing.T, router PeerRouter, binaries map[string]bool) *supervisor {
	t.Helper()
	s := newTestSupervisor(t, config.Config{}, binaries)
	s.peerRouter = router
	return s
}

// TestSend_PrefersLivePeer asserts the operator-quoted scenario:
// "claude'a erişebiliyor musun diye sordum codex'e agentslara gitti"
// — a SendMessage to a registered codex peer drops into that peer's
// inbox instead of spawning a fresh `codex exec` subprocess.
func TestSend_PrefersLivePeer(t *testing.T) {
	router := &stubPeerRouter{
		peerID:      "peer-codex-1",
		displayName: "codex@pane3",
		online:      true,
	}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})
	// Replace codex transport with one that explodes if invoked —
	// proves the spawn path didn't run.
	s.transports["codex"] = noSpawnTransport{family: "codex"}

	rc, err := s.Send(context.Background(), "codex", "hello peer", nil)
	if err != nil {
		t.Fatalf("expected peer-route to succeed, got %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "[peer-route]") {
		t.Errorf("expected peer-route ack body, got %q", string(body))
	}
	if len(router.enqueued) != 1 {
		t.Fatalf("expected one enqueue, got %d", len(router.enqueued))
	}
	if router.enqueued[0].peerID != "peer-codex-1" || router.enqueued[0].prompt != "hello peer" {
		t.Errorf("enqueued call shape unexpected: %+v", router.enqueued[0])
	}
}

// TestSend_FallsBackToSpawnWhenNoPeer asserts the legacy spawn path
// still fires when no peer is registered (peer-prefer is the default;
// missing peer must NOT break existing dispatch).
func TestSend_FallsBackToSpawnWhenNoPeer(t *testing.T) {
	router := &stubPeerRouter{online: false}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})

	rc, err := s.Send(context.Background(), "codex", "hello", nil)
	if err != nil {
		t.Fatalf("expected spawn fallback to succeed, got %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("expected fakeTransport spawn output, got %q", body)
	}
	if len(router.enqueued) != 0 {
		t.Errorf("expected no enqueues on spawn fallback, got %d", len(router.enqueued))
	}
}

// TestSend_PeerOnlyFailsCleanly asserts mode=peer-only refuses to
// spawn when no live peer matches — operator gets a typed error
// instead of a silent fresh-subprocess.
func TestSend_PeerOnlyFailsCleanly(t *testing.T) {
	router := &stubPeerRouter{online: false}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})

	_, err := s.Send(context.Background(), "codex", "hello", map[string]any{
		"mode": "peer-only",
	})
	if err == nil {
		t.Fatal("expected peer-only to fail when no peer matches")
	}
	if !errors.Is(err, ErrNoLivePeer) {
		t.Errorf("expected ErrNoLivePeer, got %v", err)
	}
}

// TestSend_AvoidsSelfDispatch asserts the calling peer is excluded
// from the routing search — without this, a peer asking its own
// family would just route the prompt back to itself in a loop.
func TestSend_AvoidsSelfDispatch(t *testing.T) {
	router := &stubPeerRouter{
		peerID:      "peer-codex-self",
		displayName: "codex@self",
		online:      true,
	}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})

	rc, err := s.Send(context.Background(), "codex", "hello", map[string]any{
		"from_peer_id": "peer-codex-self",
	})
	if err != nil {
		t.Fatalf("expected spawn fallback (self exclusion), got %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("expected fakeTransport spawn output (self-dispatch dodged), got %q", body)
	}
	if len(router.enqueued) != 0 {
		t.Errorf("expected zero enqueues when caller == only candidate peer, got %d", len(router.enqueued))
	}
}

// TestSend_SpawnOnlyBypassesPeerRoute asserts mode=spawn-only skips
// the registry even when a live peer is available — the legacy
// dispatch path stays available for callers that need it.
func TestSend_SpawnOnlyBypassesPeerRoute(t *testing.T) {
	router := &stubPeerRouter{
		peerID: "peer-codex-1", displayName: "codex@pane3", online: true,
	}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})

	rc, err := s.Send(context.Background(), "codex", "hello", map[string]any{
		"mode": "spawn-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("expected spawn output under spawn-only, got %q", body)
	}
	if len(router.enqueued) != 0 {
		t.Errorf("spawn-only must not enqueue; got %d", len(router.enqueued))
	}
}

// TestA2APeerRouter_FamilyMappingAndSelfExclusion exercises the real
// *a2a.Registry adapter end-to-end so the family→backend mapping
// (claude → claude-code), online-only filter, role != orchestrator
// gate, and excludePeerID anti-self check actually work against the
// production registry — not just the stub.
func TestA2APeerRouter_FamilyMappingAndSelfExclusion(t *testing.T) {
	reg := a2a.NewRegistry(filepath.Join(t.TempDir(), "peers.json"))
	router := NewA2APeerRouter(reg)
	if router == nil {
		t.Fatal("expected non-nil router for non-nil registry")
	}

	// Register one online claude-code peer + one orchestrator. Use
	// distinct SessionIDs so the registry's identity-collapse on
	// (backend, path, session, pane) treats them as separate rows.
	agentPeer, err := reg.Register(a2a.RegisterInput{
		DisplayName: "claude-pane",
		Backend:     "claude-code",
		SessionID:   "agent-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := reg.Register(a2a.RegisterInput{
		DisplayName: "claude-orch",
		Backend:     "claude-code",
		Role:        a2a.RoleOrchestrator,
		SessionID:   "orch-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = orch

	// claude (family) → claude-code (backend) lookup.
	got, name, ok := router.FindOnlinePeer("claude", "")
	if !ok || got != agentPeer.PeerID || name != "claude-pane" {
		t.Errorf("expected claude→claude-code mapping to surface agent peer; got id=%q name=%q ok=%v", got, name, ok)
	}

	// excludePeerID == agent → orchestrator is the only remaining
	// candidate, and it's filtered out by role; should miss.
	if _, _, ok := router.FindOnlinePeer("claude", agentPeer.PeerID); ok {
		t.Errorf("expected self-exclusion to leave no candidates (orchestrator filtered)")
	}

	// codex family with no codex peer registered → miss.
	if _, _, ok := router.FindOnlinePeer("codex", ""); ok {
		t.Errorf("expected miss for unregistered family")
	}

	// EnqueueToPeer end-to-end: message lands in the registry's inbox.
	msgID, err := router.EnqueueToPeer(agentPeer.PeerID, "", "ping")
	if err != nil {
		t.Fatal(err)
	}
	if msgID == "" {
		t.Error("expected non-empty msgID")
	}
	msgs := reg.DrainInbox(agentPeer.PeerID, true)
	if len(msgs) != 1 || msgs[0].Text != "ping" {
		t.Errorf("expected one queued message with text 'ping'; got %+v", msgs)
	}

	// EnqueueToPeer to unknown peer → typed error.
	if _, err := router.EnqueueToPeer("nope-uuid", "", "x"); err == nil {
		t.Error("expected error for unknown peer id")
	}

	// nil registry path
	if r := NewA2APeerRouter(nil); r != nil {
		t.Errorf("expected NewA2APeerRouter(nil) → nil, got %T", r)
	}
}

// TestResolveSendMode covers the parser's defaults + env override.
func TestResolveSendMode(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		env  string
		want SendMode
	}{
		{name: "empty defaults to peer-prefer", want: SendModePeerPrefer},
		{name: "explicit peer-prefer", opts: map[string]any{"mode": "peer-prefer"}, want: SendModePeerPrefer},
		{name: "peer-only", opts: map[string]any{"mode": "peer-only"}, want: SendModePeerOnly},
		{name: "spawn-only", opts: map[string]any{"mode": "spawn-only"}, want: SendModeSpawnOnly},
		{name: "auto-tmux", opts: map[string]any{"mode": "auto-tmux"}, want: SendModeAutoTmux},
		{name: "auto-tmux case-insensitive", opts: map[string]any{"mode": "Auto-Tmux"}, want: SendModeAutoTmux},
		{name: "unknown defaults to peer-prefer", opts: map[string]any{"mode": "wat"}, want: SendModePeerPrefer},
		{name: "case-insensitive", opts: map[string]any{"mode": "Peer-Only"}, want: SendModePeerOnly},
		{name: "env=0 forces spawn-only", env: "0", opts: map[string]any{"mode": "peer-only"}, want: SendModeSpawnOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("CLAWTOOL_PEER_ROUTING", tc.env)
			} else {
				t.Setenv("CLAWTOOL_PEER_ROUTING", "")
			}
			got := resolveSendMode(tc.opts)
			if got != tc.want {
				t.Errorf("resolveSendMode(%+v) = %q; want %q", tc.opts, got, tc.want)
			}
		})
	}
}

// newAutoSpawnSupervisor wires both peerRouter + peerSpawner stubs
// and resets the package-global cooldown tracker so a previous test
// firing a spawn can't shadow this one's first call. Returns the
// supervisor + the linked router/spawner pair so each test can read
// the spawner's call log.
func newAutoSpawnSupervisor(t *testing.T, family string, tmux bool) (*supervisor, *stubPeerRouter, *stubPeerSpawner) {
	t.Helper()
	resetAutoSpawnTracker()
	t.Cleanup(resetAutoSpawnTracker)
	router := &stubPeerRouter{online: false}
	spawner := &stubPeerSpawner{
		tmux:           tmux,
		linkedRouter:   router,
		spawnedPeerID:  "peer-" + family + "-spawned",
		spawnedDisplay: family + "@auto-pane",
	}
	s := newPeerSupervisor(t, router, map[string]bool{family: true})
	s.peerSpawner = spawner
	return s, router, spawner
}

// TestRouting_AutoSpawnTmuxOnNoPeer asserts the zero-touch case:
// peer-prefer + no live peer + tmux available → clawtool spawns a
// new pane, registers the peer, and routes to it.
func TestRouting_AutoSpawnTmuxOnNoPeer(t *testing.T) {
	s, router, spawner := newAutoSpawnSupervisor(t, "codex", true /*tmux*/)
	// noSpawnTransport asserts the legacy fresh-subprocess path is
	// NOT taken — the route must land in the auto-spawned peer's
	// inbox instead.
	s.transports["codex"] = noSpawnTransport{family: "codex"}

	rc, err := s.Send(context.Background(), "codex", "hello new pane", nil)
	if err != nil {
		t.Fatalf("expected auto-spawn route to succeed, got %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "[peer-route]") {
		t.Errorf("expected peer-route ack after auto-spawn, got %q", body)
	}
	if got := spawner.ensuredFamily; len(got) != 1 || got[0] != "codex" {
		t.Errorf("expected exactly one EnsurePeer(codex) call, got %v", got)
	}
	if len(router.enqueued) != 1 {
		t.Fatalf("expected one enqueue to the auto-spawned peer, got %d", len(router.enqueued))
	}
	if router.enqueued[0].peerID != "peer-codex-spawned" {
		t.Errorf("enqueue routed to wrong peer: %+v", router.enqueued[0])
	}
}

// TestRouting_AutoSpawnIdempotent asserts five SendMessage calls in
// quick succession to a non-existent codex peer produce ONE auto-
// spawn (not five tmux panes). The cooldown debounces concurrent
// calls; after the first spawn the rest reuse the just-registered
// peer.
func TestRouting_AutoSpawnIdempotent(t *testing.T) {
	s, router, spawner := newAutoSpawnSupervisor(t, "codex", true /*tmux*/)
	s.transports["codex"] = noSpawnTransport{family: "codex"}

	for i := 0; i < 5; i++ {
		rc, err := s.Send(context.Background(), "codex", "ping", nil)
		if err != nil {
			t.Fatalf("call %d: expected success, got %v", i, err)
		}
		_ = rc.Close()
	}
	if got := len(spawner.ensuredFamily); got != 1 {
		t.Errorf("expected exactly ONE EnsurePeer call across 5 SendMessage calls, got %d (%v)", got, spawner.ensuredFamily)
	}
	if got := len(router.enqueued); got != 5 {
		t.Errorf("expected 5 enqueues (route through reused peer); got %d", got)
	}
}

// TestRouting_AutoSpawnFallsBackWhenNoTmux asserts peer-prefer +
// no-tmux falls through to the legacy spawn-fresh-subprocess path
// rather than failing — operators on hosts without tmux see no
// regression. EnsurePeer is NOT called.
func TestRouting_AutoSpawnFallsBackWhenNoTmux(t *testing.T) {
	s, router, spawner := newAutoSpawnSupervisor(t, "codex", false /*no tmux*/)
	// Default codex transport is the fakeTransport; we expect its
	// "codex-out|" output, proving the fresh-subprocess path ran.

	rc, err := s.Send(context.Background(), "codex", "hello", nil)
	if err != nil {
		t.Fatalf("expected legacy spawn fallback to succeed, got %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("expected fakeTransport output (legacy spawn path), got %q", body)
	}
	if len(spawner.ensuredFamily) != 0 {
		t.Errorf("expected EnsurePeer NOT to be called when tmux is absent, got %v", spawner.ensuredFamily)
	}
	if len(router.enqueued) != 0 {
		t.Errorf("expected no enqueue (fell through to spawn), got %d", len(router.enqueued))
	}
}

// TestSendMessage_AutoCloseFalseSkipsLink asserts ADR-034 Q3: when
// the caller passes opts["auto_close"]=false on a SendMessage
// landing in an auto-spawned peer, tryPeerRoute MUST NOT call
// LinkTaskToPeer. Without the link, the BIAM terminal-status hook
// can't find the task → peer mapping and skips the kill-pane —
// the operator's pane stays alive for inspection.
func TestSendMessage_AutoCloseFalseSkipsLink(t *testing.T) {
	resetPeerLifecycleStateForTest()
	t.Cleanup(resetPeerLifecycleStateForTest)

	router := &stubPeerRouter{
		peerID:      "peer-codex-1",
		displayName: "codex@pane3",
		online:      true,
		autoSpawned: true, // simulate IsAutoSpawnedPeer == true
	}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})
	s.transports["codex"] = noSpawnTransport{family: "codex"}

	rc, err := s.Send(context.Background(), "codex", "do not link me", map[string]any{
		"env":        map[string]string{"CLAWTOOL_TASK_ID": "task-no-close"},
		"auto_close": false,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "[peer-route]") {
		t.Fatalf("expected peer-route ack, got %q", body)
	}

	// Verify the link table is empty for this task — the lifecycle
	// hook would never find a row to close even if it ran.
	taskPeerLinkMu.Lock()
	_, linked := taskPeerLink["task-no-close"]
	taskPeerLinkMu.Unlock()
	if linked {
		t.Errorf("auto_close=false MUST NOT register a lifecycle link; taskPeerLink has the row")
	}
}

// TestSendMessage_AutoCloseTrueLinksAsBefore asserts the legacy
// behaviour stays intact when auto_close is unset (default = true)
// or explicitly true: the link gets registered and the lifecycle
// hook can act on it. Mirror of the previous test — same shape so
// regression on either branch is easy to spot.
func TestSendMessage_AutoCloseTrueLinksAsBefore(t *testing.T) {
	resetPeerLifecycleStateForTest()
	t.Cleanup(resetPeerLifecycleStateForTest)

	router := &stubPeerRouter{
		peerID:      "peer-codex-1",
		displayName: "codex@pane3",
		online:      true,
		autoSpawned: true,
	}
	s := newPeerSupervisor(t, router, map[string]bool{"codex": true})
	s.transports["codex"] = noSpawnTransport{family: "codex"}

	// Sub-test 1: explicit auto_close=true.
	rc, err := s.Send(context.Background(), "codex", "default close", map[string]any{
		"env":        map[string]string{"CLAWTOOL_TASK_ID": "task-explicit-true"},
		"auto_close": true,
	})
	if err != nil {
		t.Fatalf("Send (explicit true): %v", err)
	}
	_ = rc.Close()
	taskPeerLinkMu.Lock()
	pid := taskPeerLink["task-explicit-true"]
	taskPeerLinkMu.Unlock()
	if pid != "peer-codex-1" {
		t.Errorf("auto_close=true should register link; got pid=%q", pid)
	}

	// Sub-test 2: omitted (default).
	rc2, err := s.Send(context.Background(), "codex", "default close", map[string]any{
		"env": map[string]string{"CLAWTOOL_TASK_ID": "task-default"},
	})
	if err != nil {
		t.Fatalf("Send (default): %v", err)
	}
	_ = rc2.Close()
	taskPeerLinkMu.Lock()
	pid2 := taskPeerLink["task-default"]
	taskPeerLinkMu.Unlock()
	if pid2 != "peer-codex-1" {
		t.Errorf("default (auto_close unset) should register link; got pid=%q", pid2)
	}
}

// TestRouting_ModeAutoTmuxFailsWithoutTmux asserts mode=auto-tmux
// refuses to fall back: when no tmux is detected the caller sees a
// typed ErrTmuxUnavailable instead of a silent fresh-subprocess.
func TestRouting_ModeAutoTmuxFailsWithoutTmux(t *testing.T) {
	s, _, spawner := newAutoSpawnSupervisor(t, "codex", false /*no tmux*/)

	_, err := s.Send(context.Background(), "codex", "hello", map[string]any{
		"mode": "auto-tmux",
	})
	if err == nil {
		t.Fatal("expected auto-tmux to fail when tmux is absent")
	}
	if !errors.Is(err, ErrTmuxUnavailable) {
		t.Errorf("expected ErrTmuxUnavailable, got %v", err)
	}
	if len(spawner.ensuredFamily) != 0 {
		t.Errorf("auto-tmux must NOT call EnsurePeer when tmux is unavailable, got %v", spawner.ensuredFamily)
	}
}
