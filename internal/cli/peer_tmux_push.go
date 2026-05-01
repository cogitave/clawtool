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
func init() {
	agents.SetCloseTmuxPaneFn(KillTmuxPane)
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
