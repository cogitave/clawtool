// Package cli — `clawtool init` wizard. Two-scope welcome screen:
// repo (recipes) and user-global (agents + sources + secrets), with
// "both" and a read-only "show me what's available" preview path.
//
// Honors --yes for non-interactive runs and falls back to the same
// path when stdin/stdout aren't TTYs (CI / containers).
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/catalog"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/setup"
)

// initScope is the top-level branch the wizard takes.
type initScope string

const (
	scopeRepo    initScope = "repo"
	scopeUser    initScope = "user"
	scopeBoth    initScope = "both"
	scopePreview initScope = "preview"
)

// runInit is the dispatcher entry. Honors --yes and TTY detection,
// otherwise routes to the chosen scope.
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

	fmt.Fprintf(a.Stdout, "clawtool init — %s\n\n", cwd)

	noTTY := !isTTY(os.Stdin) || !isTTY(os.Stdout)
	if yes || noTTY {
		// Non-interactive: only the repo scope's "Stable + no
		// required-options" subset is safe to apply unattended.
		// User-global changes (agent claims, source registration)
		// are too consequential to apply without consent.
		return a.runInitRepoNonInteractive(cwd)
	}

	scope, err := promptScope()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool init: %v\n", err)
		return 1
	}

	switch scope {
	case scopePreview:
		return a.runInitPreview(cwd)
	case scopeRepo:
		return a.runInitRepoInteractive(cwd)
	case scopeUser:
		return a.runInitUserInteractive()
	case scopeBoth:
		if rc := a.runInitUserInteractive(); rc != 0 {
			return rc
		}
		return a.runInitRepoInteractive(cwd)
	}
	return 0
}

// promptScope shows the welcome screen and returns the user's choice.
func promptScope() (initScope, error) {
	var scope initScope
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[initScope]().
			Title("What would you like to set up?").
			Description("clawtool can scaffold this repo, configure your global clawtool, or both.").
			Options(
				huh.NewOption("This repository (recipes: license, dependabot, release-please, …)", scopeRepo),
				huh.NewOption("My clawtool (agents to claim, MCP sources to add, secrets)", scopeUser),
				huh.NewOption("Both — clawtool first, then this repo", scopeBoth),
				huh.NewOption("Just show me what's available (read-only)", scopePreview),
			).
			Value(&scope),
	))
	if err := f.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", errors.New("aborted")
		}
		return "", err
	}
	return scope, nil
}

// ── repo scope ─────────────────────────────────────────────────────

// runInitRepoNonInteractive applies recipe defaults — every Stable
// recipe whose Detect reports Absent and that doesn't need required
// Options. Recipes needing user input (license, codeowners) are
// skipped cleanly.
func (a *App) runInitRepoNonInteractive(cwd string) int {
	any := false
	for _, cat := range setup.Categories() {
		for _, r := range setup.InCategory(cat) {
			m := r.Meta()
			if m.Stability != setup.StabilityStable && m.Stability != "" {
				continue
			}
			if needsRequiredOptions(m.Name) {
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
				fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", m.Name, err)
				continue
			}
			fmt.Fprintf(a.Stdout, "  ✓ applied %s\n", m.Name)
			any = true
		}
	}
	if !any {
		fmt.Fprintln(a.Stdout, "Nothing applied. Run `clawtool init` interactively to pick recipes.")
	}
	return 0
}

