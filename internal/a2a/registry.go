// Package a2a — peer registry. Phase 1 of ADR-024's local-mesh
// half: every running clawtool / claude-code / codex / gemini /
// opencode session on this host registers into a single in-memory
// table keyed on a stable peer_id, so `clawtool a2a peers` can
// surface the live roster.
//
// Mirrors the shape of repowire/daemon/peer_registry.py
// (prassanna-ravishankar/repowire) — the reference implementation
// for the discovery half. Differences from repowire:
//   - Identity tuple: (backend, path, session_id, tmux_pane). The
//     runtime-supplied session_id (claude-code's hook payload
//     `.session_id`, etc.) is the primary disambiguator so two
//     parallel sessions in the same cwd register as separate
//     peers. tmux_pane is the secondary key when no session id
//     exists.
//   - REST + 30s heartbeat instead of WebSocket transport. The
//     real-time push notifications repowire offers via websocket
//     are deferred to Phase 2; Phase 1 ships the registry +
//     polling because it's a fraction of the LoC and covers 80%
//     of the operator value (visibility, cross-pane discovery).
//
// Persistence: ~/.config/clawtool/peers.json (LF-delimited JSON,
// 0600). Atomic temp+rename writes so a crash mid-write doesn't
// leave a corrupt state file. Lazy repair on every read sweeps
// peers whose declared `path` no longer exists.
package a2a

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/google/uuid"
)

// PeerStatus is the lifecycle marker every peer carries.
type PeerStatus string

const (
	PeerOnline  PeerStatus = "online"
	PeerBusy    PeerStatus = "busy"
	PeerOffline PeerStatus = "offline"
)

// PeerRole differentiates dispatchers (orchestrators) from
// dispatchees (worker agents). Most peers are agents; an
// operator running multiple terminals manually flips one to
// orchestrator if they want it to coordinate the others.
type PeerRole string

const (
	RoleAgent        PeerRole = "agent"
	RoleOrchestrator PeerRole = "orchestrator"
)

// HeartbeatStaleAfter — peers whose last_seen is older than
// this are flipped to PeerOffline on the next list. Matches the
// 30 s heartbeat cadence we recommend in the registration docs
// (one missed heartbeat = grace period; two missed = offline).
const HeartbeatStaleAfter = 60 * time.Second

