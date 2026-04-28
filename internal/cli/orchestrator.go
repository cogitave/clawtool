// Package cli — `clawtool orchestrator` (alias `clawtool orch`).
// Phase 2 of ADR-028: split-pane Bubble Tea TUI that auto-spawns
// one stdout-tail pane per active BIAM dispatch. Subscribes to the
// daemon's task-watch Unix socket so transitions reach the screen
// in real time.
package cli

import (
	"fmt"
	"strings"

	"github.com/cogitave/clawtool/internal/tui"
)

const orchestratorUsage = `Usage:
  clawtool orchestrator [flags]   (alias: clawtool orch)

Live multi-pane view of every active BIAM dispatch. One pane per
task, auto-spawning when new dispatches arrive and fading 5 s after
they hit a terminal state. Backed by the daemon's task-watch Unix
socket — no SQLite poll.

Keys:
  q / esc / ctrl-c   quit
  r                  reconnect to the watch socket (after a daemon
                     restart, etc.)

For a single-task tail use 'clawtool task watch <id>'; for the broad
runtime panel use 'clawtool dashboard'.
`

func (a *App) runOrchestrator(argv []string) int {
	for _, arg := range argv {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(a.Stdout, orchestratorUsage)
			return 0
		default:
			if strings.HasPrefix(arg, "--") {
				fmt.Fprintf(a.Stderr, "clawtool orchestrator: unknown flag %q\n%s",
					arg, orchestratorUsage)
				return 2
			}
		}
	}
	if err := tui.RunOrchestrator(); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool orchestrator: %v\n", err)
		return 1
	}
	return 0
}
