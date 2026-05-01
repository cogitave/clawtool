// Package cli — repowire-style real-time peer push via
// `tmux send-keys`. When a target peer's `tmux_pane` field is
// populated, `peer send` ALSO drives the pane directly so the
// recipient agent sees the message in its live transcript without
// waiting for a session-tick drain. The inbox write remains the
// canonical delivery — tmux push is best-effort and additive.
//
// The 3-step send-keys sequence mirrors repowire's
// websocket_hook._tmux_send_keys (literal text → Escape → Enter).
// Escape clears any half-typed buffer in the target pane;
// Enter submits the prompt.

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/cogitave/clawtool/internal/agents"
)

// init wires the package's tmux-pane-kill helper into the agents
// peer-lifecycle hook seam. The agents package can't import this
// package (would invert the daemon → CLI relationship — the daemon
// stays the owner of the dispatch path), so we register from the
// importing side. cmd/clawtool imports internal/cli, so this fires
// once on every binary launch + before the daemon installs the BIAM
// terminal-status hook. Tests that don't import internal/cli still
// see the agents-package default no-op closeTmuxPaneFn.
//
// Both seams (close-pane + close-pane-and-window) bind to this
// package's tmux helpers. The agents-side hook picks which one to
// invoke based on whether the auto-spawned peer's metadata carries
// a tmux window_id (it does, via MetaTmuxWindow set at spawn time).
func init() {
	agents.SetCloseTmuxPaneFn(KillTmuxPane)
	agents.SetCloseTmuxPaneAndMaybeWindowFn(KillTmuxPaneAndMaybeWindow)
}

// tmuxSocketArgs returns `-L <socket>` when CLAWTOOL_TMUX_SOCKET is
// set, else an empty slice. Passed in front of every tmux subcommand
// so a containerised / sandboxed tmux server (e.g. the e2e Docker
// harness which runs `tmux -L claw-test`) is reachable. Without this
// flag, tmux would dial its default `/tmp/tmux-<uid>/default` socket
// which the harness doesn't bind. The env var is the single source
// of truth — we don't surface it as a flag because it's an
// integration concern, not a per-call decision.
func tmuxSocketArgs() []string {
	sock := strings.TrimSpace(os.Getenv("CLAWTOOL_TMUX_SOCKET"))
	if sock == "" {
		return nil
	}
	return []string{"-L", sock}
}

// tmuxArgv composes the full tmux argv: `tmux` (or socket-prefixed)
// + `subcmd` + remaining args. Lets every callsite stay one-liner
// without re-reading the env var inline.
func tmuxArgv(subcmd string, args ...string) []string {
	out := append([]string{}, tmuxSocketArgs()...)
	out = append(out, subcmd)
	out = append(out, args...)
	return out
}

// tmuxRunArgv runs a tmux invocation for its side effect and
// discards stdout. Used by tmuxSendKeys (fire-and-forget). The
// indirection is a test seam — production binds to execRunArgv,
// tests overwrite with a non-execing recorder so the suite stays
// portable across Linux/macOS without depending on /bin/true vs
// /usr/bin/true (the original /bin/true stub broke macos-latest
// CI because `/bin/true` doesn't exist on Darwin).
var tmuxRunArgv = execRunArgv

// tmuxOutputArgv runs a tmux invocation and returns its stdout.
// Used by tmuxPaneAlive. Same test-seam contract as tmuxRunArgv.
var tmuxOutputArgv = execOutputArgv

