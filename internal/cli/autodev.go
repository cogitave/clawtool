// Package cli — `clawtool autodev` self-trigger loop.
//
// `autodev` is a flag-driven Stop-hook continuation: when armed, the
// `clawtool autodev hook` subcommand emits `{"decision":"block",
// "reason":"..."}` on Claude Code's Stop event so the conversation
// keeps going with the supplied prompt instead of ending the turn.
// `/clawtool-autodev-stop` (or `clawtool autodev stop`) is the only
// path back to operator control.
//
// The hook itself is wired through the marketplace plugin's
// `hooks/hooks.json` — every install of clawtool@clawtool-marketplace
// already has the Stop event bound. There is no separate install
// step: the operator runs `clawtool autodev start` to flip the
// arm-flag, and the next turn-end in any clawtool-bound Claude Code
// session triggers the loop continuation.
//
// Subcommands:
//
//	clawtool autodev start    Arm the loop (creates flag, resets
//	                          counter).
//	clawtool autodev stop     Disarm (deletes flag + counter).
//	clawtool autodev status   armed/disarmed + counter value.
//	clawtool autodev hook     Hook entry-point. Reads the flag,
//	                          emits Stop-block JSON on stdout when
//	                          armed, exits 0 silently otherwise.
//	                          Wired by hooks/hooks.json.
//
// Reference: https://docs.claude.com/en/docs/claude-code/hooks
//
//	Stop event — decision="block" prevents Claude from stopping,
//	continues the conversation with `reason` as a fresh prompt.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const autodevUsage = `Usage:
  clawtool autodev start    Arm the self-trigger loop. Creates the
                            arm-flag and resets the self-trigger
                            counter. The hook is already wired by
                            the marketplace plugin's hooks.json —
                            no install step needed.
  clawtool autodev stop     Disarm. Deletes the flag and counter
                            so the next Stop event lets Claude
                            finish normally.
  clawtool autodev status   Show armed/disarmed state + counter.
  clawtool autodev hook     Stop-hook entry point (called by Claude
                            Code; not for direct operator use).

