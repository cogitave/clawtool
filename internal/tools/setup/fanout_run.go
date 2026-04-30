package setuptools

// Fanout — chat-driven entry point for clawtool's parallel-subgoal
// orchestrator. CLI verb (`clawtool fanout`) lives in internal/cli;
// this MCP tool exposes the same surface to chat-driven hosts so an
// AI session can split a multi-part goal into N parallel branches
// without dropping to a shell.
//
// Mirrors AutonomousRun's shape: one Register*, one runner, one
// JSON result struct, JSON via resultOfJSON. Reuses the same
// AutonomousDispatcher seam (defaultDispatcher) so a test that
// stubs the dispatcher for AutonomousRun also exercises Fanout —
// stub once, both surfaces covered.
//
// Git operations are stubbed via gitFanoutExec; production wires
// it to exec.CommandContext.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// gitFanoutExec is the seam tests overwrite to bypass real git.
type gitFanoutExec func(ctx context.Context, dir string, args ...string) error

func realFanoutGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// defaultGitExec is the package-level seam tests swap.
var defaultGitExec gitFanoutExec = realFanoutGit

// fanoutSubResult mirrors the CLI verb's per-sub record so the
// JSON wire shape is identical across surfaces. Status is one of:
// merged | failed | timeout | pending | ready.
type fanoutSubResult struct {
	Index        int      `json:"index"`
	Subgoal      string   `json:"subgoal"`
	Branch       string   `json:"branch"`
	WorktreePath string   `json:"worktree_path"`
	Status       string   `json:"status"`
	Iterations   int      `json:"iterations"`
	Done         bool     `json:"done"`
	FilesChanged []string `json:"files_changed,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// fanoutResult is the JSON shape Fanout returns. Mirrors the CLI's
// summary.json so a chat-side caller can compare apples-to-apples.
type fanoutResult struct {
	RunID         string            `json:"run_id"`
	Repo          string            `json:"repo"`
	Agent         string            `json:"agent"`
	Subgoals      []string          `json:"subgoals"`
	MaxConcurrent int               `json:"max_concurrent"`
	Cooldown      int               `json:"cooldown_seconds"`
	MaxIterPerSub int               `json:"max_iterations_per_sub"`
	DryRun        bool              `json:"dry_run"`
	Planned       bool              `json:"planned,omitempty"`
	Subs          []fanoutSubResult `json:"subs,omitempty"`
	Stopped       string            `json:"stopped,omitempty"`
	SummaryPath   string            `json:"summary_path,omitempty"`
	ErrorReason   string            `json:"error_reason,omitempty"`
}

// RegisterFanout wires the Fanout MCP tool to s. Mirror of
// RegisterAutonomousRun; same shape so the manifest entry is a
// minor variant of AutonomousRun's.
func RegisterFanout(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"Fanout",
			mcp.WithDescription(
				"Spawn N parallel subgoals — each in its own git worktree under .clawtool/fanout/wt-N — dispatch each to a BIAM peer as a mini autonomous loop, then sequentially fast-forward-merge each ready sub back into main with a cooldown between merges. Host-agnostic alternative to Claude Code's built-in Agent fan-out.",
			),
			mcp.WithArray("subgoals",
				mcp.Description("Ordered list of independent subgoals; each becomes its own worktree + branch + autonomous loop. Required, ≤8."),
				mcp.Required()),
			mcp.WithString("repo",
				mcp.Description("Repo root. Defaults to the server's cwd when empty.")),
			mcp.WithString("agent",
				mcp.Description("BIAM peer to dispatch to. Default \"claude\".")),
			mcp.WithNumber("max_concurrent",
				mcp.Description("Cap on parallel in-flight subgoals. Default 4.")),
			mcp.WithNumber("cooldown_seconds",
				mcp.Description("Cooldown between sequential ff-merges, in seconds. Default 300 (matches autodev cron pacing).")),
			mcp.WithNumber("max_iterations_per_sub",
				mcp.Description("Per-subgoal autonomous-loop cap. Default 5.")),
			mcp.WithBoolean("dry_run",
				mcp.Description("When true, return the parsed plan + worktree paths as JSON without dispatching or merging. Default false.")),
		),
		runFanout,
	)
}

// runFanout is the handler. Validates args, applies defaults,
// gates on .clawtool/, then either renders a plan (dry_run) or
// drives the real flow.
func runFanout(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	subs, err := parseFanoutSubgoals(req)
	if err != nil {
		return mcp.NewToolResultError("Fanout: " + err.Error()), nil
	}
	if len(subs) > 8 {
		return mcp.NewToolResultError(fmt.Sprintf("Fanout: %d subgoals exceeds default cap of 8", len(subs))), nil
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
	maxConc := int(req.GetFloat("max_concurrent", 4))
	if maxConc <= 0 {
		maxConc = 4
	}
	cooldown := int(req.GetFloat("cooldown_seconds", 300))
	if cooldown < 0 {
		cooldown = 300
	}
	maxIter := int(req.GetFloat("max_iterations_per_sub", 5))
	if maxIter <= 0 {
		maxIter = 5
	}
	dryRun := req.GetBool("dry_run", false)
	if maxConc > len(subs) {
		maxConc = len(subs)
	}

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	out := fanoutResult{
		RunID:         runID,
		Repo:          repo,
		Agent:         agent,
		Subgoals:      subs,
		MaxConcurrent: maxConc,
		Cooldown:      cooldown,
		MaxIterPerSub: maxIter,
		DryRun:        dryRun,
	}

	// Onboard gate. Same as AutonomousRun — the calling agent
	// owns the choice of when to onboard.
	if fi, err := os.Stat(filepath.Join(repo, ".clawtool")); err != nil || !fi.IsDir() {
		out.ErrorReason = "Fanout: repo lacks .clawtool/ — call OnboardWizard then InitApply first, then retry."
		return resultOfJSON("Fanout", out)
	}

	results := make([]fanoutSubResult, len(subs))
	for i, s := range subs {
		results[i] = fanoutSubResult{
			Index:        i + 1,
			Subgoal:      s,
			Branch:       fmt.Sprintf("fanout/%s/sub-%d", runID, i+1),
			WorktreePath: filepath.Join(repo, ".clawtool", "fanout", fmt.Sprintf("wt-%d", i+1)),
			Status:       "pending",
		}
	}

	if dryRun {
		out.Planned = true
		out.Subs = results
		return resultOfJSON("Fanout", out)
	}

	// Live dispatch + merge. Mirrors the CLI's runFanoutLoop.
	runDir := filepath.Join(repo, ".clawtool", "fanout", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		out.ErrorReason = "mkdir run-dir: " + err.Error()
		return resultOfJSON("Fanout", out)
	}

	// Phase 1: create worktrees up front.
	for i := range results {
		if ctx.Err() != nil {
			break
		}
		if err := defaultGitExec(ctx, repo, "worktree", "add", "-b", results[i].Branch, results[i].WorktreePath); err != nil {
			results[i].Status = "failed"
			results[i].Error = "worktree create: " + err.Error()
		}
	}

	// Phase 2: parallel dispatch.
	mergeQueue := make(chan int, len(subs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConc)
	for i := range results {
		if results[i].Status == "failed" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[idx].Status = "timeout"
				results[idx].Error = "cancelled before start"
				return
			}
			defer func() { <-sem }()
			runFanoutSub(ctx, agent, maxIter, &results[idx])
			if results[idx].Status == "ready" {
				mergeQueue <- idx
			}
		}(i)
	}
	go func() { wg.Wait(); close(mergeQueue) }()

	// Phase 3: sequential ff-merge with cooldown.
	mergedCount := 0
	for idx := range mergeQueue {
		if ctx.Err() != nil {
			results[idx].Status = "timeout"
			results[idx].Error = "cancelled before merge"
			break
		}
		if err := defaultGitExec(ctx, repo, "merge", "--ff-only", results[idx].Branch); err != nil {
			results[idx].Status = "failed"
			results[idx].Error = "merge: " + err.Error()
			continue
		}
		if err := defaultGitExec(ctx, repo, "push"); err != nil {
			results[idx].Status = "failed"
			results[idx].Error = "push: " + err.Error()
			continue
		}
		results[idx].Status = "merged"
		mergedCount++
		if mergedCount < len(subs) && cooldown > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(cooldown) * time.Second):
			}
		}
	}

	stopped := "ok"
	if ctx.Err() != nil {
		stopped = "interrupted"
	}
	out.Stopped = stopped
	out.Subs = results

	// Persist summary.json so a follow-up turn can inspect.
	summaryPath := filepath.Join(runDir, "summary.json")
	if b, err := json.MarshalIndent(out, "", "  "); err == nil {
		if werr := os.WriteFile(summaryPath, b, 0o644); werr == nil {
			out.SummaryPath = summaryPath
		}
	}
	return resultOfJSON("Fanout", out)
}

// runFanoutSub drives a single subgoal's mini autonomous loop via
// the shared defaultDispatcher. The dispatcher seam is the SAME
// one AutonomousRun uses, so a test that stubs it once covers
// both tools.
func runFanoutSub(ctx context.Context, agent string, maxIter int, r *fanoutSubResult) {
	disp := defaultDispatcher
	for iter := 1; iter <= maxIter; iter++ {
		if ctx.Err() != nil {
			r.Status = "timeout"
			r.Error = "cancelled mid-loop"
			return
		}
		_ = agent // dispatcher is configured per-instance; agent name is recorded in the result for the operator's audit
		prompt := fmt.Sprintf("fanout subgoal %q — iteration %d of %d", r.Subgoal, iter, maxIter)
		tick, err := disp.Dispatch(ctx, prompt)
		r.Iterations = iter
		if err != nil {
			r.Status = "failed"
			r.Error = fmt.Sprintf("iter %d: %v", iter, err)
			return
		}
		for _, f := range tick.FilesChanged {
			seen := false
			for _, e := range r.FilesChanged {
				if e == f {
					seen = true
					break
				}
			}
			if !seen {
				r.FilesChanged = append(r.FilesChanged, f)
			}
		}
		if tick.Done {
			r.Done = true
			r.Status = "ready"
			return
		}
	}
	// Hit max-iterations without DONE — still mark ready so the
	// merge phase tries to land partial work; Done=false flags it.
	r.Status = "ready"
}

// parseFanoutSubgoals pulls subgoals[] off the request. Accepts
// []any (mcp-go's default), []string, or a single string (single
// subgoal convenience). Empty / non-array → typed error.
func parseFanoutSubgoals(req mcp.CallToolRequest) ([]string, error) {
	v, ok := req.GetArguments()["subgoals"]
	if !ok {
		return nil, fmt.Errorf("subgoals is required")
	}
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("subgoals is empty — pass at least one")
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("subgoals is empty — pass at least one")
		}
		return out, nil
	case string:
		if s := strings.TrimSpace(x); s != "" {
			return []string{s}, nil
		}
		return nil, fmt.Errorf("subgoals is empty — pass at least one")
	default:
		return nil, fmt.Errorf("subgoals must be an array of strings; got %T", v)
	}
}