// Peer is the single source of truth for one registered session.
// Field names are JSON-serialised verbatim so the wire shape
// (the `/v1/peers` endpoint) reflects the in-memory model
// directly.
type Peer struct {
	PeerID       string            `json:"peer_id"`
	DisplayName  string            `json:"display_name"`
	Path         string            `json:"path,omitempty"`
	Backend      string            `json:"backend"` // claude-code | codex | gemini | opencode | clawtool
	Circle       string            `json:"circle"`  // group name; defaults to tmux session or "default"
	Role         PeerRole          `json:"role"`
	Status       PeerStatus        `json:"status"`
	SessionID    string            `json:"session_id,omitempty"` // runtime-supplied session key (claude-code: hook payload .session_id)
	TmuxPane     string            `json:"tmux_pane,omitempty"`
	PID          int               `json:"pid,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	RegisteredAt time.Time         `json:"registered_at"`
	LastSeen     time.Time         `json:"last_seen"`
}

// Registry is the process-wide peer table. One instance lives in
// the daemon for the lifetime of the process; constructed via
// NewRegistry which loads any persisted state.
type Registry struct {
	mu           sync.RWMutex
	peers        map[string]*Peer
	statePath    string
	dirty        bool
	persistEvery time.Duration // debounce — we save at most once per interval
	lastSave     time.Time

	// Inbox lane. Lazy-allocated on first SendTo / DrainInbox.
	// Separate mutex from `mu` so a chatty sender doesn't block
	// the registry's hot path (List, Heartbeat). The inbox layer
	// has its own per-peer locking inside Inbox.mu.
	boxMu   sync.Mutex
	inboxes *inboxes
}

// NewRegistry constructs an empty registry, then attempts to load
// state from path. A missing / unreadable / corrupt file is
// non-fatal: we start with an empty table and log to stderr.
func NewRegistry(statePath string) *Registry {
	r := &Registry{
		peers:        map[string]*Peer{},
		statePath:    statePath,
		persistEvery: 2 * time.Second,
	}
	if err := r.load(); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool a2a: peer registry load failed (starting empty): %v\n", err)
	}
	return r
}

// DefaultStatePath returns ~/.config/clawtool/peers.json (or its
// XDG_CONFIG_HOME equivalent). Mirrors daemon.StatePath's
// convention so an operator inspecting the config dir sees
// daemon.json + peers.json side-by-side.
func DefaultStatePath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "clawtool", "peers.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "clawtool", "peers.json")
	}
	return "peers.json"
}

// RegisterInput is the shape callers supply to Register. Mirrors
// the JSON body of POST /v1/peers/register so the HTTP handler
// is a thin marshaller.
type RegisterInput struct {
	DisplayName string            `json:"display_name"`
	Path        string            `json:"path,omitempty"`
	Backend     string            `json:"backend"`
	Circle      string            `json:"circle,omitempty"`
	Role        PeerRole          `json:"role,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
	TmuxPane    string            `json:"tmux_pane,omitempty"`
	PID         int               `json:"pid,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Register adds a new peer (or refreshes an existing one with the
// same identity tuple) and returns the assigned peer_id. Idempotent:
// repeated calls with the same backend + path + tmux_pane + pubkey
// update the existing row's last_seen instead of creating a
// duplicate. Without this, every hook fire would multiply the
// peer table.
func (r *Registry) Register(in RegisterInput) (*Peer, error) {
	if in.Backend == "" {
		return nil, errors.New("a2a registry: backend is required")
	}
	if in.DisplayName == "" {
		return nil, errors.New("a2a registry: display_name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Idempotency: collapse on the natural identity tuple.
	if existing := r.findByIdentity(in.Backend, in.Path, in.SessionID, in.TmuxPane); existing != nil {
		existing.LastSeen = time.Now().UTC()
		existing.Status = PeerOnline
		// Also pick up any metadata refresh — operator may
		// have updated their circle name or PID.
		if in.Circle != "" {
			existing.Circle = in.Circle
		}
		if in.PID > 0 {
			existing.PID = in.PID
		}
		if in.Role != "" {
			existing.Role = in.Role
		}
		if len(in.Metadata) > 0 {
			if existing.Metadata == nil {
				existing.Metadata = map[string]string{}
			}
			for k, v := range in.Metadata {
				existing.Metadata[k] = v
			}
		}
		r.markDirty()
		return existing, nil
	}

	peer := &Peer{
		PeerID:       uuid.NewString(),
		DisplayName:  in.DisplayName,
		Path:         in.Path,
		Backend:      in.Backend,
		Circle:       defaultIfEmpty(in.Circle, "default"),
		Role:         defaultRoleIfEmpty(in.Role, RoleAgent),
		Status:       PeerOnline,
		SessionID:    in.SessionID,
		TmuxPane:     in.TmuxPane,
		PID:          in.PID,
		Metadata:     cloneMeta(in.Metadata),
		RegisteredAt: time.Now().UTC(),
		LastSeen:     time.Now().UTC(),
	}
	r.peers[peer.PeerID] = peer
	r.markDirty()
	return peer, nil
}

// Heartbeat refreshes a peer's last_seen + status. Returns
// nil-error / nil-peer when the peer_id is unknown; that's the
// "I just registered, then noticed my session ID was wrong"
// case — caller should re-register, not retry.
func (r *Registry) Heartbeat(peerID string, status PeerStatus) (*Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.peers[peerID]
	if !ok {
		return nil, nil
	}
	p.LastSeen = time.Now().UTC()
	if status != "" {
		p.Status = status
	}
	r.markDirty()
	return p, nil
}

// Deregister removes a peer outright. Used by SessionEnd hooks
// when the session is shutting down cleanly. Returns the
// removed peer (or nil) so callers can surface a "peer X went
// offline" event. Also drops the peer's inbox so deregistered
// sessions don't leave persisted mailboxes behind.
func (r *Registry) Deregister(peerID string) (*Peer, error) {
	r.mu.Lock()
	p, ok := r.peers[peerID]
	if !ok {
		r.mu.Unlock()
		return nil, nil
	}
	delete(r.peers, peerID)
	r.markDirty()
	r.mu.Unlock()
	r.dropInbox(peerID)
	return p, nil
}

// ListFilter narrows the result set returned by List. Empty
// fields are no-ops so callers can pass {Backend: "claude-code"}
// to see just claude peers.
type ListFilter struct {
	Status  PeerStatus
	Path    string
	Backend string
	Circle  string
}

// List returns every peer matching the filter. Lazy-repair runs
// inline: peers whose last_seen is older than HeartbeatStaleAfter
// flip to PeerOffline before the result is built; peers whose
// declared path no longer exists are dropped entirely. Sort
// order: online first, then by display_name lexicographic — so
// `clawtool a2a peers` reads top-down "currently active first".
func (r *Registry) List(filter ListFilter) []Peer {
	now := time.Now().UTC()
	r.mu.Lock()
	for id, p := range r.peers {
		if p.Path != "" {
			if _, err := os.Stat(p.Path); err != nil && os.IsNotExist(err) {
				delete(r.peers, id)
				r.markDirty()
				continue
			}
		}
		if p.Status != PeerOffline && now.Sub(p.LastSeen) > HeartbeatStaleAfter {
			p.Status = PeerOffline
			r.markDirty()
		}
	}
	out := make([]Peer, 0, len(r.peers))
	for _, p := range r.peers {
		if !filter.match(*p) {
			continue
		}
		out = append(out, *p) // value copy — caller can't mutate the registry
	}
	r.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return statusRank(out[i].Status) < statusRank(out[j].Status)
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out
}

// Get returns one peer by ID, or nil when unknown. Pure read,
// no lazy-repair (the lazy sweep is List's job).
func (r *Registry) Get(peerID string) *Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.peers[peerID]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// Save persists the registry to its state path. Atomic via
// temp+rename so a crash mid-write doesn't leave a half-formed
// JSON. Idempotent — if dirty=false, no I/O happens.
func (r *Registry) Save() error {
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return nil
	}
	r.dirty = false
	r.lastSave = time.Now()
	data := make(map[string]Peer, len(r.peers))
	for id, p := range r.peers {
		data[id] = *p
	}
	statePath := r.statePath
	r.mu.Unlock()

	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFileMkdir(statePath, append(body, '\n'), 0o600, 0o700)
}

// load reads peers.json into the registry. Missing file is not
// an error (the registry just starts empty). Parse errors are
// returned so callers can decide whether to fail-fast or
// degrade.
func (r *Registry) load() error {
	body, err := os.ReadFile(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var data map[string]Peer
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("parse %s: %w", r.statePath, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range data {
		cp := p
		// Persisted peers come back online-eligible: lazy_repair
		// in List() flips them to offline if the heartbeat is
		// stale. Without this every daemon restart would treat
		// every peer as offline forever.
		r.peers[id] = &cp
	}
	return nil
}

// findByIdentity collapses re-registration calls onto the same
// peer row. Two peers are "the same" when their (backend, path,
// session_id, tmux_pane) tuple matches. Empty strings count as
// wildcards so a SessionStart hook that doesn't know the tmux
// pane still finds an existing peer with the same backend+path+
// session. session_id is the primary disambiguator for runtimes
// that supply it (claude-code's hook payload, codex/gemini
// equivalents) — without it, two parallel claude-code sessions
// in the same cwd would collapse onto one row. Caller must hold
// r.mu.
func (r *Registry) findByIdentity(backend, path, session, pane string) *Peer {
	for _, p := range r.peers {
		if p.Backend != backend {
			continue
		}
		if path != "" && p.Path != path {
			continue
		}
		if session != "" && p.SessionID != session {
			continue
		}
		if pane != "" && p.TmuxPane != pane {
			continue
		}
		return p
	}
	return nil
}

func (r *Registry) markDirty() { r.dirty = true }

func (f ListFilter) match(p Peer) bool {
	if f.Status != "" && p.Status != f.Status {
		return false
	}
	if f.Backend != "" && p.Backend != f.Backend {
		return false
	}
	if f.Circle != "" && p.Circle != f.Circle {
		return false
	}
	if f.Path != "" && p.Path != f.Path {
		return false
	}
	return true
}

func statusRank(s PeerStatus) int {
	switch s {
	case PeerOnline:
		return 0
	case PeerBusy:
		return 1
	case PeerOffline:
		return 2
	default:
		return 3
	}
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func defaultRoleIfEmpty(r, fallback PeerRole) PeerRole {
	if r == "" {
		return fallback
	}
	return r
}

func cloneMeta(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
