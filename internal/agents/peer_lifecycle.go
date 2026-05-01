// Package agents — peer-lifecycle auto-close.
//
// When SendMessage auto-spawns a tmux pane to deliver a prompt
// (peer-prefer / auto-tmux flow in tryPeerRoute) and the resulting
// BIAM task lands in a terminal status, this file's hook closes
// that pane. Without auto-close, every operator dispatch that
// happens to spawn a fresh peer leaves a dead tmux pane behind —
// the user's "şişer" complaint: tmux window list grows unbounded.
//
// Decision logic (3 lines):
//   1. The peer-route enqueue records taskID → peerID in linkTaskToPeer
//      ONLY when the resolved peer's metadata says auto_spawned=true.
//   2. The BIAM store fires a close-hook on terminal task status; the
//      hook calls MaybeAutoClosePane(taskID).
//   3. MaybeAutoClosePane resolves taskID → peerID → peer.TmuxPane
//      and calls closeTmuxPaneFn(paneID). User-attached panes are
//      never registered in the link table, so they're never closed.
//
// Test seam: closeTmuxPaneFn defaults to a no-op so unit tests can
// rebind it to a recorder. The daemon wires it to internal/cli's
// KillTmuxPane (which honors CLAWTOOL_TMUX_SOCKET) at boot via
// SetCloseTmuxPaneFn. Disabling the feature is a one-line
// SetAutoClosePanes(false) toggle the daemon flips when
// cfg.Peer.AutoClosePanes is explicitly false.
//
// Window cleanup (ADR-034 Q1) and grace period (Q2) layer on top of
// this contract:
//
//   - Spawner records the new pane's window_id in the peer's metadata
//     (MetaTmuxWindow) at registration time. MaybeAutoClosePane reads
//     it back and calls closeTmuxPaneAndMaybeWindowFn so the pane and
//     its (now-empty) window get reaped together.
//   - SetAutoCloseGraceSeconds(n) defers the kill by n seconds via
//     time.AfterFunc, storing the timer keyed by peerID so a
//     re-trigger on the same peer (LinkTaskToPeer fires for a fresh
//     dispatch into the same auto-spawned pane before the grace
//     window elapses) cancels the prior timer and we never kill the
//     pane out from under a back-to-back task.

package agents

import (
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
)

// MetaAutoSpawned is the peer-metadata key that marks a peer as one
// auto-spawned by SendMessage's tmux fallback. Only auto-spawned
// peers are eligible for auto-close — a user-attached pane (an
// operator's manually-opened claude session) carries no such flag
// and is left alone. Defined as a constant so the spawner + the
// lifecycle hook agree on the spelling without a typo regression.
const MetaAutoSpawned = "auto_spawned"

// MetaTmuxWindow is the peer-metadata key holding the tmux window_id
// (`@<digits>`) of the auto-spawned pane. Set by the spawner alongside
// MetaAutoSpawned; read by MaybeAutoClosePane so the close hook can
// optionally reap the empty window after killing the pane (ADR-034
// open-question Q1: window cleanup). Empty when the spawner ran
// against an older tmux that didn't echo `#{window_id}` in the -F
// format string — the close hook then falls back to legacy pane-only
// close.
const MetaTmuxWindow = "tmux_window"

// closeTmuxPaneFn is the test seam used by MaybeAutoClosePane.
// Defaults to a no-op so an agents-package unit test that doesn't
// care about pane closure (or runs without tmux on PATH) sees no
// side effects. The daemon's wireup binds this to
// internal/cli.KillTmuxPane via SetCloseTmuxPaneFn so production
// hits the real `tmux kill-pane -t %N` invocation.
//
// closeTmuxPaneAndMaybeWindowFn is the window-aware sibling: takes
// (paneID, windowID) and is responsible for killing the pane AND
// (when the window becomes empty) the window. Production binds it
// to internal/cli.KillTmuxPaneAndMaybeWindow. Defaulting to a no-op
// keeps unit tests that only stub the pane-only seam compatible.
var (
	closeTmuxPaneMu               sync.RWMutex
	closeTmuxPaneFn               = func(paneID string) error { return nil }
	closeTmuxPaneAndMaybeWindowFn = func(paneID, windowID string) error { return nil }
)

// SetCloseTmuxPaneFn registers the production close-pane adapter.
// Pass nil to clear (e.g. test cleanup); callers pass the real
// internal/cli.KillTmuxPane wrapper. Idempotent.
func SetCloseTmuxPaneFn(fn func(paneID string) error) {
	closeTmuxPaneMu.Lock()
	defer closeTmuxPaneMu.Unlock()
	if fn == nil {
		closeTmuxPaneFn = func(paneID string) error { return nil }
		return
	}
	closeTmuxPaneFn = fn
}

