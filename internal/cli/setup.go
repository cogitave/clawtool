// Package cli — `clawtool setup` is the unified first-run entry.
// Phase 2 of ADR-027: one huh form with a per-feature opt-in matrix
// instead of the onboard → init verb chain. --legacy falls back to
// the Phase 1 sequential dispatch for operators who hit a bug or
// prefer the old prompts.
package cli

import (
	"fmt"
	"os"
	"strings"
)

const setupUsage = `Usage:
  clawtool setup [--yes] [--legacy]
                 Unified first-run wizard. Probes the host + repo,
                 shows a single per-feature opt-in matrix (daemon /
                 identity / secrets / host claims / bridge installs /
                 stable repo recipes), applies the selection in
                 dependency order, runs 'clawtool overview' to verify.

  --legacy       Fall back to the Phase 1 sequential chain
                 (onboard → init). Use if the matrix screen has issues
                 or you prefer the per-stage prompts.

For finer control:
  clawtool onboard      Host-side wizard only (the original).
  clawtool init [--yes] Recipe wizard only — also the path for recipes
                        that need caller-supplied options (license
                        holder, codeowners, …).
`

func (a *App) runSetup(argv []string) int {
	for _, arg := range argv {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(a.Stdout, setupUsage)
			return 0
		case "--yes", "-y", "--legacy":
			// honoured downstream
		default:
			if strings.HasPrefix(arg, "--") {
				fmt.Fprintf(a.Stderr, "clawtool setup: unknown flag %q\n%s", arg, setupUsage)
				return 2
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool setup: cwd: %v\n", err)
		return 1
	}
	return a.runSetupV2(argv, cwd)
}