// execRunArgv is the production no-stub adapter that fork+execs
// the named binary with the given argv and returns its Run error.
func execRunArgv(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// execOutputArgv is the production no-stub adapter that fork+execs
// the named binary with the given argv and returns its captured
// stdout (or an error).
func execOutputArgv(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// tmuxPaneIDPattern matches tmux's `%<digits>` pane id format.
// Anything else (path-like, shell-meta-laden) is rejected before
// we hand it to `tmux send-keys -t` so a malformed registry row
// can't smuggle arguments.
var tmuxPaneIDPattern = regexp.MustCompile(`^%[0-9]+$`)

// validTmuxPaneID returns true when paneID is the literal
// `%<digits>` shape. We refuse to invoke tmux otherwise.
func validTmuxPaneID(paneID string) bool {
	return tmuxPaneIDPattern.MatchString(paneID)
}

// tmuxSendKeys drives the 3-step send-keys sequence at the
// target pane. The `-l` (literal) flag prevents tmux from
// interpreting key names inside the message body, so a payload
// like "C-c" doesn't accidentally SIGINT the recipient.
//
// Returns the first error encountered (or nil). Best-effort:
// callers treat any error as silent fallback to inbox-only
// delivery — the canonical write already happened upstream.
func tmuxSendKeys(paneID, text string) error {
	if !validTmuxPaneID(paneID) {
		return fmt.Errorf("invalid tmux pane id %q (want %%<digits>)", paneID)
	}
	// Step 1 — push the literal text into the pane's input
	// buffer. `-l` suppresses key-name interpretation.
	if err := tmuxRunArgv("tmux", tmuxArgv("send-keys", "-t", paneID, "-l", text)...); err != nil {
		return fmt.Errorf("tmux send-keys text: %w", err)
	}
	// Step 2 — Escape to clear any partial keystroke state in
	// the recipient's TUI before submission. Mirrors repowire.
	if err := tmuxRunArgv("tmux", tmuxArgv("send-keys", "-t", paneID, "Escape")...); err != nil {
		return fmt.Errorf("tmux send-keys Escape: %w", err)
	}
	// Step 3 — Enter to submit the prompt to the recipient agent.
	if err := tmuxRunArgv("tmux", tmuxArgv("send-keys", "-t", paneID, "Enter")...); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w", err)
	}
	return nil
}

// tmuxKillPane closes the named pane (`tmux kill-pane -t %N`). Used
// by the auto-close lifecycle hook when an auto-spawned peer's
// dispatch lands in a terminal status — without this, every
// SendMessage that auto-spawns leaves a dead pane behind and the
// operator's tmux window list grows unbounded ("şişer"). Best-
// effort: returns the underlying exec error verbatim so the caller
// (peer-lifecycle hook) can log it, but the auto-close path treats
// any error as a silent skip — pane already gone is fine.
func tmuxKillPane(paneID string) error {
	if !validTmuxPaneID(paneID) {
		return fmt.Errorf("invalid tmux pane id %q (want %%<digits>)", paneID)
	}
	if err := tmuxRunArgv("tmux", tmuxArgv("kill-pane", "-t", paneID)...); err != nil {
		return fmt.Errorf("tmux kill-pane: %w", err)
	}
	return nil
}

// KillTmuxPane is the package's exported entry point for the
// peer-lifecycle auto-close path. The supervisor's BIAM terminal-
// status hook lives in internal/agents and can't import internal/cli
// (the dependency would invert the daemon → CLI relationship), so
// the daemon wires this function into the agents-side hook seam at
// boot. Honors CLAWTOOL_TMUX_SOCKET via the same tmuxArgv path so
// containerised tmux servers stay reachable.
func KillTmuxPane(paneID string) error {
	return tmuxKillPane(paneID)
}

// tmuxWindowIDPattern matches tmux's `@<digits>` window id format
// returned by `#{window_id}`. Same paranoia as the pane-id check —
// any drift from the literal shape is rejected before we hand the
// value to `tmux kill-window -t`.
var tmuxWindowIDPattern = regexp.MustCompile(`^@[0-9]+$`)

// validTmuxWindowID is the window_id sibling of validTmuxPaneID.
func validTmuxWindowID(windowID string) bool {
	return tmuxWindowIDPattern.MatchString(windowID)
}

// tmuxWindowEmpty reports whether windowID has no remaining panes.
// `tmux list-panes -t <window_id>` errors with "can't find window"
// when the window has already been collapsed by tmux itself (last
// pane closed → window auto-removed in some tmux versions); both
// "no output" and "errored" map to "empty" so the caller's
// kill-window is best-effort and idempotent.
//
// On modern tmux (3.x) closing the last pane normally collapses the
// window automatically — but the e2e harness has caught cases where
// `kill-pane -t <pane_id>` leaves an empty window behind (custom
// `remain-on-exit` settings; pane-detach via -X). The window-cleanup
// hook needs to handle both: pane gone + window auto-collapsed
// (true → kill-window is a no-op typed error we swallow), and pane
// gone + window still present (true → kill-window actually closes).
func tmuxWindowEmpty(windowID string) bool {
	if !validTmuxWindowID(windowID) {
		return false
	}
	out, err := tmuxOutputArgv("tmux", tmuxArgv("list-panes", "-t", windowID, "-F", "#{pane_id}")...)
	if err != nil {
		// list-panes refuses an unknown window — treat as empty
		// so the caller skips the kill-window (already gone).
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

// tmuxKillWindow closes a tmux window. Refuses anything that doesn't
// match the `@<digits>` shape so a malformed registry row can't
// smuggle arguments. Best-effort like its kill-pane sibling — the
// caller treats any error as a silent skip.
func tmuxKillWindow(windowID string) error {
	if !validTmuxWindowID(windowID) {
		return fmt.Errorf("invalid tmux window id %q (want @<digits>)", windowID)
	}
	if err := tmuxRunArgv("tmux", tmuxArgv("kill-window", "-t", windowID)...); err != nil {
		return fmt.Errorf("tmux kill-window: %w", err)
	}
	return nil
}

// KillTmuxPaneAndMaybeWindow closes the named pane, then probes the
// window — if no panes remain (or the window was already auto-
// collapsed by tmux) it ALSO closes the window so an auto-spawned
// pane doesn't leave an empty tmux window behind. Empty windowID
// (peer registered before the spawner started recording window_id)
// short-circuits to legacy pane-only close so the existing-peer
// path is unaffected.
//
// Returns the kill-pane error (if any). The kill-window step is
// best-effort: a stale window id, a tmux server that already
// reaped the window, or a permission glitch all surface as
// non-fatal — we still return success on the kill-pane step,
// which is the caller's load-bearing concern.
func KillTmuxPaneAndMaybeWindow(paneID, windowID string) error {
	if err := tmuxKillPane(paneID); err != nil {
		return err
	}
	if windowID == "" {
		return nil
	}
	if !tmuxWindowEmpty(windowID) {
		return nil
	}
	// Empty window detected — close it. Errors are swallowed by
	// the caller (peer-lifecycle hook) which logs but doesn't
	// propagate; an already-collapsed window is a no-op typed
	// error.
	_ = tmuxKillWindow(windowID)
	return nil
}

// tmuxPaneAlive checks whether the named pane is still listed by
// the running tmux server. Used as a liveness gate before send-
// keys: if the recipient session crashed, the pane id is stale
// and pushing into it would either silently no-op or (worse) get
// reused by a different agent on a new session.
//
// Returns false on ANY error (tmux not on PATH, server down,
// pane absent, parse failure) so callers fall through to inbox-
// only delivery without surfacing noise.
func tmuxPaneAlive(paneID string) bool {
	if !validTmuxPaneID(paneID) {
		return false
	}
	out, err := tmuxOutputArgv("tmux", tmuxArgv("list-panes", "-a", "-F", "#{pane_id}")...)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == paneID {
			return true
		}
	}
	return false
}
