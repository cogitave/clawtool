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
	"os/exec"
	"regexp"
	"strings"
)

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
	if err := tmuxRunArgv("tmux", "send-keys", "-t", paneID, "-l", text); err != nil {
		return fmt.Errorf("tmux send-keys text: %w", err)
	}
	// Step 2 — Escape to clear any partial keystroke state in
	// the recipient's TUI before submission. Mirrors repowire.
	if err := tmuxRunArgv("tmux", "send-keys", "-t", paneID, "Escape"); err != nil {
		return fmt.Errorf("tmux send-keys Escape: %w", err)
	}
	// Step 3 — Enter to submit the prompt to the recipient agent.
	if err := tmuxRunArgv("tmux", "send-keys", "-t", paneID, "Enter"); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w", err)
	}
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
	out, err := tmuxOutputArgv("tmux", "list-panes", "-a", "-F", "#{pane_id}")
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