// runInitRepoInteractive walks the user through each non-empty
// category. Default selection is empty — pressing enter immediately
// is the natural skip. The category's title states this so users
// don't need to learn it from documentation.
func (a *App) runInitRepoInteractive(cwd string) int {
	type pick struct {
		recipe setup.Recipe
		opts   setup.Options
	}
	var picks []pick

	fmt.Fprintln(a.Stdout, "\n— Set up this repository —")

	for _, cat := range setup.Categories() {
		recipes := setup.InCategory(cat)
		if len(recipes) == 0 {
			continue
		}

		options := make([]huh.Option[string], 0, len(recipes))
		for _, r := range recipes {
			m := r.Meta()
			status, detail, _ := r.Detect(context.Background(), cwd)
			label := fmt.Sprintf("%-26s  %-9s  %s", m.Name, statusLabel(status), m.Description)
			if detail != "" && status != setup.StatusAbsent {
				label += "  (" + detail + ")"
			}
			options = append(options, huh.NewOption(label, m.Name))
		}

		var chosen []string // empty by default — enter == skip
		title := fmt.Sprintf("[%s] %s", cat, setup.CategoryDescriptions()[cat])
		f := huh.NewForm(huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(title).
				Description("Press <space> to toggle, <enter> to confirm. Press <enter> with nothing selected to skip this category.").
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
		fmt.Fprintln(a.Stdout, "\nNothing selected for the repository. Done.")
		return 0
	}

	prompter, runner := newWizardPrompter(a.Stdout, a.Stderr)
	fmt.Fprintf(a.Stdout, "\nApplying %d recipe(s):\n", len(picks))
	for _, p := range picks {
		res, err := setup.Apply(context.Background(), p.recipe, setup.ApplyOptions{
			Repo:          cwd,
			RecipeOptions: p.opts,
			Prompter:      prompter,
			Runner:        runner,
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

// runInitPreview prints the read-only state — what recipes are
// shipped, current detection status. No prompts, no writes.
func (a *App) runInitPreview(cwd string) int {
	fmt.Fprintln(a.Stdout, "\n— Available recipes —")
	for _, cat := range setup.Categories() {
		recipes := setup.InCategory(cat)
		if len(recipes) == 0 {
			continue
		}
		fmt.Fprintf(a.Stdout, "\n[%s] %s\n", cat, setup.CategoryDescriptions()[cat])
		for _, r := range recipes {
			m := r.Meta()
			status, _, _ := r.Detect(context.Background(), cwd)
			fmt.Fprintf(a.Stdout, "  %-26s %-10s %s\n", m.Name, statusLabel(status), m.Description)
		}
	}
	fmt.Fprintln(a.Stdout, "\nRun `clawtool init` again without --preview to apply.")
	return 0
}

// ── user scope ─────────────────────────────────────────────────────

// runInitUserInteractive walks the user through configuring clawtool
// itself: agent claims and catalog sources (with secret prompts).
func (a *App) runInitUserInteractive() int {
	fmt.Fprintln(a.Stdout, "\n— Configure clawtool —")

	// 1. Agent claims.
	if rc := a.wizardAgentClaims(); rc != 0 {
		return rc
	}
	// 2. Catalog sources.
	if rc := a.wizardSources(); rc != 0 {
		return rc
	}

	fmt.Fprintln(a.Stdout, "\nclawtool configured. `clawtool agents status` and `clawtool source list` show the result.")
	return 0
}

// wizardAgentClaims surveys every registered agent adapter and lets
// the user toggle claim status. Already-claimed agents are
// pre-selected as a safe default ("don't lose existing claims").
func (a *App) wizardAgentClaims() int {
	if len(agents.Registry) == 0 {
		return 0
	}

	rows, options, preSelected := snapshotAgentRows()

	chosen := append([]string{}, preSelected...)
	f := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Agents to claim").
			Description("Claiming an agent disables its native Bash/Read/Edit/etc. so the model only sees mcp__clawtool__*. Already-claimed agents are pre-selected. Deselect to release.").
			Options(options...).
			Value(&chosen),
	))
	if err := f.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool init: %v\n", err)
		return 1
	}

	a.applyAgentClaimDiff(rows, chosen)
	return 0
}

// agentRow is the per-agent data the wizard threads through huh
// (label rendering, pre-selection) and back into the diff applier.
type agentRow struct {
	Name     string
	Detected bool
	Claimed  bool
}

// snapshotAgentRows queries every registered adapter for its status
// and packages the data the wizard needs: the rows themselves, the
// huh option slice (label + value), and the pre-selected names
// (already-claimed agents). Pure helper — no prompts.
func snapshotAgentRows() ([]agentRow, []huh.Option[string], []string) {
	rows := make([]agentRow, 0, len(agents.Registry))
	options := make([]huh.Option[string], 0, len(agents.Registry))
	preSelected := []string{}

	for _, ad := range agents.Registry {
		s, err := ad.Status()
		if err != nil {
			continue
		}
		row := agentRow{Name: ad.Name(), Detected: s.Detected, Claimed: s.Claimed}
		rows = append(rows, row)
		marker := "○"
		hint := "not detected"
		if row.Detected {
			hint = "detected"
		}
		if row.Claimed {
			marker = "●"
			hint = "already claimed"
			preSelected = append(preSelected, row.Name)
		}
		label := fmt.Sprintf("%s  %-15s  %s", marker, row.Name, hint)
		options = append(options, huh.NewOption(label, row.Name))
	}
	return rows, options, preSelected
}

// applyAgentClaimDiff reconciles the user's chosen set against the
// current claim status: claims agents that are now wanted but not
// claimed, releases agents that were claimed but no longer wanted.
// Pure orchestration over agents.Find — easy to test.
func (a *App) applyAgentClaimDiff(rows []agentRow, chosen []string) {
	chosenSet := map[string]bool{}
	for _, n := range chosen {
		chosenSet[n] = true
	}
	for _, row := range rows {
		want := chosenSet[row.Name]
		switch {
		case want && !row.Claimed:
			ad, err := agents.Find(row.Name)
			if err != nil {
				fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", row.Name, err)
				continue
			}
			if _, err := ad.Claim(agents.Options{}); err != nil {
				fmt.Fprintf(a.Stdout, "  ✘ claim %s — %v\n", row.Name, err)
				continue
			}
			fmt.Fprintf(a.Stdout, "  ✓ claimed %s\n", row.Name)
		case !want && row.Claimed:
			ad, err := agents.Find(row.Name)
			if err != nil {
				fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", row.Name, err)
				continue
			}
			if _, err := ad.Release(agents.Options{}); err != nil {
				fmt.Fprintf(a.Stdout, "  ✘ release %s — %v\n", row.Name, err)
				continue
			}
			fmt.Fprintf(a.Stdout, "  ↺ released %s\n", row.Name)
		}
	}
}

// wizardSources lets the user pick MCP sources from the built-in
// catalog and prompts for each required env var (which lands in the
// secrets store as scope=<instance>, key=<ENV_NAME>).
func (a *App) wizardSources() int {
	cat, err := catalog.Builtin()
	if err != nil {
		fmt.Fprintf(a.Stderr, "wizard: catalog: %v\n", err)
		return 0
	}

	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		fmt.Fprintf(a.Stderr, "wizard: load config: %v\n", err)
		return 0
	}

	options := make([]huh.Option[string], 0)
	for _, ne := range cat.List() {
		if _, exists := cfg.Sources[ne.Name]; exists {
			continue // already configured — skip from the picker
		}
		envHint := ""
		if len(ne.Entry.RequiredEnv) > 0 {
			envHint = " (needs: " + strings.Join(ne.Entry.RequiredEnv, ", ") + ")"
		}
		label := fmt.Sprintf("%-22s %s%s", ne.Name, ne.Entry.Description, envHint)
		options = append(options, huh.NewOption(label, ne.Name))
	}
	if len(options) == 0 {
		fmt.Fprintln(a.Stdout, "  (every catalog source is already configured — `clawtool source list` to see)")
		return 0
	}

	var chosen []string
	f := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("MCP sources to add").
			Description("Each picked source spawns a child MCP server when clawtool serves. You'll be asked for any required credentials next.").
			Options(options...).
			Value(&chosen),
	))
	if err := f.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool init: %v\n", err)
		return 1
	}
	if len(chosen) == 0 {
		return 0
	}

	store, err := secrets.LoadOrEmpty(a.SecretsPath())
	if err != nil {
		fmt.Fprintf(a.Stderr, "wizard: load secrets: %v\n", err)
		return 1
	}

	for _, name := range chosen {
		ne, ok := cat.Lookup(name)
		if !ok {
			continue
		}
		// Add the source to config.
		argv, err := ne.ToSourceCommand()
		if err != nil {
			fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", name, err)
			continue
		}
		if cfg.Sources == nil {
			cfg.Sources = map[string]config.Source{}
		}
		cfg.Sources[name] = config.Source{
			Type:    "mcp",
			Command: argv,
			Env:     ne.EnvTemplate(),
		}

		// Prompt for each required env.
		if len(ne.RequiredEnv) > 0 {
			fmt.Fprintf(a.Stdout, "  · %s — %s\n", name, ne.AuthHint)
			for _, key := range ne.RequiredEnv {
				var value string
				prompt := huh.NewForm(huh.NewGroup(
					huh.NewInput().
						Title(fmt.Sprintf("%s: %s", name, key)).
						EchoMode(huh.EchoModePassword).
						Description("Stored in secrets.toml (mode 0600). Leave empty to skip — you can run `clawtool source set-secret` later.").
						Value(&value),
				))
				if err := prompt.Run(); err != nil {
					if errors.Is(err, huh.ErrUserAborted) {
						break
					}
					fmt.Fprintf(a.Stderr, "wizard: %v\n", err)
					return 1
				}
				if strings.TrimSpace(value) != "" {
					store.Set(name, key, value)
				}
			}
		}

		fmt.Fprintf(a.Stdout, "  ✓ added %s\n", name)
	}

	if err := cfg.Save(a.Path()); err != nil {
		fmt.Fprintf(a.Stderr, "wizard: save config: %v\n", err)
		return 1
	}
	if err := store.Save(a.SecretsPath()); err != nil {
		fmt.Fprintf(a.Stderr, "wizard: save secrets: %v\n", err)
		return 1
	}
	return 0
}

// ── helpers ────────────────────────────────────────────────────────

// needsRequiredOptions identifies recipes that won't apply with
// empty Options (license needs holder, codeowners needs owners).
func needsRequiredOptions(name string) bool {
	switch name {
	case "license", "codeowners":
		return true
	}
	return false
}

// promptForOptions runs a per-recipe input chain to collect required
// Options. Returns ok=false if the user cancelled.
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

// statusLabel renders a uniform-width status badge in pretty output.
// Empty status (defensive) renders as a placeholder so column
// alignment doesn't break.
func statusLabel(s setup.Status) string {
	if s == "" {
		return "—"
	}
	return string(s)
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
