// Package setup owns clawtool's project-setup layer: the recipe
// framework, the wizard, and the .clawtool.toml repo-scoped config.
//
// The user-facing CLI verb is `clawtool init`, but the internal
// package is named `setup` to avoid colliding with Go's reserved
// init() lifecycle function and to read clearly in imports.
//
// Recipes are organized into 9 fixed categories. The category set
// is part of clawtool's public API — at v1.0 it freezes. Adding a
// category is a major bump; adding recipes within a category is
// always free.
package setup

import "fmt"

// Category is the typed enum for recipe grouping. Defined as an
// exhaustive set so a recipe authored against a category that
// doesn't exist literally cannot compile.
type Category string

const (
	// CategoryGovernance covers files & policies that govern
	// collaboration: LICENSE, CODEOWNERS, CONTRIBUTING.md,
	// SECURITY.md, issue/PR templates, code of conduct.
	CategoryGovernance Category = "governance"

	// CategoryCommits covers commit-time discipline: format
	// conventions, message linting, pre-commit hooks, secret
	// scanning at commit time.
	CategoryCommits Category = "commits"

	// CategoryRelease covers version cutting + publishing: release
	// automation, changelog generation, artifact distribution.
	CategoryRelease Category = "release"

	// CategoryCI covers PR/push pipeline scaffolding: test runners,
	// build matrix, coverage uploads.
	CategoryCI Category = "ci"

	// CategoryQuality covers code quality enforcement: linters,
	// formatters, type checkers, test scaffolds.
	CategoryQuality Category = "quality"

	// CategorySupplyChain covers dependencies & security:
	// dependency updates, SBOM generation, vulnerability scanning.
	CategorySupplyChain Category = "supply-chain"

	// CategoryKnowledge covers project memory & docs: brain,
	// ADR tooling, documentation sites, changelog tooling.
	CategoryKnowledge Category = "knowledge"

	// CategoryAgents covers AI agent integration: agent claims,
	// project-scoped MCP sources, skill bindings. clawtool's USP.
	CategoryAgents Category = "agents"

	// CategoryRuntime covers dev environment & containers:
	// devcontainers, Docker, Nix, direnv, mise.
	CategoryRuntime Category = "runtime"
)

// Categories returns the categories in repo-maturity walk order
// (the order the wizard surfaces them). Frozen at v1.0.
func Categories() []Category {
	return []Category{
		CategoryGovernance,
		CategoryCommits,
		CategoryRelease,
		CategoryCI,
		CategoryQuality,
		CategorySupplyChain,
		CategoryKnowledge,
		CategoryAgents,
		CategoryRuntime,
	}
}

// CategoryDescriptions surfaces the one-line description shown in
// the wizard category header and in `recipe_categories()` MCP
// output. Kept here, not on each recipe, because the category
// boundary is a contract, not a recipe-author concern.
func CategoryDescriptions() map[Category]string {
	return map[Category]string{
		CategoryGovernance:  "Files & policies that govern collaboration",
		CategoryCommits:     "Commit-time discipline (format, hooks)",
		CategoryRelease:     "Version cutting & publishing",
		CategoryCI:          "PR/push pipeline scaffolding",
		CategoryQuality:     "Linting, formatting, test scaffolds",
		CategorySupplyChain: "Dependencies & security",
		CategoryKnowledge:   "Project memory & docs",
		CategoryAgents:      "AI agent integration",
		CategoryRuntime:     "Dev environment & containers",
	}
}

// Valid reports whether c is one of the 9 frozen categories.
// Used at registration time to refuse recipes that target an
// unknown category.
func (c Category) Valid() bool {
	for _, k := range Categories() {
		if k == c {
			return true
		}
	}
	return false
}

// MustValid panics if c isn't a frozen category. Recipe registries
// call this at registration so a bad category surfaces at boot,
// not at the user's first run.
func (c Category) MustValid() {
	if !c.Valid() {
		panic(fmt.Sprintf("setup: unknown category %q (must be one of %v)", c, Categories()))
	}
}
