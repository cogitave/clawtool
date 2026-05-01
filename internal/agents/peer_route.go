// Package agents — peer-prefer routing for SendMessage. Routes a
// dispatch into a registered live BIAM peer's inbox before falling
// back to spawning a fresh `<family> exec` subprocess. Solves the
// "claude'a sordum codex'e agentslara gitti" surprise: an operator's
// open codex pane should receive prompts addressed to codex instead
// of being shadowed by an invisible fresh subprocess.
//
// Mode flag (opts["mode"]): "peer-prefer" (default) | "peer-only" |
// "spawn-only" | "auto-tmux". Env override CLAWTOOL_PEER_ROUTING=0
// forces spawn-only for one release while migrations land.
//
// Zero-touch auto-spawn: peer-prefer + no online peer + tmux session
// active → clawtool transparently spawns the agent in a fresh tmux
// pane, registers it as a peer, and routes the prompt there. The
// operator never has to remember `clawtool spawn` — sending a message
// to an absent peer brings it to life. auto-tmux is the explicit
// "require tmux delivery or fail" variant for callers that want the
// guarantee.
package agents

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
)

// SendMode is the typed routing-preference for Supervisor.Send.
type SendMode string

const (
	SendModePeerPrefer SendMode = "peer-prefer"
	SendModePeerOnly   SendMode = "peer-only"
	SendModeSpawnOnly  SendMode = "spawn-only"
	// SendModeAutoTmux requires the auto-spawn path to land the
	// agent in a tmux pane; no-tmux → typed ErrTmuxUnavailable.
	// Useful when the operator wants the live-pane guarantee
	// (e.g. so they can watch the agent's progress) instead of
	// the silent spawn-fresh-subprocess fallback.
	SendModeAutoTmux SendMode = "auto-tmux"
)

// ErrNoLivePeer is returned by peer-only mode when no online peer
// matches the resolved family. Typed so MCP / CLI surfaces can
// render a guided error instead of treating it as a generic dispatch
// failure.
var ErrNoLivePeer = errors.New("peer-only mode: no online peer matches the resolved family")

// ErrTmuxUnavailable is returned by auto-tmux mode when no tmux
// session is detected. Typed so the calling agent can recover by
// retrying with peer-prefer (or asking the operator to start a tmux
// session) instead of treating it as a generic dispatch failure.
var ErrTmuxUnavailable = errors.New("auto-tmux mode: no tmux session detected")

// PeerRouter is the small subset of *a2a.Registry the supervisor
// needs for peer-prefer routing. Defining it as an interface keeps
// the agents package decoupled from the registry's full API surface
// and makes the routing path unit-testable without spinning up a
// full a2a registry.
type PeerRouter interface {
	// FindOnlinePeer returns the first online peer whose backend
	// matches `family` AND whose role != orchestrator AND whose
	// peer_id != excludePeerID (anti-self-dispatch). Returns
	// (peer_id, ok) — empty peer_id + false means no match.
	FindOnlinePeer(family, excludePeerID string) (peerID, displayName string, ok bool)

	// EnqueueToPeer drops the prompt into peerID's inbox as a
	// query message. Returns the assigned message ID so the
	// caller can correlate replies later.
	EnqueueToPeer(peerID, fromPeerID, prompt string) (msgID string, err error)
}

// globalPeerRouter is the process-wide router NewSupervisor wires.
// Server boot (or test setup) calls SetGlobalPeerRouter; everything
// else picks it up implicitly. nil = peer-prefer falls through to
// spawn (legacy behavior preserved when the daemon's a2a registry
// hasn't been initialised).
var (
	globalPeerRouterMu sync.RWMutex
	globalPeerRouter   PeerRouter
)

// SetGlobalPeerRouter registers the process-wide router. Pass nil to
// clear (e.g. daemon shutdown). Idempotent.
func SetGlobalPeerRouter(r PeerRouter) {
	globalPeerRouterMu.Lock()
	defer globalPeerRouterMu.Unlock()
	globalPeerRouter = r
}

// GetGlobalPeerRouter returns the process-wide router or nil.
func GetGlobalPeerRouter() PeerRouter {
	globalPeerRouterMu.RLock()
	defer globalPeerRouterMu.RUnlock()
	return globalPeerRouter
}

// a2aRouter adapts an *a2a.Registry to the PeerRouter interface.
// Lives in the agents package so a2a stays a leaf dependency
// (a2a → atomicfile + xdg, no agents import).
type a2aRouter struct{ reg *a2a.Registry }

