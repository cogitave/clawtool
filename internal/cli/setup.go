// Package cli — `clawtool setup` is the unified first-run entry. v1
// (Phase 1 of the onboarding state machine — see
// wiki/decisions/027-onboarding-state-machine.md) chains the
// existing onboard wizard (host detection, MCP claims, identity,
// telemetry consent, sandbox-worker note) and init wizard (recipe
// matrix per scope) so the operator only types one command.
//
// Phase 2 collapses both into a single huh form with a per-feature
// opt-in matrix. Today's chain is the bridge — same prompts, one
// less verb to remember.
package cli

import (
	"fmt"
	"strings"
)

const setupUsage = `Usage:
  clawtool setup [--yes]
                 Unified first-run wizard. Runs onboard (detect hosts /
                 install bridges / register clawtool MCP / generate BIAM
                 identity / record telemetry consent) then init (apply
                 project recipes — license / dependabot / release-please
                 / brain / etc). --yes propagates to init's
                 non-interactive defaults.

For finer control:
  clawtool onboard      Host-side wizard only.
  clawtool init [--yes] Recipe wizard only (current repo).
`

func (a *App) runSetup(argv []string) int {
	for _, arg := range argv {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(a.Stdout, setupUsage)
			return 0
		case "--yes", "-y":
			// Propagated to init's non-interactive defaults.
		default:
			if strings.HasPrefix(arg, "--") {
				fmt.Fprintf(a.Stderr, "clawtool setup: unknown flag %q\n%s", arg, setupUsage)
				return 2
			}
		}
	}

	fmt.Fprintln(a.Stdout, "── stage 1/2 — clawtool onboard ─────────────")
	if rc := a.runOnboard(nil); rc != 0 {
		fmt.Fprintln(a.Stderr, "clawtool setup: onboard returned non-zero; stopping before init.")
		return rc
	}

	fmt.Fprintln(a.Stdout, "")
	fmt.Fprintln(a.Stdout, "── stage 2/2 — clawtool init (this repo) ────")
	return a.runInit(argv)
}
