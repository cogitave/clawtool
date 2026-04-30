package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/cogitave/clawtool/internal/cli/listfmt"
	"github.com/cogitave/clawtool/internal/setup"
	"github.com/cogitave/clawtool/internal/setup/recipes/bridges"

	// Same blank import as recipe.go: ensures the bridges package's
	// init() runs before any subcommand. recipes/all.go covers it
	// transitively but importing directly keeps this file's
	// dependency explicit (the bridge surface predates its inclusion
	// in some downstream packages).
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// bridgeAddJSON is the wire shape produced by `bridge add --json`
// (and `bridge upgrade --json` since they share the same install
// logic). snake_case keys per the project's wire convention.
// Mirrors the human-output banner fields.
type bridgeAddJSON struct {
	Family       string   `json:"family"`
	Action       string   `json:"action"` // "add" | "upgrade"
	Recipe       string   `json:"recipe,omitempty"`
	Installed    bool     `json:"installed"`
	VerifyOK     bool     `json:"verify_ok"`
	VerifyError  string   `json:"verify_error,omitempty"`
	SkipReason   string   `json:"skip_reason,omitempty"`
	ManualHints  []string `json:"manual_hints,omitempty"`
	InstalledRaw []string `json:"installed_prereqs,omitempty"`
	Error        string   `json:"error,omitempty"`
}

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
		family, asJSON, err := parseBridgeAddArgs(argv[1:])
		if err != nil {
			fmt.Fprintf(a.Stderr, "usage: clawtool bridge add <family> [--json]\n")
			return 2
		}
		if asJSON {
			return a.bridgeAddJSON(family, "add")
		}
		if err := a.BridgeAdd(family); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bridge add: %v\n", err)
			return 1
		}
	case "list":
		format, _, err := listfmt.ExtractFlag(argv[1:])
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bridge list: %v\n", err)
			return 2
		}
		if err := a.BridgeList(format); err != nil {
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
		family, asJSON, err := parseBridgeAddArgs(argv[1:])
		if err != nil {
			fmt.Fprintf(a.Stderr, "usage: clawtool bridge upgrade <family> [--json]\n")
			return 2
		}
		if asJSON {
			return a.bridgeAddJSON(family, "upgrade")
		}
		if err := a.BridgeAdd(family); err != nil {
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
// Output format follows the operator's --format flag: table (default,
// human-readable), tsv (pipe-friendly), json (programmatic).
func (a *App) BridgeList(format listfmt.Format) error {
	w := a.Stdout
	fams := bridges.Families()
	if len(fams) == 0 {
		fmt.Fprintln(w, "(no bridges registered — internal error: bridges/init missing)")
		return nil
	}
	sort.Strings(fams)
	cols := listfmt.Cols{
		Header: []string{"FAMILY", "STATUS", "DESCRIPTION"},
	}
	for _, fam := range fams {
		r := bridges.LookupByFamily(fam)
		if r == nil {
			continue
		}
		status, _, _ := r.Detect(context.Background(), "")
		cols.Rows = append(cols.Rows, []string{fam, string(status), r.Meta().Description})
	}
	return listfmt.Render(w, format, cols)
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

// parseBridgeAddArgs extracts <family> + an optional --json flag
// from the argv slice. The bridge-add subcommand was previously
// strict positional ("exactly one arg, the family"); this loosens
// that to accept a single --json flag in any position alongside
// the family name. Returns (family, asJSON, err).
func parseBridgeAddArgs(argv []string) (string, bool, error) {
	var family string
	asJSON := false
	for _, a := range argv {
		switch a {
		case "--json", "--format=json":
			asJSON = true
		default:
			if family != "" {
				return "", false, fmt.Errorf("multiple non-flag args; expected one family")
			}
			family = a
		}
	}
	if family == "" {
		return "", false, fmt.Errorf("missing family")
	}
	return family, asJSON, nil
}

// bridgeAddJSON runs the same install/upgrade orchestration
// `BridgeAdd` does, but emits the structured `bridgeAddJSON`
// payload instead of the human banner. Always prints to stdout
// (even on error) so a caller running `clawtool bridge add codex
// --json | jq -e '.installed'` can branch on the structured
// fields without parsing stderr.
//
// Returns the CLI exit code (0 on install / verify-warn, 1 on a
// hard failure). Mirrors the human path's exit semantics so
// scripts that gate on exit codes work uniformly.
func (a *App) bridgeAddJSON(family, action string) int {
	out := bridgeAddJSON{Family: family, Action: action}

	r := bridges.LookupByFamily(family)
	if r == nil {
		out.Error = fmt.Sprintf("unknown family %q (known: %s)", family, joinFamilies())
		a.writeBridgeJSON(out)
		return 1
	}
	out.Recipe = r.Meta().Name

	cwd, err := os.Getwd()
	if err != nil {
		out.Error = err.Error()
		a.writeBridgeJSON(out)
		return 1
	}
	res, err := setup.Apply(context.Background(), r, setup.ApplyOptions{
		Repo:     cwd,
		Prompter: setup.AlwaysSkip{},
	})
	if res.SkipReason != "" {
		out.SkipReason = res.SkipReason
	}
	if len(res.ManualHints) > 0 {
		out.ManualHints = append([]string(nil), res.ManualHints...)
	}
	if len(res.Installed) > 0 {
		out.InstalledRaw = append([]string(nil), res.Installed...)
	}
	if err != nil {
		out.Error = err.Error()
		a.writeBridgeJSON(out)
		return 1
	}
	if res.VerifyErr != nil {
		out.Installed = true
		out.VerifyOK = false
		out.VerifyError = res.VerifyErr.Error()
		a.writeBridgeJSON(out)
		// Match the human path: verify-warn returns 0 (the
		// bridge IS applied; the verify is informational).
		return 0
	}
	out.Installed = true
	out.VerifyOK = true
	a.writeBridgeJSON(out)
	return 0
}

func (a *App) writeBridgeJSON(payload bridgeAddJSON) {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool bridge: marshal: %v\n", err)
		return
	}
	fmt.Fprintln(a.Stdout, string(body))
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
