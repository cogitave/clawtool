// Package cli — `clawtool dashboard` (alias `clawtool tui`).
// Bubble Tea-based multi-pane runtime view over BIAM dispatches +
// agent registry + dispatch stats. Implementation lives in
// internal/tui; this file is the CLI surface that opens it.
//
// Closing v0.19's deferred TUI sketch — the operator wanted
// "everything visible while agents work in the background" without
// pasting `clawtool task list` over and over.
package cli

import (
	"fmt"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/tui"
)

const dashboardUsage = `Usage:
  clawtool dashboard            Launch the runtime TUI (Bubble Tea).
  clawtool tui                  Alias of the above.

Three panes:
  1. Dispatches  — BIAM tasks (active first, then recent)
  2. Agents      — supervisor's agent registry
  3. Stats       — totals / done / failed / active

Keybindings inside the TUI:
  q / esc        quit
  r              force refresh
  tab            cycle focused pane
  ↑ / ↓ / k / j  navigate within the focused pane
`

func (a *App) runDashboard(argv []string) int {
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		fmt.Fprint(a.Stdout, dashboardUsage)
		return 0
	}
	store, err := openBiamStore()
	if err != nil {
		// Don't fail the dashboard launch when no BIAM store
		// exists yet — the TUI renders an empty Pane 1 cleanly.
		// We surface the error to stderr so the operator sees
		// it once at boot.
		fmt.Fprintf(a.Stderr, "clawtool dashboard: BIAM store unavailable: %v\n", err)
	}
	if store != nil {
		defer store.Close()
	}
	sup := agents.NewSupervisor()
	if err := tui.Run(store, sup); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool dashboard: %v\n", err)
		return 1
	}
	return 0
}