When armed, every Claude Code Stop event in a clawtool-bound session
returns {"decision":"block","reason":"..."} so the conversation
continues with the supplied prompt instead of ending the turn. A
200-self-trigger cap is the runaway safety belt; reset on every start.
`

// AutodevSelfTriggerCap is the per-arming budget. Set high enough
// that a real productive loop doesn't trip it (~200 turns ≈ a full
// work session) but low enough that a buggy loop runs out of fuel
// before the operator's bill does.
const AutodevSelfTriggerCap = 200

func (a *App) runAutodev(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, autodevUsage)
		return 2
	}
	switch argv[0] {
	case "start":
		return a.runAutodevStart()
	case "stop":
		return a.runAutodevStop()
	case "status":
		return a.runAutodevStatus()
	case "hook":
		return a.runAutodevHook()
	case "--help", "-h", "help":
		fmt.Fprint(a.Stdout, autodevUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool autodev: unknown subcommand %q\n\n%s", argv[0], autodevUsage)
		return 2
	}
}

// autodevPaths centralises every filesystem location the
// subcommands touch. Tests can override the home dir by setting
// $HOME; everything resolves from there.
type autodevPaths struct {
	flag    string
	counter string
}

func newAutodevPaths() autodevPaths {
	home, _ := os.UserHomeDir()
	cfg := filepath.Join(home, ".config", "clawtool")
	return autodevPaths{
		flag:    filepath.Join(cfg, "autodev.enabled"),
		counter: filepath.Join(cfg, "autodev.counter"),
	}
}

func (a *App) runAutodevStart() int {
	p := newAutodevPaths()
	if err := os.MkdirAll(filepath.Dir(p.flag), 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "autodev start: %v\n", err)
		return 1
	}
	if err := os.WriteFile(p.flag, nil, 0o644); err != nil {
		fmt.Fprintf(a.Stderr, "autodev start: %v\n", err)
		return 1
	}
	_ = os.Remove(p.counter)
	fmt.Fprintf(a.Stdout, "✓ autodev armed (counter reset, cap %d)\n", AutodevSelfTriggerCap)
	return 0
}

func (a *App) runAutodevStop() int {
	p := newAutodevPaths()
	flagRemoved := os.Remove(p.flag) == nil
	counterRemoved := os.Remove(p.counter) == nil
	if !flagRemoved && !counterRemoved {
		fmt.Fprintln(a.Stdout, "autodev: already disarmed")
		return 0
	}
	fmt.Fprintln(a.Stdout, "✓ autodev disarmed")
	return 0
}

func (a *App) runAutodevStatus() int {
	p := newAutodevPaths()
	armed := fileExists(p.flag)
	count := readCounter(p.counter)
	state := "disarmed"
	if armed {
		state = "armed"
	}
	fmt.Fprintf(a.Stdout, "state:    %s\n", state)
	fmt.Fprintf(a.Stdout, "counter:  %d / %d\n", count, AutodevSelfTriggerCap)
	if !armed {
		return 1
	}
	return 0
}

// runAutodevHook is the Stop-event entry-point invoked by Claude
// Code via hooks/hooks.json when the user-facing turn ends.
//
// Exit-code contract:
//
//   - 0 + empty stdout — flag absent OR counter exhausted; Claude
//     stops normally.
//   - 0 + JSON {"decision":"block","reason":"..."} on stdout —
//     Claude refuses to stop and continues with `reason` as the
//     next user prompt. This is the loop-continuation path.
//
// Never exits non-zero: a hook crash would surface as stderr noise
// but block nothing useful. We prefer a quiet no-op over a noisy
// half-failure, so every error path falls through to "exit 0, stop
// normally."
func (a *App) runAutodevHook() int {
	p := newAutodevPaths()
	if !fileExists(p.flag) {
		return 0
	}
	count := readCounter(p.counter)
	if count >= AutodevSelfTriggerCap {
		emitStopBlock(a.Stdout, fmt.Sprintf(
			"AUTODEV LOOP cap reached (%d self-triggers). Run /clawtool-autodev-stop to acknowledge, then /clawtool-autodev-start to resume. Operator must explicitly re-arm to continue.",
			AutodevSelfTriggerCap,
		))
		return 0
	}
	if err := os.WriteFile(p.counter, []byte(fmt.Sprintf("%d\n", count+1)), 0o644); err != nil {
		// Counter write failed — disarm to avoid an uncounted runaway.
		_ = os.Remove(p.flag)
		return 0
	}
	emitStopBlock(a.Stdout, autodevHookPrompt(count+1))
	return 0
}

// emitStopBlock writes the Claude Code Stop-hook JSON envelope.
func emitStopBlock(w io.Writer, reason string) {
	body, _ := json.Marshal(map[string]any{
		"decision": "block",
		"reason":   reason,
	})
	w.Write(body)
	w.Write([]byte{'\n'})
}

// autodevHookPrompt composes the next-turn prompt. Repo path,
// latest tag, and queue state are probed at hook-fire time so a
// long-running loop doesn't drift on stale snapshots — the prompt
// the model reads is always the one matching `now`.
func autodevHookPrompt(triggerN int) string {
	repo := strings.TrimSpace(os.Getenv("CLAWTOOL_AUTODEV_REPO"))
	if repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			repo = cwd
		}
	}
	latestTag := "?"
	if repo != "" {
		cmd := exec.Command("git", "describe", "--tags", "--abbrev=0", "origin/main")
		cmd.Dir = repo
		if out, err := cmd.Output(); err == nil {
			latestTag = strings.TrimSpace(string(out))
		}
	}
	queue := "?"
	if path, err := exec.LookPath("clawtool"); err == nil {
		cmd := exec.Command(path, "autopilot", "status")
		if out, err := cmd.Output(); err == nil {
			queue = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
	}
	// Tag-watcher snapshot — operator's #1 visibility complaint:
	// "I can't see what the loop is waiting for." Inline the
	// freshest watcher status into every self-trigger prompt so the
	// model can surface it back without the operator running
	// `tail /tmp/clawtool-tag-watcher*.log` by hand.
	watchers := summarizeWatchersForHook()

	return fmt.Sprintf(`AUTODEV LOOP (self-trigger #%d / %d) — durmadan çalış.

Latest tag: %s
Queue:      %s

Tag watchers (latest 3):
%s

Loop steps:
1. `+"`clawtool watchers list`"+` — every active CI tag-watcher's status, target tag, and conclusion.
2. `+"`clawtool autopilot status`"+` — if pending=0, `+"`clawtool ideate --top 15`"+` and act on signal.
3. If signal: ideate --apply, dispatch up to 4 parallel agents.
4. If genuinely idle: pick the next architectural improvement (look at docs/ideator.md, recent ADRs, deps_outdated, deadcode, vuln advisories) — don't just say "no change".
5. /clawtool-autodev-stop (or `+"`clawtool autodev stop`"+`) is the ONLY path back to operator control.

If you've genuinely run out of ideas after a real ideate sweep, write a short architecture-review or doc improvement and ship that — never just "idle, no change". The framework dry-loop diagnostic shipped in v0.22.150 surfaces a synthetic Idea via the autopilot/MCP path; act on it.`,
		triggerN, AutodevSelfTriggerCap, latestTag, queue, watchers)
}

// summarizeWatchersForHook returns the most recent 3 watchers as
// one-liners for the autodev hook prompt. Read-only — never spawns
// or mutates anything; just parses /tmp logs.
func summarizeWatchersForHook() string {
	snaps := listWatchers()
	if len(snaps) == 0 {
		return "  (no watchers — no /tmp/clawtool-tag-watcher*.log files yet)"
	}
	if len(snaps) > 3 {
		snaps = snaps[len(snaps)-3:]
	}
	var b strings.Builder
	for _, s := range snaps {
		tag := s.Tag
		if tag == "" {
			tag = "(no tag)"
		}
		state := s.Status
		if s.Conclusion != "" && s.Conclusion != s.Status {
			state += "/" + s.Conclusion
		}
		fmt.Fprintf(&b, "  watcher%-3d %s — %s\n", s.ID, tag, state)
	}
	return strings.TrimRight(b.String(), "\n")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readCounter(path string) int {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(body)), "%d", &n)
	return n
}
