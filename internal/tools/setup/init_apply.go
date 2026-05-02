package setuptools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// initApplyResult is the chat-driven mirror of `clawtool init`'s
// summary panel.
//
// The parallel init-context-summary branch landed cli.InitSummary
// in internal/cli, not internal/setup, with a different shape:
// AppliedRecipes/SkippedRecipes (typed status enum + Generated map)
// vs the four flat Applied/Skipped/Pending/Failed slices this tool
// returns. Swapping wholesale would break the JSON contract agents
// consume from InitApply, and importing internal/cli from here
// would form a cycle (see needsRequiredOptions below). So the two
// shapes stay separate by design — this struct is the agent-facing
// JSON, cli.InitSummary is the operator-facing renderer.
type initApplyResult struct {
	Repo      string         `json:"repo"`
	CoreOnly  bool           `json:"core_only"`
	DryRun    bool           `json:"dry_run"`
	Applied   []recipeAction `json:"applied,omitempty"`
	Skipped   []recipeAction `json:"skipped,omitempty"`
	Pending   []recipeAction `json:"pending,omitempty"`
	Failed    []recipeAction `json:"failed,omitempty"`
	NextSteps []string       `json:"next_steps,omitempty"`
	// PendingActions is the agent-facing key the OnboardStatus
	// → InitApply pipeline relies on: a list of recipe names
	// the agent should consider running next. Mirrors the
	// `pending_actions` field documented in the tool's
	// UsageHint.
	PendingActions []string `json:"pending_actions,omitempty"`
	ErrorReason    string   `json:"error_reason,omitempty"`
}

// recipeAction is one row in the summary. Reason carries a
// short string explaining the verdict — useful for "why is
// this skipped?" follow-ups from the calling agent.
type recipeAction struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Reason   string `json:"reason,omitempty"`
}

// RegisterInitApply wires the InitApply MCP tool to s. Mirror of
// RegisterOnboardStatus.
func RegisterInitApply(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"InitApply",
			mcp.WithDescription(
				"Apply clawtool's project recipes from chat — dispatches into the same setup.Apply machinery `clawtool init` runs. Pass dry_run=true to preview without writes; core_only=true (default) limits the surface to Core recipes (no required-options needed). Idempotent — already-applied recipes report as skipped.",
			),
			mcp.WithBoolean("core_only",
				mcp.Description("When true (default), only Core recipes (RecipeMeta.Core) are considered. When false, every Stable + Core recipe is considered.")),
			mcp.WithString("repo",
				mcp.Description("Repo path. Defaults to the server's cwd when empty.")),
			mcp.WithBoolean("dry_run",
				mcp.Description("When true, render what would apply without writing. Default false.")),
		),
		runInitApply,
	)
}

