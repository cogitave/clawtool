// Package cli — `clawtool` (no args) launches a friendly TUI
// landing menu that points at the most-used flows. Designed for
// users who'd rather not memorise subcommands; pure huh.Select.
package cli

import (
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
)

// menuChoice is the discriminator the landing menu returns. Each
// choice maps to an existing CLI flow so the menu adds no new
// surface to support — it's an alias dispatcher.
type menuChoice string

const (
	menuOnboard menuChoice = "onboard"
	menuInit    menuChoice = "init"
	menuRecipe  menuChoice = "recipe"
	menuDoctor  menuChoice = "doctor"
	menuSkill   menuChoice = "skill"
	menuSource  menuChoice = "source"
	menuAgents  menuChoice = "agents"
	menuVersion menuChoice = "version"
	menuExit    menuChoice = "exit"
)

// runMenu shows the landing menu and dispatches to the chosen
// flow. Returns the exit code of the chosen subcommand. Aborts
// (Ctrl-C) → return 0 silently, no error report.
func (a *App) runMenu() int {
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		// Non-TTY environments hit this when the user pipes input
		// or runs in CI; fall through to the existing topUsage
		// instead of getting stuck.
		fmt.Fprint(a.Stderr, topUsage)
		return 2
	}

	fmt.Fprintln(a.Stdout, "clawtool — pick what you want to do")
	fmt.Fprintln(a.Stdout)

	// First-run nudge — telemetry shows install→onboard
	// drop-off. When the operator hasn't completed the wizard yet,
	// pre-select onboard so the menu acts as a guided first step
	// instead of a flat catalogue. The hint above the form makes
	// the recommendation explicit.
	defaultPick := menuInit
	if !IsOnboarded() {
		fmt.Fprintln(a.Stdout, "👋  Looks like clawtool hasn't been onboarded yet on this machine.")
		fmt.Fprintln(a.Stdout, "    The wizard wires bridges, claims MCP hosts, and starts the daemon — pick \"Onboard\" below to run it now.")
		fmt.Fprintln(a.Stdout)
		defaultPick = menuOnboard
	}

	pick := defaultPick
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[menuChoice]().
			Title("Main menu").
			Description("Use ↑/↓ to navigate, <enter> to confirm. Pick \"exit\" to drop back to the shell.").
			Options(
				huh.NewOption("🚀  Onboard (first-run wizard — bridges, MCP claim, daemon)", menuOnboard),
				huh.NewOption("📦  Set up this repo (clawtool init wizard)", menuInit),
				huh.NewOption("🍽️   Browse / apply recipes (recipe list / status / apply)", menuRecipe),
				huh.NewOption("🩺  Diagnose my install (clawtool doctor)", menuDoctor),
				huh.NewOption("🧠  Author a new skill (skill new)", menuSkill),
				huh.NewOption("🌐  Add an MCP source (source add)", menuSource),
				huh.NewOption("🤖  Claim native tools on an agent (agents claim)", menuAgents),
				huh.NewOption("ℹ️   Print version", menuVersion),
				huh.NewOption("✕  Exit", menuExit),
			).
			Value(&pick),
	))
	if err := form.Run(); err != nil {
		// User aborted — silent exit.
		return 0
	}

	switch pick {
	case menuOnboard:
		return a.runOnboard(nil)
	case menuInit:
		return a.runInit(nil)
	case menuRecipe:
		// Default to listing; the user can re-invoke with apply
		// once they've picked a recipe.
		return a.runRecipe([]string{"list"})
	case menuDoctor:
		return a.runDoctor(nil)
	case menuSkill:
		return a.runMenuSkillNew()
	case menuSource:
		return a.runMenuSourceAdd()
	case menuAgents:
		return a.runMenuAgentsClaim()
	case menuVersion:
		// version printing is owned by main.go; delegate via Run.
		return a.Run([]string{"version"})
	case menuExit:
		return 0
	}
	return 0
}

// runMenuSkillNew prompts for the skill name + description + triggers
// using huh forms, then calls runSkillNew with the synthesised argv.
// Same code path as the CLI flag form — guarantees consistency.
func (a *App) runMenuSkillNew() int {
	var name, desc, triggers string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Skill name (kebab-case)").
			Description("e.g. \"karpathy-llm-wiki\". Becomes the directory + frontmatter name.").
			Value(&name),
		huh.NewInput().
			Title("Description").
			Description("One paragraph that tells the agent WHEN to load this skill.").
			Value(&desc),
		huh.NewInput().
			Title("Triggers (optional)").
			Description("Comma-separated phrases. Leave empty to skip.").
			Value(&triggers),
	))
	if err := form.Run(); err != nil {
		return 0
	}
	args := []string{name, "--description", desc}
	if triggers != "" {
		args = append(args, "--triggers", triggers)
	}
	return a.runSkillNew(args)
}

// runMenuSourceAdd prompts for a catalog source name + optional
// alias, then calls runSourceAdd. Source-specific secrets are
// surfaced afterwards via `source check` so the user knows what's
// missing.
func (a *App) runMenuSourceAdd() int {
	var name, alias string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Source name (catalog or custom)").
			Description("e.g. \"github\", \"slack\", \"context7\", \"playwright\". Run \"clawtool source list\" later to see what's configured.").
			Value(&name),
		huh.NewInput().
			Title("Instance alias (optional)").
			Description("Used when you want two of the same source — e.g. github-personal vs github-work. Leave blank for default.").
			Value(&alias),
	))
	if err := form.Run(); err != nil {
		return 0
	}
	args := []string{"add", name}
	if alias != "" {
		args = append(args, "--as", alias)
	}
	return a.runSource(args)
}

// runMenuAgentsClaim asks which adapter to claim. The list comes
// from the agents.Registry in the same way `clawtool agents
// list` does.
func (a *App) runMenuAgentsClaim() int {
	// Use the existing agents list dispatcher — it prints the
	// adapter rundown so the user can see which is detected.
	a.runAgents([]string{"list"})

	var which string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Agent to claim").
			Description("Type the adapter name from the list above (e.g. \"claude-code\"). Empty cancels.").
			Value(&which),
	))
	if err := form.Run(); err != nil {
		return 0
	}
	if which == "" {
		return 0
	}
	return a.runAgents([]string{"claim", which})
}