// NewA2APeerRouter wraps a registry. Returns nil when reg is nil so
// the daemon's "registry not initialised" path stays ergonomic.
func NewA2APeerRouter(reg *a2a.Registry) PeerRouter {
	if reg == nil {
		return nil
	}
	return &a2aRouter{reg: reg}
}

// familyToBackend maps the supervisor's family vocabulary
// (claude / codex / gemini / opencode / hermes / aider) to the
// a2a registry's backend vocabulary. The two diverge for "claude"
// only: peers register as "claude-code" (the runtime name) while
// the supervisor's transport key is "claude" (the family name).
func familyToBackend(family string) string {
	switch family {
	case "claude":
		return "claude-code"
	default:
		return family
	}
}

func (a *a2aRouter) FindOnlinePeer(family, excludePeerID string) (string, string, bool) {
	if a == nil || a.reg == nil {
		return "", "", false
	}
	peers := a.reg.List(a2a.ListFilter{
		Backend: familyToBackend(family),
		Status:  a2a.PeerOnline,
	})
	for _, p := range peers {
		if p.Role == a2a.RoleOrchestrator {
			continue
		}
		if excludePeerID != "" && p.PeerID == excludePeerID {
			continue
		}
		return p.PeerID, p.DisplayName, true
	}
	return "", "", false
}

func (a *a2aRouter) EnqueueToPeer(peerID, fromPeerID, prompt string) (string, error) {
	if a == nil || a.reg == nil {
		return "", errors.New("peer router: registry not initialised")
	}
	if a.reg.Get(peerID) == nil {
		return "", fmt.Errorf("peer router: no peer with id %q", peerID)
	}
	saved := a.reg.SendTo(peerID, a2a.Message{
		Type:     a2a.MsgQuery,
		FromPeer: fromPeerID,
		ToPeer:   peerID,
		Text:     prompt,
	})
	return saved.ID, nil
}

// resolveSendMode parses opts["mode"] into a typed SendMode. Empty
// string defaults to peer-prefer. Unknown values are tolerated as
// peer-prefer so a forward-compat caller passing a future mode name
// still gets reasonable behavior (we don't 400 on a typo).
//
// Env-var escape hatch: CLAWTOOL_PEER_ROUTING=0 forces spawn-only
// regardless of opts. Documented in the package comment so an
// operator hitting a regression has a one-flag rollback.
func resolveSendMode(opts map[string]any) SendMode {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CLAWTOOL_PEER_ROUTING")), "0") {
		return SendModeSpawnOnly
	}
	if opts == nil {
		return SendModePeerPrefer
	}
	v, _ := opts["mode"].(string)
	switch SendMode(strings.TrimSpace(strings.ToLower(v))) {
	case SendModePeerOnly:
		return SendModePeerOnly
	case SendModeSpawnOnly:
		return SendModeSpawnOnly
	case SendModeAutoTmux:
		return SendModeAutoTmux
	case SendModePeerPrefer, "":
		return SendModePeerPrefer
	default:
		return SendModePeerPrefer
	}
}

