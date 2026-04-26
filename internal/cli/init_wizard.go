// Package cli — `clawtool init` wizard. Runs an interactive multi-
// select per category in TTY mode (charmbracelet/huh), or applies the
// safe defaults non-interactively when --yes is set or stdin isn't a
// TTY.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/setup"
)

// recipeChoice is one selectable row in a category form.
type recipeChoice struct {
	Name        string
	Description string
	Status      setup.Status
}

// runInit is the dispatcher entry. Honors `--yes` for non-interactive
// runs, falls back to the same path when stdin isn't a TTY, otherwise
// drives the huh-based wizard category-by-category.
func (a *App) runInit(argv []string) int {
	yes := false
	for _, arg := range argv {
		if arg == "--yes" || arg == "-y" {
			yes = true
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool init: cwd: %v\n", err)
		return 1
	}

	// Banner so the user has clear context for what's about to happen.
	fmt.Fprintf(a.Stdout, "clawtool init — repo at %s\n\n", cwd)

	noTTY := !isTTY(os.Stdin) || !isTTY(os.Stdout)
	if yes || noTTY {
		return a.runInitNonInteractive(cwd)
	}
	return a.runInitInteractive(cwd)
}

// runInitNonInteractive applies the recipe defaults without prompting.
// "Defaults" means: every Stable recipe whose Detect reports Absent
// gets applied with empty Options. Recipes that need required Options
// (license[holder], codeowners[owners]) are skipped with a notice.
func (a *App) runInitNonInteractive(cwd string) int {
	any := false
	for _, cat := range setup.Categories() {
		for _, r := range setup.InCategory(cat) {
			m := r.Meta()
			if m.Stability != setup.StabilityStable && m.Stability != "" {
				continue
			}
			status, _, _ := r.Detect(context.Background(), cwd)
			if status != setup.StatusAbsent {
				continue
			}
			res, err := setup.Apply(context.Background(), r, setup.ApplyOptions{
				Repo:     cwd,
				Prompter: setup.AlwaysSkip{},
			})
			if err != nil {
				if errors.Is(err, setup.ErrSkippedByUser) {
					fmt.Fprintf(a.Stdout, "  ↷ skipped %s — %s\n", m.Name, res.SkipReason)
					continue
				}
				// Required-option errors (license needs holder, etc.)
				// surface as plain errors here. Don't fail the whole
				// init, just note and move on.
				fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", m.Name, err)
				continue
			}
			fmt.Fprintf(a.Stdout, "  ✓ applied %s\n", m.Name)
			any = true
		}
	}
	if !any {
		fmt.Fprintln(a.Stdout, "Nothing applied. Run with -h to see what `clawtool init` does, or use `clawtool recipe apply <name>` for a specific recipe.")
	}
	return 0
}

// runInitInteractive walks the user through each category with a
// huh.MultiSelect, then applies the chosen recipes. Per-recipe Options
// (license holder, codeowners handles) are collected via a follow-up
// huh.Input in the same form sequence.
func (a *App) runInitInteractive(cwd string) int {
	type pick struct {
		recipe setup.Recipe
		opts   setup.Options
	}
	var picks []pick

	for _, cat := range setup.Categories() {
		recipes := setup.InCategory(cat)
		if len(recipes) == 0 {
			continue
		}

		// Pre-select recipes whose Detect == Absent and that don't
		// need required options (those will fail on apply otherwise
		// — for v1 the wizard keeps the surface narrow).
		options := make([]huh.Option[string], 0, len(recipes))
		var preSelected []string
		for _, r := range recipes {
			m := r.Meta()
			status, detail, _ := r.Detect(context.Background(), cwd)
			label := fmt.Sprintf("%-26s  %s — %s", m.Name, status, m.Description)
			if detail != "" && status != setup.StatusAbsent {
				label += "  (" + detail + ")"
			}
			options = append(options, huh.NewOption(label, m.Name))
			if status == setup.StatusAbsent && !needsRequiredOptions(m.Name) {
				preSelected = append(preSelected, m.Name)
			}
		}

		var chosen []string
		chosen = append(chosen, preSelected...)
		f := huh.NewForm(huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(fmt.Sprintf("[%s] %s", cat, setup.CategoryDescriptions()[cat])).
				Options(options...).
				Value(&chosen),
		))
		if err := f.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Fprintln(a.Stdout, "\nAborted by user. Nothing applied.")
				return 0
			}
			fmt.Fprintf(a.Stderr, "clawtool init: %v\n", err)
			return 1
		}

		for _, name := range chosen {
			r := setup.Lookup(name)
			if r == nil {
				continue
			}
			opts, ok := promptForOptions(name)
			if !ok {
				fmt.Fprintf(a.Stdout, "  ↷ skipped %s\n", name)
				continue
			}
			picks = append(picks, pick{recipe: r, opts: opts})
		}
	}

	if len(picks) == 0 {
		fmt.Fprintln(a.Stdout, "Nothing selected. Run again any time with `clawtool init`.")
		return 0
	}

	fmt.Fprintf(a.Stdout, "\nApplying %d recipe(s):\n", len(picks))
	for _, p := range picks {
		res, err := setup.Apply(context.Background(), p.recipe, setup.ApplyOptions{
			Repo:          cwd,
			RecipeOptions: p.opts,
			Prompter:      setup.AlwaysSkip{}, // wizard prereq prompts wired in v0.10
		})
		if err != nil {
			if errors.Is(err, setup.ErrSkippedByUser) {
				fmt.Fprintf(a.Stdout, "  ↷ %s — %s\n", p.recipe.Meta().Name, res.SkipReason)
				continue
			}
			fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", p.recipe.Meta().Name, err)
			continue
		}
		fmt.Fprintf(a.Stdout, "  ✓ %s\n", p.recipe.Meta().Name)
	}
	fmt.Fprintln(a.Stdout, "\nDone. `clawtool recipe status` shows what's installed.")
	return 0
}

