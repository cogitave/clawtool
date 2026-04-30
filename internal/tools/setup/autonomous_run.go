package setuptools

// AutonomousRun — chat-driven entry point for clawtool's
// self-paced autonomous dev loop.
//
// The CLI verb (`clawtool autonomous "<goal>"`) lives on a
// parallel branch (autodev/autonomous-mode) and exposes the
// same loop to a terminal operator. This MCP tool surfaces it
// to whichever AI session is talking to clawtool — Claude Code,
// Codex, Cursor, custom agents — so the operator never has to
// drop down to a shell to kick off a multi-step build.
//
// Shape mirrors the chat-driven Onboard + Init bundle next door
// (onboard_status.go, init_apply.go, onboard_wizard.go): one
// Register* fn, one runner, one JSON result struct, JSON via
// resultOfJSON. The local AutonomousDispatcher interface is the
// seam tests stub against; when this branch rebases on top of
// autodev/autonomous-mode, the canonical Dispatcher there will
// take its place — field/method names match by design so the
// merge is mechanical.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AutonomousTick is the per-iteration result a dispatcher returns.
// Field names track the parallel CLI branch's tick struct so the
// rebase is mechanical (`{ Summary, FilesChanged, NextSteps,
// Done }`). Don't rename — the wire format is shared.
type AutonomousTick struct {
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed,omitempty"`
	NextSteps    []string `json:"next_steps,omitempty"`
	Done         bool     `json:"done"`
}

// AutonomousDispatcher is the seam runAutonomousRun closes over.
// One method, exactly the shape the parallel CLI branch's
// Dispatcher uses, so the merge swaps the local definition for
// the canonical one without touching call sites or tests.
type AutonomousDispatcher interface {
	Dispatch(ctx context.Context, prompt string) (AutonomousTick, error)
}

// defaultDispatcher is the package-level seam tests overwrite.
// Same pattern init_apply_test.go uses for its own stub points.
// Production wiring will replace this with the BIAM peer driver
// from the parallel branch (`internal/autonomous`); for now it's
// a stub that refuses to dispatch — the chat path errors loudly
// rather than silently no-op'ing.
var defaultDispatcher AutonomousDispatcher = stubDispatcher{}

type stubDispatcher struct{}

func (stubDispatcher) Dispatch(_ context.Context, _ string) (AutonomousTick, error) {
	return AutonomousTick{}, fmt.Errorf("autonomous: no dispatcher wired (parallel branch autodev/autonomous-mode owns the BIAM driver — chat path returns this error until rebase lands)")
}