// callerPeerID extracts opts["from_peer_id"] when set so peer-prefer
// can avoid dispatching back to the same peer that just initiated
// the call. Empty string when not supplied — anti-self-dispatch
// check is then a no-op (the registry returns the first matching
// peer, which is fine for the normal "claude in pane A asking
// codex in pane B" flow).
func callerPeerID(opts map[string]any) string {
	if opts == nil {
		return ""
	}
	if v, ok := opts["from_peer_id"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// PeerSpawner is the indirection seam for the auto-spawn path. When
// peer-prefer (or auto-tmux) finds no online peer, the supervisor
// asks the spawner to bring one to life — typically by opening a
// new tmux pane, registering the freshly spawned agent in the BIAM
// peer registry, and returning the assigned peer_id. Defining it as
// an interface keeps the agents package decoupled from the
// internal/cli spawn machinery (which depends on daemon HTTP) and
// makes the auto-spawn flow unit-testable.
type PeerSpawner interface {
	// TmuxAvailable reports whether a tmux session is currently
	// active in this process's environment ($TMUX is set + the
	// `tmux` binary is on PATH). The supervisor checks this
	// before invoking EnsurePeer so peer-prefer can fall through
	// to the spawn-fresh-subprocess path on no-tmux hosts and
	// auto-tmux can fail with ErrTmuxUnavailable.
	TmuxAvailable() bool

	// EnsurePeer ensures a peer for the given family is alive
	// and registered. Implementations should be idempotent at
	// the family level — repeated calls within a small cooldown
	// window return the same peer rather than spawning a second
	// pane. Returns the peer_id + display name + a flag noting
	// whether the call actually fired a spawn (false = reused
	// an existing peer).
	EnsurePeer(family, fromPeerID string) (peerID, displayName string, spawned bool, err error)
}

// globalPeerSpawner is the process-wide spawner NewSupervisor wires.
// Server boot calls SetGlobalPeerSpawner; tests inject stubs by
// setting supervisor.peerSpawner directly. nil = auto-spawn path is
// disabled and peer-prefer falls through to the spawn-fresh-
// subprocess legacy behavior (auto-tmux returns ErrTmuxUnavailable).
var (
	globalPeerSpawnerMu sync.RWMutex
	globalPeerSpawner   PeerSpawner
)

// SetGlobalPeerSpawner registers the process-wide spawner. Pass nil
// to clear (e.g. daemon shutdown). Idempotent.
func SetGlobalPeerSpawner(s PeerSpawner) {
	globalPeerSpawnerMu.Lock()
	defer globalPeerSpawnerMu.Unlock()
	globalPeerSpawner = s
}

// GetGlobalPeerSpawner returns the process-wide spawner or nil.
func GetGlobalPeerSpawner() PeerSpawner {
	globalPeerSpawnerMu.RLock()
	defer globalPeerSpawnerMu.RUnlock()
	return globalPeerSpawner
}

// autoSpawnCooldown is the per-family debounce window. SendMessage
// fired five times in a second to a missing codex peer must NOT
// produce five tmux panes — within the window, the second through
// fifth calls reuse the just-spawned peer. The window is generous
// enough to cover the agent's own boot + first peer registration
// roundtrip but tight enough that a genuine respawn-after-crash
// scenario isn't blocked.
const autoSpawnCooldown = 10 * time.Second

// autoSpawnDeadline caps a single tmux-new-window call. Generous
// for a local pane spawn (5s); tight enough that a wedged tmux
// server surfaces as a typed error rather than a hung SendMessage
// handler. Lives here (not in peer_spawn.go) so the cooldown +
// deadline budgets sit alongside each other for ops review.
const autoSpawnDeadline = 5 * time.Second

// autoSpawnTracker debounces auto-spawn calls per (family). Lives at
// package scope so every supervisor in the process shares one
// cooldown view — otherwise five concurrent NewSupervisor instances
// each with their own tracker would defeat the rate-limit.
var (
	autoSpawnTrackerMu sync.Mutex
	autoSpawnLastFire  = map[string]time.Time{}
)

// shouldAutoSpawn reports whether `family` is outside the cooldown
// window. True → caller proceeds with EnsurePeer. False → caller
// re-checks the registry (the just-spawned peer should be there)
// before falling through. The registered fire-time is updated on
// `true` so concurrent SendMessage calls funnel through the first
// spawn.
func shouldAutoSpawn(family string) bool {
	autoSpawnTrackerMu.Lock()
	defer autoSpawnTrackerMu.Unlock()
	last, ok := autoSpawnLastFire[family]
	if ok && time.Since(last) < autoSpawnCooldown {
		return false
	}
	autoSpawnLastFire[family] = time.Now()
	return true
}

// resetAutoSpawnTracker is a test-only helper. The cooldown map is
// process-global, so a previous test that fired a spawn would block
// the next test for autoSpawnCooldown seconds without this.
func resetAutoSpawnTracker() {
	autoSpawnTrackerMu.Lock()
	defer autoSpawnTrackerMu.Unlock()
	autoSpawnLastFire = map[string]time.Time{}
}

// newPeerAckStream returns a synthetic ReadCloser confirming the
// peer-route handoff. The body is human-readable (not NDJSON) so the
// calling agent's buffered SendMessage reply renders it directly to
// the operator. Real reply tracking lives in BIAM TaskReply and the
// peer drain hook — this stream is just the handoff acknowledgement.
func newPeerAckStream(peerID, displayName, msgID string) io.ReadCloser {
	short := peerID
	if len(short) > 8 {
		short = short[:8]
	}
	name := displayName
	if name == "" {
		name = "peer"
	}
	body := fmt.Sprintf(
		"[peer-route] enqueued prompt to %s (peer %s, msg %s).\n"+
			"The peer picks it up via `clawtool peer inbox` (or its UserPromptSubmit hook).\n"+
			"Track replies via the peer's drain stream / TaskReply envelopes.\n",
		name, short, msgID,
	)
	return io.NopCloser(bytes.NewReader([]byte(body)))
}
