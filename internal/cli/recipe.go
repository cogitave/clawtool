package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"

	// Blank imports trigger each recipe package's init() so the
	// global registry is populated before any subcommand runs.
	// New recipe packages are added here.
	_ "github.com/cogitave/clawtool/internal/setup/recipes/commits"
	_ "github.com/cogitave/clawtool/internal/setup/recipes/governance"
	_ "github.com/cogitave/clawtool/internal/setup/recipes/supplychain"
)

const recipeUsage = `Usage:
  clawtool recipe list [--category <c>]
                            List recipes with their state in the current
                            repo. Optionally filter to one of the 9
                            categories: governance, commits, release, ci,
                            quality, supply-chain, knowledge, agents,
                            runtime.
  clawtool recipe status [<name>]
                            Show Detect status for a single recipe or all
                            recipes against the current working directory.
  clawtool recipe apply <name> [key=value ...]
                            Apply <name> to the current working directory.
                            Options are key=value pairs; values containing
                            commas become string slices. Examples:
                              clawtool recipe apply license holder="Jane Doe" spdx=MIT
                              clawtool recipe apply codeowners owners=@me,@team
                              clawtool recipe apply dependabot
`

// RecipeList prints every registered recipe grouped by category with
// its Detect state in the cwd.
func (a *App) RecipeList(category string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if category != "" {
		cat := setup.Category(category)
		if !cat.Valid() {
			return fmt.Errorf("unknown category %q (valid: %s)", category, joinCategories())
		}
		return a.recipeListInCategory(cwd, cat)
	}

	w := a.Stdout
	for _, cat := range setup.Categories() {
		recipes := setup.InCategory(cat)
		if len(recipes) == 0 {
			continue
		}
		fmt.Fprintf(w, "[%s] — %s\n", cat, setup.CategoryDescriptions()[cat])
		printRecipeRows(w, cwd, recipes)
		fmt.Fprintln(w)
	}
	return nil
}

func (a *App) recipeListInCategory(cwd string, cat setup.Category) error {
	w := a.Stdout
	fmt.Fprintf(w, "[%s] — %s\n", cat, setup.CategoryDescriptions()[cat])
	recipes := setup.InCategory(cat)
	if len(recipes) == 0 {
		fmt.Fprintln(w, "  (no recipes shipped yet)")
		return nil
	}
	printRecipeRows(w, cwd, recipes)
	return nil
}

func printRecipeRows(w interface{ Write([]byte) (int, error) }, repo string, recipes []setup.Recipe) {
	for _, r := range recipes {
		m := r.Meta()
		status, _, _ := r.Detect(context.Background(), repo)
		fmt.Fprintf(w, "  %-26s %-10s %s\n", m.Name, status, m.Description)
	}
}

// RecipeStatus prints the Detect output for one recipe or all recipes.
func (a *App) RecipeStatus(name string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if name != "" {
		r := setup.Lookup(name)
		if r == nil {
			return fmt.Errorf("unknown recipe %q (run `clawtool recipe list`)", name)
		}
		status, detail, derr := r.Detect(context.Background(), cwd)
		if derr != nil {
			return fmt.Errorf("detect: %w", derr)
		}
		fmt.Fprintf(a.Stdout, "%s [%s] — %s\n  %s\n", r.Meta().Name, r.Meta().Category, status, detail)
		return nil
	}
	// All recipes.
	for _, cat := range setup.Categories() {
		for _, r := range setup.InCategory(cat) {
			status, detail, _ := r.Detect(context.Background(), cwd)
			fmt.Fprintf(a.Stdout, "%-26s %-10s %s\n", r.Meta().Name, status, detail)
		}
	}
	return nil
}

// RecipeApply runs the recipe against the current working directory.
// Options are key=value strings; comma-separated values become
// []string. v0.9 keeps options simple — wizard / MCP path will pass
// richer types as the surface evolves.
func (a *App) RecipeApply(name string, kvs []string) error {
	r := setup.Lookup(name)
	if r == nil {
		return fmt.Errorf("unknown recipe %q (run `clawtool recipe list`)", name)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	opts := parseKV(kvs)

	// AlwaysSkip prompter: in v0.9 CLI we don't auto-install prereqs
	// from the bare `recipe apply` invocation. The wizard (`clawtool
	// init`) is where prereq install offers live; here, missing
	// prereqs surface as a skip with the manual hint.
	res, applyErr := setup.Apply(context.Background(), r, setup.ApplyOptions{
		Repo:          cwd,
		RecipeOptions: opts,
		Prompter:      setup.AlwaysSkip{},
	})
	if applyErr != nil {
		// ErrSkippedByUser is expected when a prereq is missing.
		fmt.Fprintf(a.Stderr, "✘ apply %s: %v\n", name, applyErr)
		if res.SkipReason != "" {
			fmt.Fprintf(a.Stderr, "  reason: %s\n", res.SkipReason)
		}
		return applyErr
	}
	if res.VerifyErr != nil {
		fmt.Fprintf(a.Stdout, "⚠ %s applied but Verify reported: %v\n", name, res.VerifyErr)
		return nil
	}
	fmt.Fprintf(a.Stdout, "✓ applied %s [%s]\n", res.Recipe, res.Category)
	for _, h := range res.ManualHints {
		fmt.Fprintf(a.Stdout, "  manual prereq: %s\n", h)
	}
	for _, i := range res.Installed {
		fmt.Fprintf(a.Stdout, "  installed prereq: %s\n", i)
	}
	return nil
}

// parseKV converts ["k=v", "k2=v2,v3"] into Options. Bare commas in
// the value split into a string slice; values without commas remain
// scalar strings.
func parseKV(kvs []string) setup.Options {
	if len(kvs) == 0 {
		return nil
	}
	opts := setup.Options{}
	for _, raw := range kvs {
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			continue
		}
		key := raw[:eq]
		val := raw[eq+1:]
		// Strip a single layer of surrounding quotes a shell might leave.
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		if strings.Contains(val, ",") {
			parts := strings.Split(val, ",")
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				out = append(out, strings.TrimSpace(p))
			}
			opts[key] = out
			continue
		}
		// Numeric values come through as float64 to match JSON shape.
		if n, err := strconv.ParseFloat(val, 64); err == nil {
			opts[key] = n
			continue
		}
		if b, err := strconv.ParseBool(val); err == nil {
			opts[key] = b
			continue
		}
		opts[key] = val
	}
	return opts
}

func joinCategories() string {
	cats := setup.Categories()
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// runRecipe is the dispatcher hooked into Run().
func (a *App) runRecipe(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, recipeUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		category := ""
		args := argv[1:]
		// Tiny --category parser; flag pkg is overkill for one optional.
		for i := 0; i < len(args); i++ {
			if args[i] == "--category" && i+1 < len(args) {
				category = args[i+1]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--category=") {
				category = strings.TrimPrefix(args[i], "--category=")
				continue
			}
		}
		if err := a.RecipeList(category); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool recipe list: %v\n", err)
			return 1
		}
	case "status":
		name := ""
		if len(argv) >= 2 {
			name = argv[1]
		}
		if err := a.RecipeStatus(name); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool recipe status: %v\n", err)
			return 1
		}
	case "apply":
		if len(argv) < 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool recipe apply <name> [key=value ...]\n")
			return 2
		}
		name := argv[1]
		if err := a.RecipeApply(name, argv[2:]); err != nil {
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool recipe: unknown subcommand %q\n\n%s", argv[0], recipeUsage)
		return 2
	}
	return 0
}
