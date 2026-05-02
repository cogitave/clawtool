// Package checkpoint — Guard middleware (defense-in-depth atop
// ADR-021 Read-before-Write).
//
// The contract:
//
//   - ADR-021 (Read-before-Write) catches the most common failure
//     mode: an agent overwriting a file it never Read this session.
//     The guard there is per-file and per-session.
//   - This Guard is per-process, file-agnostic, and counts EDITS,
//     not files. It answers a different question: "how many
//     mutations have piled up since the last checkpoint?"
//   - When Enabled = true and the counter reaches N (default 5),
//     the next pre-edit Check() returns ErrCheckpointRequired.
//     The Edit/Write tool surfaces that error verbatim so the
//     operator (or agent) knows to call `clawtool checkpoint save`
//     (autocommit) or land a real Conventional commit before
//     continuing.
//
// Why it's defense-in-depth and not the primary guard:
//
// An agent can bypass Read-before-Write with the loud
// `unsafe_overwrite_without_read=true` flag (used legitimately for
// scaffolded files, force-rewrites, etc.). Once that escape hatch
// fires, RbW is silent for the rest of that file's lifetime in the
// session. Guard layers a second invariant on top — even if every
// Edit slipped through the bypass, you can't have run more than N
// uncheckpointed mutations in a row. The operator still gets a
// visible \"please checkpoint\" stop the moment things drift far
// from a known-good state.
//
// Wiring (intentionally minimal — see autodev task notes):
//
//   - The Edit / Write tool calls Guard().OnEdit(path) before
//     mutating, then Guard().Check() to decide whether the edit
//     proceeds.
//   - The autocommit primitive (Autocommit in autocommit.go) and
//     the Commit primitive call Guard().OnCheckpoint() on success
//     to reset the counter.
//   - Tests call Reset() in a t.Cleanup so the package-global
//     instance doesn't leak counter state between cases.
//
// Concurrency: Guard is safe for concurrent callers — the daemon's
// MCP surface fans Edit/Write calls across goroutines. A single
// sync.Mutex protects the counter, the Enabled flag, and the
// max-edits threshold. Hot-path overhead is one mutex acquire +
// one int compare; an absent / disabled config short-circuits to
// nil immediately.
//
// Scope: this package owns the in-process counter and the Check()
// predicate. It does NOT own the wiring into Edit/Write — that
// lands in internal/tools/core. The hook is exported (OnEdit,
// OnCheckpoint, Check) so a future BIAM / autopilot caller can
// reuse the same instance without re-deriving the counter from
// git history.
package checkpoint

import (
	"errors"
	"fmt"
	"sync"
)

// DefaultMaxEditsWithoutCheckpoint is the package-level default
// applied when GuardConfig.MaxEditsWithoutCheckpoint is zero or
// negative. Five edits is the operator's chosen cadence — small
// enough that a runaway agent can't churn an entire package
// before being caught, large enough that interactive editing
// doesn't trip the gate after every tweak.
const DefaultMaxEditsWithoutCheckpoint = 5

// ErrCheckpointRequired is the sentinel returned by Check when the
// edit counter has reached the configured threshold. The Edit /
// Write tool surfaces the message verbatim so the operator's
// chat / terminal sees the actionable hint.
var ErrCheckpointRequired = errors.New(
	"checkpoint guard: too many uncheckpointed edits — call `clawtool checkpoint save` " +
		"(or land a real Conventional commit) before continuing. This is defense-in-depth " +
		"atop Read-before-Write; pass --no-guard / disable [checkpoint.guard] enabled to " +
		"silence the gate.",
)

// guardState is the mutable inside of the package-global Guard.
// Held behind a mutex so Edit / Write goroutines + the autocommit
// path can call OnEdit / OnCheckpoint without racing the counter.
type guardState struct {
	mu       sync.Mutex
	enabled  bool
	maxEdits int
	// counter tracks edits since the last checkpoint. Starts at 0.
	counter int
}