// runInitApply is the handler. Mirrors internal/cli's
// runInitAll / runInitRepoNonInteractive but folded into a
// single MCP-driven dispatch.
func runInitApply(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Defaults: core_only=true, dry_run=false. The MCP request
	// helper returns the Go zero value when the key is absent —
	// so for core_only we explicitly look for the absent case
	// to distinguish "not set" (→ true) from "set to false"
	// (→ false).
	args := req.GetArguments()
	coreOnly := true
	if v, ok := args["core_only"]; ok {
		if b, ok := v.(bool); ok {
			coreOnly = b
		}
	}
	dryRun := req.GetBool("dry_run", false)
	repo := strings.TrimSpace(req.GetString("repo", ""))
	if repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			repo = cwd
		} else {
			repo = "."
		}
	}

	out := initApplyResult{
		Repo:     repo,
		CoreOnly: coreOnly,
		DryRun:   dryRun,
	}

	// Recipe selection — same predicate `clawtool init` uses.
	//   - core_only=true: RecipeMeta.Core
	//   - core_only=false: Stability == Stable (or unset, which
	//     defaults to Stable per recipe.go's documented zero-
	//     value semantics).
	//
	// `needsRequiredOptions` recipes (license, codeowners) get
	// surfaced as Pending — they need operator input the chat
	// path can't elicit safely.
	candidates := selectRecipes(coreOnly)

	for _, r := range candidates {
		m := r.Meta()
		row := recipeAction{Name: m.Name, Category: string(m.Category)}

		if needsRequiredOptions(m.Name) {
			row.Reason = "needs operator input (run `clawtool init` interactively for this one)"
			out.Pending = append(out.Pending, row)
			out.PendingActions = append(out.PendingActions, m.Name)
			continue
		}

		status, detail, _ := r.Detect(ctx, repo)
		switch status {
		case setup.StatusApplied:
			row.Reason = "already applied"
			if detail != "" {
				row.Reason = "already applied — " + detail
			}
			out.Skipped = append(out.Skipped, row)
			continue
		case setup.StatusPartial:
			// Partial = file exists but isn't clawtool-managed.
			// The interactive wizard prompts before overwrite;
			// the chat path declines and surfaces it as pending.
			row.Reason = "partial state needs explicit overwrite (use `clawtool init` interactively or RecipeApply with options.force=true)"
			out.Pending = append(out.Pending, row)
			out.PendingActions = append(out.PendingActions, m.Name)
			continue
		}

		// status == StatusAbsent (or StatusError) — try to apply.
		if dryRun {
			row.Reason = "would apply"
			out.Applied = append(out.Applied, row)
			continue
		}

		res, err := setup.Apply(ctx, r, setup.ApplyOptions{
			Repo:     repo,
			Prompter: setup.AlwaysSkip{},
		})
		if err != nil {
			if errors.Is(err, setup.ErrSkippedByUser) {
				row.Reason = "skipped: " + res.SkipReason
				out.Skipped = append(out.Skipped, row)
				continue
			}
			row.Reason = err.Error()
			out.Failed = append(out.Failed, row)
			continue
		}
		row.Reason = "applied"
		if !res.VerifyOK && res.VerifyErr != nil {
			row.Reason = "applied (verify failed: " + res.VerifyErr.Error() + ")"
		}
		out.Applied = append(out.Applied, row)
	}

	// Next-steps — guide the agent's NEXT call. Empty when
	// there's nothing left to do.
	if dryRun {
		out.NextSteps = []string{
			"Call InitApply again with dry_run=false to apply.",
		}
	}
	if len(out.Pending) > 0 {
		out.NextSteps = append(out.NextSteps,
			fmt.Sprintf("%d recipe(s) need operator input — surface them to the user before proceeding.", len(out.Pending)),
		)
	}
	if len(out.Failed) > 0 {
		out.NextSteps = append(out.NextSteps,
			"Some recipes failed — inspect the `failed` array's reason fields.",
		)
	}
	if len(out.NextSteps) == 0 && !dryRun {
		out.NextSteps = []string{"Done. Use RecipeStatus for a per-recipe drill-down."}
	}

	return resultOfJSON("InitApply", out)
}

// selectRecipes returns the recipe set InitApply considers, given
// the core_only flag. Centralised so tests can re-use the same
// predicate without duplicating the loop.
func selectRecipes(coreOnly bool) []setup.Recipe {
	var out []setup.Recipe
	for _, cat := range setup.Categories() {
		for _, r := range setup.InCategory(cat) {
			m := r.Meta()
			if coreOnly {
				if !m.Core {
					continue
				}
			} else {
				// Stable + Core. Empty Stability defaults to
				// Stable per recipe.go's contract.
				if m.Stability != setup.StabilityStable && m.Stability != "" {
					continue
				}
			}
			out = append(out, r)
		}
	}
	return out
}

// needsRequiredOptions identifies recipes that won't apply with
// empty Options. Mirrors internal/cli's same-named helper. Kept
// as a private duplicate (not imported) because internal/cli
// imports internal/tools indirectly via the App struct; the
// reverse import would form a cycle.
func needsRequiredOptions(name string) bool {
	switch name {
	case "license", "codeowners":
		return true
	}
	return false
}