// SetCloseTmuxPaneAndMaybeWindowFn registers the production
// pane+window close adapter. Pass nil to clear. Tests that want to
// observe window-cleanup behaviour rebind this; tests that don't
// care leave it on the default no-op so the existing pane-only
// stub (recordCloseFn) continues to drive MaybeAutoClosePane.
func SetCloseTmuxPaneAndMaybeWindowFn(fn func(paneID, windowID string) error) {
	closeTmuxPaneMu.Lock()
	defer closeTmuxPaneMu.Unlock()
	if fn == nil {
		closeTmuxPaneAndMaybeWindowFn = func(paneID, windowID string) error { return nil }
		return
	}
	closeTmuxPaneAndMaybeWindowFn = fn
}

// getCloseTmuxPaneFn returns the current close-pane adapter under
// the read lock so the hook can call it without holding the lock
// while tmux runs.
func getCloseTmuxPaneFn() func(paneID string) error {
	closeTmuxPaneMu.RLock()
	defer closeTmuxPaneMu.RUnlock()
	return closeTmuxPaneFn
}

// getCloseTmuxPaneAndMaybeWindowFn returns the window-aware adapter
// under the read lock.
func getCloseTmuxPaneAndMaybeWindowFn() func(paneID, windowID string) error {
	closeTmuxPaneMu.RLock()
	defer closeTmuxPaneMu.RUnlock()
	return closeTmuxPaneAndMaybeWindowFn
}

// autoCloseEnabled is the master gate. Defaults to true; daemon
// flips to false when cfg.Peer.AutoClosePanes is explicitly set
// to false (power users who want the panes to stick around for
// post-mortem inspection). Lives behind a mutex so a config
// reload can flip it without racing the lifecycle hook.
var (
	autoCloseEnabledMu sync.RWMutex
	autoCloseEnabled   = true
)

// SetAutoClosePanes flips the master gate. Daemon calls this with
// the resolved cfg.Peer.AutoClosePanes value at boot.
func SetAutoClosePanes(enabled bool) {
	autoCloseEnabledMu.Lock()
	defer autoCloseEnabledMu.Unlock()
	autoCloseEnabled = enabled
}

func autoCloseEnabledRead() bool {
	autoCloseEnabledMu.RLock()
	defer autoCloseEnabledMu.RUnlock()
	return autoCloseEnabled
}

// autoCloseGraceSeconds defers the kill-pane after a terminal task
// status by the configured number of seconds. Default 0 = immediate
// (legacy behaviour). When > 0, MaybeAutoClosePane schedules the
// kill via time.AfterFunc and stores the timer keyed by peerID so
// a back-to-back dispatch into the same pane (LinkTaskToPeer fires
// before the grace window elapses) cancels the prior timer instead
// of killing the pane mid-second-task. Daemon flips this from
// cfg.Peer.AutoCloseGraceSeconds at boot via SetAutoCloseGraceSeconds.
var (
	autoCloseGraceMu      sync.RWMutex
	autoCloseGraceSeconds = 0
)

// SetAutoCloseGraceSeconds sets the grace-period in seconds. Pass 0
// (the default) to disable the deferral and fall back to immediate
// close. Negative values are clamped to 0.
func SetAutoCloseGraceSeconds(seconds int) {
	if seconds < 0 {
		seconds = 0
	}
	autoCloseGraceMu.Lock()
	defer autoCloseGraceMu.Unlock()
	autoCloseGraceSeconds = seconds
}

// autoCloseGraceRead returns the current grace-period configuration
// under the read lock. The hook reads once per fire — a config
// reload mid-grace-window keeps the original deadline (no in-flight
// timer mutation).
func autoCloseGraceRead() int {
	autoCloseGraceMu.RLock()
	defer autoCloseGraceMu.RUnlock()
	return autoCloseGraceSeconds
}

// taskPeerLink stores the taskID → peerID mapping that the auto-
// close hook consumes. Populated by tryPeerRoute when a
// SendMessage routes into an auto-spawned peer; consumed (and
// removed) by MaybeAutoClosePane. Lives at package scope so the
// BIAM-side terminal-status hook can dial it without threading a
// supervisor handle through the runner.
//
// graceTimers keeps the *time.Timer per peerID for a pending grace
// window. Indexed by peerID (not taskID) because the cancellation
// invariant is "a fresh dispatch INTO THE SAME PEER cancels its
// pending kill" — a back-to-back SendMessage that hits a different
// auto-spawned peer mustn't disturb the first peer's countdown.
// Cleared after the timer fires (or is cancelled by a re-trigger).
var (
	taskPeerLinkMu sync.Mutex
	taskPeerLink   = map[string]string{}
	graceTimers    = map[string]*time.Timer{}
)

// LinkTaskToPeer records that the dispatch behind taskID was routed
// into peerID's inbox via auto-spawn. Idempotent: a second call with
// the same taskID overwrites the prior peerID — fine, the lifecycle
// hook only cares about the most recent route. Empty taskID or
// empty peerID is a no-op so callers don't have to nil-guard.
//
// Side effect: cancels any pending grace-period timer for this peer.
// The "rapid back-to-back tasks shouldn't kill the pane out from
// under the second one" rule from ADR-034 Q2 — a second SendMessage
// to the same auto-spawned codex pane while the first task's grace
// window is still ticking must keep the pane alive.
func LinkTaskToPeer(taskID, peerID string) {
	if taskID == "" || peerID == "" {
		return
	}
	taskPeerLinkMu.Lock()
	defer taskPeerLinkMu.Unlock()
	taskPeerLink[taskID] = peerID
	if t, ok := graceTimers[peerID]; ok {
		t.Stop()
		delete(graceTimers, peerID)
	}
}