// pkgGuard is the process-wide singleton. Edit/Write call into it
// via Guard(); tests reach in via Reset() / SetConfig() to seed
// determinstic state.
var pkgGuard = &guardState{
	enabled:  false,
	maxEdits: DefaultMaxEditsWithoutCheckpoint,
}

// Guard returns the package-wide GuardHandle. Returning a small
// indirection (rather than the *guardState directly) keeps the
// API surface narrow — callers see OnEdit / OnCheckpoint / Check
// only, and we can swap the backing store later (e.g. SQLite per
// repo) without changing call sites.
func Guard() GuardHandle { return GuardHandle{s: pkgGuard} }

// GuardHandle is the public middleware surface.
type GuardHandle struct {
	s *guardState
}

// SetConfig wires a GuardConfig-shaped value into the singleton.
// Idempotent. The caller is responsible for marshalling
// config.GuardConfig into the boolean + int pair — this package
// deliberately doesn't import internal/config to keep the
// dependency graph straight (config already imports atomicfile +
// xdg + toml; checkpoint shouldn't pile on).
//
// maxEdits ≤ 0 falls back to DefaultMaxEditsWithoutCheckpoint so
// a literal 0 in TOML round-trips as "use default" rather than
// "disable" — the Enabled flag is the disable switch.
func (g GuardHandle) SetConfig(enabled bool, maxEdits int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.enabled = enabled
	if maxEdits <= 0 {
		g.s.maxEdits = DefaultMaxEditsWithoutCheckpoint
	} else {
		g.s.maxEdits = maxEdits
	}
	// Reset the counter on (re)config so an operator flipping the
	// toggle on doesn't immediately trip the gate from a long-
	// running session's accumulated edits. Symmetric: flipping
	// off-then-on is a clean slate.
	g.s.counter = 0
}

// Enabled reports the current toggle state. Cheap; one mutex.
// Exposed so the Edit / Write hook can short-circuit the
// OnEdit + Check pair when Guard is off.
func (g GuardHandle) Enabled() bool {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.enabled
}

// OnEdit is the pre-mutation hook. Increments the edit counter
// when Guard is enabled; no-op otherwise. The path argument is
// accepted for future use (per-path debouncing, allowlist) but
// not consulted today — the v1 contract is "any mutation counts".
//
// Returns nil unconditionally; Check() is the gate.
func (g GuardHandle) OnEdit(path string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if !g.s.enabled {
		return nil
	}
	g.s.counter++
	_ = path // reserved
	return nil
}

// OnCheckpoint resets the edit counter. Called by Autocommit on
// success and by Run (commit.go) on a real Conventional commit.
// Safe to call when Guard is disabled — the counter just stays at
// zero.
func (g GuardHandle) OnCheckpoint() {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.counter = 0
}

// Check is the gate. Returns ErrCheckpointRequired when Guard is
// enabled and the counter has reached the configured maximum.
// Otherwise returns nil.
//
// The check fires AFTER the OnEdit increment so the Nth edit is
// the one that trips the gate (operator gets a hard stop the
// moment they exceed the budget, not one edit late). Edit/Write
// call OnEdit + Check in sequence — the post-OnEdit Check is the
// canonical pattern.
func (g GuardHandle) Check() error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if !g.s.enabled {
		return nil
	}
	if g.s.counter >= g.s.maxEdits {
		return fmt.Errorf("%w (counter=%d, max=%d)", ErrCheckpointRequired, g.s.counter, g.s.maxEdits)
	}
	return nil
}

// Counter returns the current edit count. Used by tests + the
// future `clawtool checkpoint status` verb. Not load-bearing for
// the gate itself.
func (g GuardHandle) Counter() int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.counter
}

// Reset is the test hook. Re-zeros the counter and restores the
// disabled-default state so a t.Cleanup keeps state from leaking
// between tests. Production code never calls this — flipping the
// toggle off-then-on via SetConfig is the supported path.
func (g GuardHandle) Reset() {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.enabled = false
	g.s.maxEdits = DefaultMaxEditsWithoutCheckpoint
	g.s.counter = 0
}
