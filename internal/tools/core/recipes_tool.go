// Package core — Recipe* MCP tools mirror the `clawtool recipe …`
// CLI surface so a model can run setup tasks from inside a
// conversation. Same registry, same Detect/Apply/Verify cycle.
//
// Three tools per ADR-013:
//
//	RecipeList   — enumerate recipes, optionally filter by category.
//	RecipeStatus — Detect output for one recipe or all of them.
//	RecipeApply  — full Detect→Prereqs→Apply→Verify against a repo,
//	               with structured options.
//
// All three return BaseResult-shaped output (pretty text + structured
// JSON) so chat UIs render them the same way as Bash/Read/etc.
package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ── shapes ─────────────────────────────────────────────────────────

type recipeListResult struct {
	BaseResult
	Category string       `json:"category,omitempty"`
	Recipes  []recipeInfo `json:"recipes"`
}

type recipeInfo struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Upstream    string `json:"upstream"`
	Stability   string `json:"stability"`
	Status      string `json:"status,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

func (r recipeListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	if r.Category != "" {
		fmt.Fprintf(&b, "%d recipe(s) in [%s]\n\n", len(r.Recipes), r.Category)
	} else {
		fmt.Fprintf(&b, "%d recipe(s) across %d categories\n\n", len(r.Recipes), len(setup.Categories()))
	}
	current := ""
	for _, ri := range r.Recipes {
		if ri.Category != current {
			current = ri.Category
			fmt.Fprintf(&b, "[%s] — %s\n", current, setup.CategoryDescriptions()[setup.Category(current)])
		}
		fmt.Fprintf(&b, "  %-26s %-10s %s\n", ri.Name, ri.Status, ri.Description)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type recipeStatusResult struct {
	BaseResult
	Repo    string       `json:"repo"`
	Recipes []recipeInfo `json:"recipes"`
}

func (r recipeStatusResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Repo)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "recipe status for %s\n\n", r.Repo)
	current := ""
	for _, ri := range r.Recipes {
		if ri.Category != current {
			current = ri.Category
			fmt.Fprintf(&b, "[%s]\n", current)
		}
		fmt.Fprintf(&b, "  %-26s %s — %s\n", ri.Name, ri.Status, ri.Detail)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type recipeApplyResult struct {
	BaseResult
	Recipe       string   `json:"recipe"`
	RecipeCat    string   `json:"category"`
	Repo         string   `json:"repo"`
	Skipped      bool     `json:"skipped,omitempty"`
	SkipReason   string   `json:"skip_reason,omitempty"`
	Installed    []string `json:"installed_prereqs,omitempty"`
	ManualHints  []string `json:"manual_prereqs,omitempty"`
	UpstreamUsed string   `json:"upstream_used,omitempty"`
	VerifyOK     bool     `json:"verify_ok"`
	VerifyError  string   `json:"verify_error,omitempty"`
}

func (r recipeApplyResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Recipe)
	}
	if r.Skipped {
		return fmt.Sprintf("↷ skipped %s — %s", r.Recipe, r.SkipReason)
	}
	verb := "applied"
	if !r.VerifyOK {
		verb = "applied (verify failed)"
	}
	extras := []string{r.RecipeCat}
	if !r.VerifyOK {
		extras = append(extras, "verify: "+r.VerifyError)
	}
	for _, h := range r.ManualHints {
		extras = append(extras, "manual prereq: "+h)
	}
	for _, i := range r.Installed {
		extras = append(extras, "installed: "+i)
	}
	return r.SuccessLine(verb+" "+r.Recipe, extras...)
}

// ── registration ───────────────────────────────────────────────────

// RegisterRecipeTools adds the three Recipe* MCP tools to s. The
// registry must already be populated (typically via blank import of
// internal/setup/recipes).
func RegisterRecipeTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"RecipeList",
			mcp.WithDescription(
				"Browse the clawtool init-recipe catalog with each recipe's "+
					"current state in a target repo. Use when the operator "+
					"asks \"what recipes do you have?\" or before RecipeApply "+
					"to discover available recipe names. Recipes are organized "+
					"into 9 fixed categories (governance, commits, release, "+
					"ci, quality, supply-chain, knowledge, agents, runtime). "+
					"NOT for inspecting one recipe's status only — use "+
					"RecipeStatus; NOT for installing — use RecipeApply. "+
					"Read-only.",
			),
			mcp.WithString("category",
				mcp.Description("Filter to one of the 9 categories. Empty = all.")),
			mcp.WithString("repo",
				mcp.Description("Repo path used for Detect status. Defaults to $HOME if empty.")),
		),
		runRecipeList,
	)

	s.AddTool(
		mcp.NewTool(
			"RecipeStatus",
			mcp.WithDescription(
				"Probe Detect status for one recipe (or every recipe) against "+
					"a repo without running the install. Use to verify a recipe "+
					"is `applied` in this repo, audit which recipes are still "+
					"`absent`/`partial`, or before RecipeApply to confirm the "+
					"recipe needs work. Status values: applied | partial | "+
					"absent | error. NOT for browsing the catalog with "+
					"category grouping — use RecipeList; NOT for installing — "+
					"use RecipeApply. Read-only.",
			),
			mcp.WithString("name",
				mcp.Description("Recipe name. Empty = report all recipes.")),
			mcp.WithString("repo",
				mcp.Description("Repo path. Defaults to $HOME if empty.")),
		),
		runRecipeStatus,
	)

	s.AddTool(
		mcp.NewTool(
			"RecipeApply",
			mcp.WithDescription(
				"Apply a recipe to a repo. Runs the full Detect→Prereqs→Apply→"+
					"Verify cycle with auto-skip on missing prereqs (the wizard "+
					"handles install prompts; the MCP path stays safe by default). "+
					"Pass a JSON `options` object for recipe-specific knobs: "+
					"license needs `holder` and optional `spdx`; codeowners "+
					"needs `owners` (a string array of GitHub handles).",
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Recipe name (run RecipeList to discover).")),
			mcp.WithString("repo",
				mcp.Description("Repo path. Defaults to $HOME if empty.")),
			mcp.WithObject("options",
				mcp.Description("Recipe-specific options. See each recipe's docs.")),
		),
		runRecipeApply,
	)
}

// ── handlers ───────────────────────────────────────────────────────

func runRecipeList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	category := strings.TrimSpace(req.GetString("category", ""))
	repo := req.GetString("repo", "")
	if repo == "" {
		repo = homeDir()
	}

	out := recipeListResult{
		BaseResult: BaseResult{Operation: "RecipeList"},
		Category:   category,
	}
	var recipes []setup.Recipe
	if category != "" {
		cat := setup.Category(category)
		if !cat.Valid() {
			out.ErrorReason = fmt.Sprintf("unknown category %q", category)
			return resultOf(out), nil
		}
		recipes = setup.InCategory(cat)
	} else {
		for _, c := range setup.Categories() {
			recipes = append(recipes, setup.InCategory(c)...)
		}
	}
	for _, r := range recipes {
		m := r.Meta()
		status, detail, _ := r.Detect(context.Background(), repo)
		out.Recipes = append(out.Recipes, recipeInfo{
			Name:        m.Name,
			Category:    string(m.Category),
			Description: m.Description,
			Upstream:    m.Upstream,
			Stability:   string(m.Stability),
			Status:      string(status),
			Detail:      detail,
		})
	}
	return resultOf(out), nil
}

func runRecipeStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := strings.TrimSpace(req.GetString("name", ""))
	repo := req.GetString("repo", "")
	if repo == "" {
		repo = homeDir()
	}

	out := recipeStatusResult{
		BaseResult: BaseResult{Operation: "RecipeStatus"},
		Repo:       repo,
	}
	var recipes []setup.Recipe
	if name != "" {
		r := setup.Lookup(name)
		if r == nil {
			out.ErrorReason = fmt.Sprintf("unknown recipe %q (call RecipeList to discover)", name)
			return resultOf(out), nil
		}
		recipes = []setup.Recipe{r}
	} else {
		for _, c := range setup.Categories() {
			recipes = append(recipes, setup.InCategory(c)...)
		}
	}
	for _, r := range recipes {
		m := r.Meta()
		status, detail, _ := r.Detect(context.Background(), repo)
		out.Recipes = append(out.Recipes, recipeInfo{
			Name:     m.Name,
			Category: string(m.Category),
			Status:   string(status),
			Detail:   detail,
		})
	}
	return resultOf(out), nil
}

func runRecipeApply(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: name"), nil
	}
	repo := req.GetString("repo", "")
	if repo == "" {
		repo = homeDir()
	}

	out := recipeApplyResult{
		BaseResult: BaseResult{Operation: "RecipeApply"},
		Recipe:     name,
		Repo:       repo,
	}

	r := setup.Lookup(name)
	if r == nil {
		out.ErrorReason = fmt.Sprintf("unknown recipe %q (call RecipeList to discover)", name)
		return resultOf(out), nil
	}
	out.RecipeCat = string(r.Meta().Category)
	out.UpstreamUsed = r.Meta().Upstream

	opts := setup.Options{}
	if rawOpts := req.GetArguments()["options"]; rawOpts != nil {
		if m, ok := rawOpts.(map[string]any); ok {
			opts = setup.Options(m)
		}
	}

	res, applyErr := setup.Apply(ctx, r, setup.ApplyOptions{
		Repo:          repo,
		RecipeOptions: opts,
		Prompter:      setup.AlwaysSkip{}, // MCP path is non-interactive — wizard handles install prompts
	})
	out.Skipped = res.Skipped
	out.SkipReason = res.SkipReason
	out.Installed = append(out.Installed, res.Installed...)
	out.ManualHints = append(out.ManualHints, res.ManualHints...)
	out.VerifyOK = res.VerifyOK
	if res.VerifyErr != nil {
		out.VerifyError = res.VerifyErr.Error()
	}
	if applyErr != nil && !res.Skipped {
		out.ErrorReason = applyErr.Error()
	}
	return resultOf(out), nil
}
