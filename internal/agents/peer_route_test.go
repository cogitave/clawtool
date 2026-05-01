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
// peer-prefer decision logic.
type stubPeerRouter struct {
	peerID      string
	displayName string
	online      bool
	enqueued    []enqueueCall
	enqueueErr  error
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