// unlinkTask removes taskID from the link table and returns the
// previously linked peerID (or empty if none). Used by
// MaybeAutoClosePane to consume-and-clear in one shot so the same
// terminal-status fire doesn't double-close on a redundant hook
// invocation.
func unlinkTask(taskID string) string {
	taskPeerLinkMu.Lock()
	defer taskPeerLinkMu.Unlock()
	pid := taskPeerLink[taskID]
	delete(taskPeerLink, taskID)
	return pid
}

// resetPeerLifecycleStateForTest clears the link table + restores
// the close-pane stub. Test-only helper (parallel test runs share
// package state via the testing.T cleanup hook).
func resetPeerLifecycleStateForTest() {
	taskPeerLinkMu.Lock()
	for _, t := range graceTimers {
		t.Stop()
	}
	graceTimers = map[string]*time.Timer{}
	taskPeerLink = map[string]string{}
	taskPeerLinkMu.Unlock()
	SetCloseTmuxPaneFn(nil)
	SetCloseTmuxPaneAndMaybeWindowFn(nil)
	SetAutoClosePanes(true)
	SetAutoCloseGraceSeconds(0)
}

// MaybeAutoClosePane is the entry point the BIAM terminal-status
// hook calls. Resolves taskID → peerID → peer.TmuxPane and fires
// closeTmuxPaneFn(paneID). Skips silently when:
//   - autoCloseEnabled is false (operator opted out via config).
//   - taskID has no link in the table (peer was user-attached, not
//     auto-spawned — we never registered the link in the first
//     place; this is the user-attached safety check).
//   - registry has no peer with that ID (raced a deregister).
//   - peer's metadata doesn't carry auto_spawned=true (defence in
//     depth: even if the link table has the row, we re-check the
//     flag before firing tmux).
//   - peer.TmuxPane is empty (peer registered without a pane —
//     not an auto-spawn, can't close anything).
//
// Returns the close-pane error (or nil). Caller (the BIAM hook)
// logs but doesn't propagate — auto-close is best-effort.
//
// Grace period (ADR-034 Q2): when autoCloseGraceSeconds > 0 the
// kill is deferred via time.AfterFunc. The timer is keyed by peerID
// in graceTimers so a re-trigger on the same peer (LinkTaskToPeer
// from a back-to-back dispatch) cancels it. Returns nil immediately
// in the deferred path — the caller can't observe the deferred
// kill's error, which matches the existing best-effort contract.
//
// Window cleanup (Q1): when the peer's metadata carries a
// MetaTmuxWindow value, the kill routes through the window-aware
// seam (closeTmuxPaneAndMaybeWindowFn) which probes the window
// after killing the pane and reaps it when empty. Empty window_id
// (peer registered before the spawner started recording it) falls
// back to the legacy pane-only seam.
func MaybeAutoClosePane(taskID string, reg *a2a.Registry) error {
	if !autoCloseEnabledRead() {
		return nil
	}
	peerID := unlinkTask(taskID)
	if peerID == "" {
		return nil
	}
	if reg == nil {
		return nil
	}
	peer := reg.Get(peerID)
	if peer == nil {
		return nil
	}
	if peer.Metadata[MetaAutoSpawned] != "true" {
		return nil
	}
	if peer.TmuxPane == "" {
		return nil
	}
	paneID := peer.TmuxPane
	windowID := peer.Metadata[MetaTmuxWindow]

	// closeNow is the actual kill invocation. Picks the window-
	// aware seam when a window_id is present (modern auto-spawn
	// path), falls back to the legacy pane-only seam otherwise so
	// existing tests that stub recordCloseFn keep working
	// unchanged.
	closeNow := func() error {
		if windowID != "" {
			return getCloseTmuxPaneAndMaybeWindowFn()(paneID, windowID)
		}
		return getCloseTmuxPaneFn()(paneID)
	}

	grace := autoCloseGraceRead()
	if grace <= 0 {
		return closeNow()
	}

	// Deferred close — schedule via AfterFunc and store the timer
	// so a back-to-back LinkTaskToPeer(...) on the same peer can
	// cancel it. Re-entrancy on closeTimer fire is safe: the
	// goroutine takes the same lock LinkTaskToPeer does so the
	// "second-task arrives mid-fire" race resolves to one
	// outcome (either the timer fires and kills, or the
	// re-trigger cancels first — never both).
	taskPeerLinkMu.Lock()
	if existing, ok := graceTimers[peerID]; ok {
		existing.Stop()
	}
	timer := time.AfterFunc(time.Duration(grace)*time.Second, func() {
		taskPeerLinkMu.Lock()
		delete(graceTimers, peerID)
		taskPeerLinkMu.Unlock()
		_ = closeNow()
	})
	graceTimers[peerID] = timer
	taskPeerLinkMu.Unlock()
	return nil
}
