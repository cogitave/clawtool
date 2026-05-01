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

package agents

import (
	"sync"

	"github.com/cogitave/clawtool/internal/a2a"
)

// MetaAutoSpawned is the peer-metadata key that marks a peer as one
// auto-spawned by SendMessage's tmux fallback. Only auto-spawned
// peers are eligible for auto-close — a user-attached pane (an
// operator's manually-opened claude session) carries no such flag
// and is left alone. Defined as a constant so the spawner + the
// lifecycle hook agree on the spelling without a typo regression.
const MetaAutoSpawned = "auto_spawned"

// closeTmuxPaneFn is the test seam used by MaybeAutoClosePane.
// Defaults to a no-op so an agents-package unit test that doesn't
// care about pane closure (or runs without tmux on PATH) sees no
// side effects. The daemon's wireup binds this to
// internal/cli.KillTmuxPane via SetCloseTmuxPaneFn so production
// hits the real `tmux kill-pane -t %N` invocation.
var (
	closeTmuxPaneMu sync.RWMutex
	closeTmuxPaneFn = func(paneID string) error { return nil }
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

// getCloseTmuxPaneFn returns the current close-pane adapter under
// the read lock so the hook can call it without holding the lock
// while tmux runs.
func getCloseTmuxPaneFn() func(paneID string) error {
	closeTmuxPaneMu.RLock()
	defer closeTmuxPaneMu.RUnlock()
	return closeTmuxPaneFn
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

// taskPeerLink stores the taskID → peerID mapping that the auto-
// close hook consumes. Populated by tryPeerRoute when a
// SendMessage routes into an auto-spawned peer; consumed (and
// removed) by MaybeAutoClosePane. Lives at package scope so the
// BIAM-side terminal-status hook can dial it without threading a
// supervisor handle through the runner.
var (
	taskPeerLinkMu sync.Mutex
	taskPeerLink   = map[string]string{}
)

// LinkTaskToPeer records that the dispatch behind taskID was routed
// into peerID's inbox via auto-spawn. Idempotent: a second call with
// the same taskID overwrites the prior peerID — fine, the lifecycle
// hook only cares about the most recent route. Empty taskID or
// empty peerID is a no-op so callers don't have to nil-guard.
func LinkTaskToPeer(taskID, peerID string) {
	if taskID == "" || peerID == "" {
		return
	}
	taskPeerLinkMu.Lock()
	defer taskPeerLinkMu.Unlock()
	taskPeerLink[taskID] = peerID
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
	taskPeerLink = map[string]string{}
	taskPeerLinkMu.Unlock()
	SetCloseTmuxPaneFn(nil)
	SetAutoClosePanes(true)
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
	return getCloseTmuxPaneFn()(peer.TmuxPane)
}