// needsRequiredOptions identifies recipes that won't apply with empty
// Options (license needs holder, codeowners needs owners). The wizard
// excludes these from auto-pre-select and prompts for the values
// when the user opts in.
func needsRequiredOptions(name string) bool {
	switch name {
	case "license", "codeowners":
		return true
	}
	return false
}

// promptForOptions runs a per-recipe huh.Input chain to collect the
// required Options. Returns ok=false if the user cancelled or left a
// required field empty.
func promptForOptions(name string) (setup.Options, bool) {
	switch name {
	case "license":
		var holder, spdx string
		spdx = "MIT"
		f := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("LICENSE — copyright holder").
				Description("Used in `Copyright (c) <year> <holder>`. Required.").
				Value(&holder),
			huh.NewSelect[string]().
				Title("LICENSE — SPDX id").
				Options(
					huh.NewOption("MIT", "MIT"),
					huh.NewOption("Apache-2.0", "Apache-2.0"),
					huh.NewOption("BSD-3-Clause", "BSD-3-Clause"),
				).
				Value(&spdx),
		))
		if err := f.Run(); err != nil {
			return nil, false
		}
		if strings.TrimSpace(holder) == "" {
			return nil, false
		}
		return setup.Options{"holder": holder, "spdx": spdx}, true
	case "codeowners":
		var ownersRaw string
		f := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("CODEOWNERS — owners for the catch-all rule").
				Description("Space- or comma-separated GitHub handles, e.g. `@you @team/maintainers`. Required.").
				Value(&ownersRaw),
		))
		if err := f.Run(); err != nil {
			return nil, false
		}
		owners := splitOwners(ownersRaw)
		if len(owners) == 0 {
			return nil, false
		}
		return setup.Options{"owners": owners}, true
	default:
		return setup.Options{}, true
	}
}

func splitOwners(raw string) []string {
	raw = strings.ReplaceAll(raw, ",", " ")
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// isTTY reports whether f is connected to a terminal. Used to decide
// whether to drop into the huh wizard or apply defaults silently.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
