// Package setuptools — chat-driven Onboard + Init MCP tools.
//
// Today's CLI surface (`clawtool onboard`, `clawtool init`)
// requires an operator at a terminal. When clawtool runs as a
// Claude Code plugin (or any MCP host), the calling AI can't
// drive that flow because there's no MCP tool exposing it.
//
// This package adds three tools so an AI session can drive
// onboarding + init from chat:
//
//   - OnboardStatus  — read-only probe of repo state.
//   - InitApply      — dispatches into the same setup.Apply
//     machinery `clawtool init` uses.
//   - OnboardWizard  — non-interactive subset of `clawtool
//     onboard`: persists telemetry preference + agent-family
//     default + the onboarded marker.
//
// Package name `setuptools` (not `setup`) avoids the clash with
// internal/setup which both files import — the recipe registry
// lives there and we delegate Apply / Detect / Categories to it.
package setuptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// onboardStatusResult is the JSON shape returned by OnboardStatus.
// Snake_case keys to match the project-wide tool-result convention
// (sourceCheck / agentDetect). Field order: marker booleans first,
// per-recipe map next, suggested next action last so a calling
// agent that reads only the suggestion still gets a valid action.
type onboardStatusResult struct {
	Repo                string            `json:"repo"`
	HasClawtoolDir      bool              `json:"has_clawtool_dir"`
	HasClaudeMD         bool              `json:"has_claude_md"`
	OnboardedMarker     bool              `json:"onboarded_marker"`
	RecipeStates        map[string]string `json:"recipe_states"`
	SuggestedNextAction string            `json:"suggested_next_action"`
	ErrorReason         string            `json:"error_reason,omitempty"`
}

// RegisterOnboardStatus wires the OnboardStatus MCP tool to s.
// Called from internal/tools/core/manifest.go via the registry's
// Register fn so the surface-drift test sees the new spec
// automatically.
func RegisterOnboardStatus(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"OnboardStatus",
			mcp.WithDescription(
				"Read-only probe of a repo's clawtool setup state — has the .clawtool dir landed, is CLAUDE.md present, which recipes are applied / partial / absent, and what the calling agent should do next.",
			),
			mcp.WithString("repo",
				mcp.Description("Repo path to probe. Defaults to the server's cwd when empty.")),
		),
		runOnboardStatus,
	)
}

// runOnboardStatus is the handler. Pure read; never writes.
// Uses setup.InCategory + Recipe.Detect (the same machinery
// `clawtool recipe status` exercises) so the verdicts match
// what an interactive operator would see.
func runOnboardStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo := strings.TrimSpace(req.GetString("repo", ""))
	if repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			repo = cwd
		} else {
			repo = "."
		}
	}

	out := onboardStatusResult{
		Repo:         repo,
		RecipeStates: map[string]string{},
	}

	// Marker checks — both repo-local (`.clawtool/`, `CLAUDE.md`)
	// and host-level (`~/.config/clawtool/.onboarded`). The
	// onboarded marker is shared with internal/cli's IsOnboarded;
	// we read the same path via xdg.ConfigDir() rather than
	// importing internal/cli (would form a layering cycle —
	// tools/setup is meant to be a leaf).
	if fi, err := os.Stat(filepath.Join(repo, ".clawtool")); err == nil && fi.IsDir() {
		out.HasClawtoolDir = true
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); err == nil {
		out.HasClaudeMD = true
	}
	if _, err := os.Stat(filepath.Join(xdg.ConfigDir(), ".onboarded")); err == nil {
		out.OnboardedMarker = true
	}

	// Recipe states — walk every category, run Detect on each
	// recipe, record applied / partial / absent / error verbatim.
	// Any Detect() that returns an error renders as "error" in
	// the map; the per-recipe error string is intentionally not
	// surfaced to the chat agent (it's noisy and recipe-specific).
	allAbsent := true
	hasApplied := false
	for _, cat := range setup.Categories() {
		for _, r := range setup.InCategory(cat) {
			m := r.Meta()
			status, _, derr := r.Detect(ctx, repo)
			if derr != nil {
				out.RecipeStates[m.Name] = "error"
				continue
			}
			out.RecipeStates[m.Name] = string(status)
			if status != setup.StatusAbsent {
				allAbsent = false
			}
			if status == setup.StatusApplied {
				hasApplied = true
			}
		}
	}

	// Suggested next action — the whole point of this tool is to
	// keep the calling agent from blindly re-applying. The
	// decision tree, from the operator's perspective:
	//   1. host not onboarded → run OnboardWizard.
	//   2. fresh repo (no recipes applied) → InitApply with
	//      core_only=true; the dry-run preview tells you what.
	//   3. some recipes applied, some absent → InitApply
	//      idempotent re-run; safe.
	//   4. everything applied → nothing to do; surface that to
	//      the operator instead of running anything.
	switch {
	case !out.OnboardedMarker:
		out.SuggestedNextAction = "Call OnboardWizard with non_interactive=true to register clawtool defaults for this host, then InitApply to drop core recipes into the repo."
	case allAbsent:
		out.SuggestedNextAction = "Call InitApply with dry_run=true to preview core defaults, then dry_run=false to apply."
	case !hasApplied:
		out.SuggestedNextAction = "Recipes are partial — call InitApply (idempotent) to reconcile."
	default:
		out.SuggestedNextAction = "Nothing to do — host onboarded and core recipes applied. Use RecipeStatus for a per-recipe drill-down."
	}

	return resultOfJSON("OnboardStatus", out)
}

// resultOfJSON renders a structured-content result + a compact
// JSON pretty-print fallback. Sister of internal/tools/core's
// resultOf/Renderer pattern; ours skips the BaseResult helper
// because these tools don't have a "verb + target" shape — they
// produce a self-describing JSON snapshot.
func resultOfJSON(operation string, payload any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("%s: marshal result: %v", operation, err)), nil
	}
	return mcp.NewToolResultStructured(payload, string(b)), nil
}
