package cli

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/cogitave/clawtool/internal/setup"
	"github.com/cogitave/clawtool/internal/setup/recipes/bridges"

	// Same blank import as recipe.go: ensures the bridges package's
	// init() runs before any subcommand. recipes/all.go covers it
	// transitively but importing directly keeps this file's
	// dependency explicit (the bridge surface predates its inclusion
	// in some downstream packages).
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

const bridgeUsage = `Usage:
  clawtool bridge add <family>          Install the canonical bridge for the family.
                                          Families: codex, opencode, gemini.
  clawtool bridge list                  Show installed bridges with status.
  clawtool bridge remove <family>       (placeholder for v0.10.x — manual claude plugin remove for now)
  clawtool bridge upgrade <family>      Re-run the install (idempotent; pulls latest plugin version).
`

// runBridge is the dispatcher hooked into Run().
func (a *App) runBridge(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, bridgeUsage)
		return 2
	}
	switch argv[0] {
	case "add":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool bridge add <family>\n")
			return 2
		}
		if err := a.BridgeAdd(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bridge add: %v\n", err)
			return 1
		}
	case "list":
		if err := a.BridgeList(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bridge list: %v\n", err)
			return 1
		}
	case "remove":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool bridge remove <family>\n")
			return 2
		}
		if err := a.BridgeRemove(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bridge remove: %v\n", err)
			return 1
		}
	case "upgrade":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool bridge upgrade <family>\n")
			return 2
		}
		if err := a.BridgeAdd(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bridge upgrade: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool bridge: unknown subcommand %q\n\n%s", argv[0], bridgeUsage)
		return 2
	}
	return 0
}

// BridgeAdd resolves the family to its recipe and applies it. Idempotent;
// if the bridge is already installed Detect returns Applied and Apply
// short-circuits.
func (a *App) BridgeAdd(family string) error {
	r := bridges.LookupByFamily(family)
	if r == nil {
		return fmt.Errorf("unknown family %q (known: %s)", family, joinFamilies())
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	res, err := setup.Apply(context.Background(), r, setup.ApplyOptions{
		Repo:     cwd,
		Prompter: setup.AlwaysSkip{},
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "✘ bridge add %s: %v\n", family, err)
		if res.SkipReason != "" {
			fmt.Fprintf(a.Stderr, "  reason: %s\n", res.SkipReason)
		}
		return err
	}
	if res.VerifyErr != nil {
		fmt.Fprintf(a.Stdout, "⚠ %s bridge applied but Verify reported: %v\n", family, res.VerifyErr)
		return nil
	}
	fmt.Fprintf(a.Stdout, "✓ %s bridge installed (recipe %s)\n", family, res.Recipe)
	for _, h := range res.ManualHints {
		fmt.Fprintf(a.Stdout, "  manual prereq: %s\n", h)
	}
	for _, i := range res.Installed {
		fmt.Fprintf(a.Stdout, "  installed prereq: %s\n", i)
	}
	return nil
}

// BridgeList prints all known bridge recipes with their Detect state.
func (a *App) BridgeList() error {
	w := a.Stdout
	fams := bridges.Families()
	if len(fams) == 0 {
		fmt.Fprintln(w, "(no bridges registered — internal error: bridges/init missing)")
		return nil
	}
	sort.Strings(fams)
	fmt.Fprintf(w, "%-12s %-12s %s\n", "FAMILY", "STATUS", "DESCRIPTION")
	for _, fam := range fams {
		r := bridges.LookupByFamily(fam)
		if r == nil {
			continue
		}
		status, _, _ := r.Detect(context.Background(), "")
		fmt.Fprintf(w, "%-12s %-12s %s\n", fam, string(status), r.Meta().Description)
	}
	return nil
}

// BridgeRemove is a placeholder. Claude Code's `claude plugin remove`
// surface isn't standardized yet across plugin types; v0.10.x will
// add proper uninstall semantics. For now we print a manual hint.
func (a *App) BridgeRemove(family string) error {
	r := bridges.LookupByFamily(family)
	if r == nil {
		return fmt.Errorf("unknown family %q (known: %s)", family, joinFamilies())
	}
	fmt.Fprintf(a.Stdout,
		"manual: run `claude plugin remove %s` (clawtool's automated remove ships in v0.10.x)\n",
		r.Meta().Name,
	)
	return nil
}

func joinFamilies() string {
	fams := bridges.Families()
	sort.Strings(fams)
	return joinStrings(fams, ", ")
}

func joinStrings(s []string, sep string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
}