// autonomousRunResult is the JSON shape AutonomousRun returns.
// `final_json_path` mirrors the CLI verb's tick.json convention:
// the loop writes a `final.json` summary at the end of the run,
// and we report its path so the calling agent can read it back
// in a follow-up turn (or ignore it; the Summary string is
// already inline).
type autonomousRunResult struct {
	Goal           string           `json:"goal"`
	Repo           string           `json:"repo"`
	Agent          string           `json:"agent"`
	MaxIterations  int              `json:"max_iterations"`
	Cooldown       int              `json:"cooldown_seconds"`
	CoreOnly       bool             `json:"core_only"`
	DryRun         bool             `json:"dry_run"`
	Planned        bool             `json:"planned,omitempty"`
	PlannedPrompts []string         `json:"planned_prompts,omitempty"`
	Done           bool             `json:"done"`
	IterationsRun  int              `json:"iterations_run"`
	FilesChanged   []string         `json:"files_changed,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	FinalJSONPath  string           `json:"final_json_path,omitempty"`
	Ticks          []AutonomousTick `json:"ticks,omitempty"`
	ErrorReason    string           `json:"error_reason,omitempty"`
}

// RegisterAutonomousRun wires the AutonomousRun MCP tool to s.
// Mirror of RegisterOnboardStatus / RegisterInitApply.
func RegisterAutonomousRun(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"AutonomousRun",
			mcp.WithDescription(
				"Drive clawtool's autonomous self-paced dev loop from chat: dispatch a goal to a BIAM peer, iterate until done or max-iterations, return the final summary. Loop runs inside clawtool's binary — host-agnostic.",
			),
			mcp.WithString("goal",
				mcp.Description("The operator's intent — a multi-step request like \"build X\" or \"refactor Y\"."),
				mcp.Required()),
			mcp.WithString("repo",
				mcp.Description("Repo to drive the loop in. Defaults to the server's cwd when empty.")),
			mcp.WithString("agent",
				mcp.Description("BIAM peer to drive. Default \"claude\".")),
			mcp.WithNumber("max_iterations",
				mcp.Description("Hard cap on iteration count. Default 10.")),
			mcp.WithNumber("cooldown_seconds",
				mcp.Description("Cooldown between iterations, in seconds. Default 300.")),
			mcp.WithBoolean("dry_run",
				mcp.Description("When true, return the planned dispatch sequence as JSON without dispatching. Default false.")),
			mcp.WithBoolean("core_only",
				mcp.Description("Pass through to InitApply if onboarding gap detected. Default true.")),
		),
		runAutonomousRun,
	)
}

// runAutonomousRun is the handler. Validates args, applies
// defaults, gates on `.clawtool/`, then either renders a plan
// (dry_run) or drives the loop through defaultDispatcher.
func runAutonomousRun(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	goal := strings.TrimSpace(req.GetString("goal", ""))
	if goal == "" {
		return mcp.NewToolResultError("AutonomousRun: goal is required — pass the operator's intent (e.g. \"build the foo plugin\")."), nil
	}

	repo := strings.TrimSpace(req.GetString("repo", ""))
	if repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			repo = cwd
		} else {
			repo = "."
		}
	}

	agent := strings.TrimSpace(req.GetString("agent", ""))
	if agent == "" {
		agent = "claude"
	}

	maxIter := int(req.GetFloat("max_iterations", 10))
	if maxIter <= 0 {
		maxIter = 10
	}
	cooldown := int(req.GetFloat("cooldown_seconds", 300))
	if cooldown < 0 {
		cooldown = 300
	}
	dryRun := req.GetBool("dry_run", false)

	// core_only defaults to true (see init_apply.go's same
	// idiom): the request helper returns Go-zero on absent, so
	// we look at the args map directly.
	coreOnly := true
	if v, ok := req.GetArguments()["core_only"]; ok {
		if b, ok := v.(bool); ok {
			coreOnly = b
		}
	}

	out := autonomousRunResult{
		Goal:          goal,
		Repo:          repo,
		Agent:         agent,
		MaxIterations: maxIter,
		Cooldown:      cooldown,
		CoreOnly:      coreOnly,
		DryRun:        dryRun,
	}

	// Onboarding gate. AutonomousRun does NOT auto-onboard —
	// the calling agent owns that decision (OnboardWizard +
	// InitApply are separate tools for a reason). We surface a
	// structured error pointing at them.
	if fi, err := os.Stat(filepath.Join(repo, ".clawtool")); err != nil || !fi.IsDir() {
		out.ErrorReason = "AutonomousRun: repo lacks .clawtool/ — call OnboardWizard then InitApply first, then retry. Auto-onboarding from this tool is intentionally blocked: the calling agent owns the choice."
		return resultOfJSON("AutonomousRun", out)
	}

	// Dry-run path: emit the plan, return.
	if dryRun {
		out.Planned = true
		out.PlannedPrompts = planPrompts(goal, maxIter)
		out.Summary = fmt.Sprintf("Would dispatch goal %q to agent %q for up to %d iterations (cooldown %ds).", goal, agent, maxIter, cooldown)
		return resultOfJSON("AutonomousRun", out)
	}

	// Live dispatch loop.
	disp := defaultDispatcher
	for i := 0; i < maxIter; i++ {
		prompt := iterationPrompt(goal, i)
		tick, err := disp.Dispatch(ctx, prompt)
		out.IterationsRun = i + 1
		if err != nil {
			out.ErrorReason = fmt.Sprintf("dispatch iteration %d: %v", i+1, err)
			break
		}
		out.Ticks = append(out.Ticks, tick)
		out.FilesChanged = appendUnique(out.FilesChanged, tick.FilesChanged...)
		out.Summary = tick.Summary
		if tick.Done {
			out.Done = true
			break
		}
		// Cooldown between iterations. Skip on the last one to
		// avoid wasting wall-clock time. ctx-aware so a host
		// cancel terminates the loop promptly.
		if i+1 < maxIter && cooldown > 0 {
			select {
			case <-ctx.Done():
				out.ErrorReason = ctx.Err().Error()
				return resultOfJSON("AutonomousRun", out)
			case <-time.After(time.Duration(cooldown) * time.Second):
			}
		}
	}

	// Persist final.json so a follow-up turn can resume / inspect.
	// Best-effort: failure to write the snapshot is not fatal —
	// the inline summary is already in the response.
	finalPath := filepath.Join(repo, ".clawtool", "autonomous", "final.json")
	if err := writeFinalJSON(finalPath, out); err == nil {
		out.FinalJSONPath = finalPath
	}

	return resultOfJSON("AutonomousRun", out)
}

// planPrompts returns the per-iteration prompt sequence the
// dry-run echoes back. Same shape iterationPrompt produces at
// run time so the operator's preview matches reality.
func planPrompts(goal string, maxIter int) []string {
	out := make([]string, 0, maxIter)
	for i := 0; i < maxIter; i++ {
		out = append(out, iterationPrompt(goal, i))
	}
	return out
}

// iterationPrompt is the prompt rendered to the BIAM peer at
// iteration i. Kept trivial here; the parallel CLI branch
// owns the canonical template — once it lands, this helper is
// replaced wholesale during rebase.
func iterationPrompt(goal string, i int) string {
	if i == 0 {
		return fmt.Sprintf("Iteration 1: %s. Plan + start.", goal)
	}
	return fmt.Sprintf("Iteration %d: continue. Goal: %s.", i+1, goal)
}

// appendUnique appends items to dst skipping duplicates. Tiny —
// inlined rather than pulling in a slice util that doesn't exist
// yet in this package.
func appendUnique(dst []string, items ...string) []string {
	for _, it := range items {
		seen := false
		for _, e := range dst {
			if e == it {
				seen = true
				break
			}
		}
		if !seen {
			dst = append(dst, it)
		}
	}
	return dst
}

// writeFinalJSON serialises the run result to path, creating
// parent dirs as needed. Same atomic-ish pattern the rest of
// the package uses; failure is non-fatal at the call site.
func writeFinalJSON(path string, payload autonomousRunResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
