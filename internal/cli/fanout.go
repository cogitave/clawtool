// Package cli — `clawtool fanout` subcommand. Parallel-subgoal
// orchestrator: spawn N subgoals, each in its own git worktree,
// dispatch each to a BIAM peer in parallel (mini-autonomous loop),
// then sequentially fast-forward-merge each completed subgoal back
// into the main branch with a cooldown between merges.
//
// Reuses autonomous.go's AutonomousDispatcher + Tick types — every
// subgoal IS an autonomous run with its own per-sub max-iterations
// cap. Tests stub the dispatcher via SetAutonomousDispatcher and
// the git ops via SetFanoutGitExec.
//
// Why this exists: today this orchestration is done by Claude
// Code's built-in Agent tool, which is host-coupled. clawtool needs
// its own primitive so any agent host (or bare terminal) can drive
// parallel sub-processes.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const fanoutUsage = `Usage:
  clawtool fanout "<sub-1> ;; <sub-2> ;; <sub-3>" [flags]
  clawtool fanout --plan <plan.json>             [flags]

  Spawn N subgoals in parallel — each in its own git worktree on
  branch fanout/<run-id>/sub-N — dispatch each to a BIAM peer as a
  mini autonomous loop, then sequentially fast-forward-merge each
  completed sub back into main with --cooldown between merges.

  Plan source is mutually exclusive: pass either the positional
  semicolon-pair-separated string OR --plan <plan.json>. The JSON
  shape is {"subgoals": ["...","...",...]}.

Flags:
  --plan <path>            Read subgoals from JSON. Mutually exclusive
                           with the positional plan arg.
  --agent <instance>       BIAM peer to dispatch to. Default: claude.
  --max-concurrent <N>     Cap on parallel in-flight subgoals.
                           Default: 4. Max: 8 (default cap).
  --cooldown <duration>    Sleep between sequential ff-merges (e.g.
                           5m, 30s, 0s). Default: 5m. Tests pass 0s.
  --workdir <path>         Repo root. Worktrees land under
                           <workdir>/.clawtool/fanout/wt-N. Default cwd.
  --max-iterations-per-sub <N>
                           Per-subgoal autonomous-loop cap. Default 5.
  --dry-run                Print the parsed plan + worktree paths
                           and exit without dispatching or merging.

Hint: pair with OnboardStatus + InitApply if the repo isn't
initialized — fanout requires .clawtool/ to exist.
`

// fanoutPlanFile is the JSON shape --plan reads.
type fanoutPlanFile struct {
	Subgoals []string `json:"subgoals"`
}

// fanoutSubResult is one subgoal's terminal state in summary.json.
// Status one of: merged | failed | timeout | pending | skipped.
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

// fanoutSummary is the JSON written to summary.json.
type fanoutSummary struct {
	RunID      string            `json:"run_id"`
	Goal       []string          `json:"goal"`
	Agent      string            `json:"agent"`
	Cooldown   string            `json:"cooldown"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Subs       []fanoutSubResult `json:"subs"`
	Stopped    string            `json:"stopped"` // ok | interrupted
}

// fanoutGitExec is the seam tests stub to bypass real git.
// Production: thin wrapper around exec.CommandContext.
type fanoutGitExec func(ctx context.Context, dir string, args ...string) error

func realGitExec(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

var defaultFanoutGit fanoutGitExec = realGitExec

// SetFanoutGitExec installs a stub and returns the prior one.
func SetFanoutGitExec(g fanoutGitExec) fanoutGitExec {
	prev := defaultFanoutGit
	defaultFanoutGit = g
	return prev
}

type fanoutArgs struct {
	planInline  string
	planPath    string
	agent       string
	maxConc     int
	cooldown    time.Duration
	workdir     string
	maxIterPer  int
	dryRun      bool
	maxSubgoals int
}

func parseFanoutArgs(argv []string) (fanoutArgs, error) {
	out := fanoutArgs{
		agent:       "claude",
		maxConc:     4,
		cooldown:    5 * time.Minute,
		maxIterPer:  5,
		maxSubgoals: 8,
	}
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--plan", "--agent", "--workdir":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("%s requires a value", v)
			}
			switch v {
			case "--plan":
				out.planPath = argv[i+1]
			case "--agent":
				out.agent = argv[i+1]
			case "--workdir":
				out.workdir = argv[i+1]
			}
			i++
		case "--max-concurrent", "--max-iterations-per-sub":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("%s requires a value", v)
			}
			n, err := strconv.Atoi(argv[i+1])
			if err != nil || n <= 0 {
				return out, fmt.Errorf("%s: %q is not a positive int", v, argv[i+1])
			}
			if v == "--max-concurrent" {
				out.maxConc = n
			} else {
				out.maxIterPer = n
			}
			i++
		case "--cooldown":
			if i+1 >= len(argv) {
				return out, errors.New("--cooldown requires a value")
			}
			d, err := time.ParseDuration(argv[i+1])
			if err != nil {
				return out, fmt.Errorf("--cooldown: %w", err)
			}
			out.cooldown = d
			i++
		case "--dry-run":
			out.dryRun = true
		case "--help", "-h":
			return out, errors.New("help requested")
		default:
			if strings.HasPrefix(v, "-") {
				return out, fmt.Errorf("unknown flag %q", v)
			}
			if out.planInline == "" {
				out.planInline = v
			} else {
				out.planInline += " " + v
			}
		}
	}
	return out, nil
}

// loadFanoutPlan returns the parsed subgoals. Inline + --plan are
// mutually exclusive; an empty plan is rejected.
func loadFanoutPlan(args fanoutArgs) ([]string, error) {
	if args.planInline != "" && args.planPath != "" {
		return nil, errors.New("positional plan and --plan are mutually exclusive")
	}
	var subs []string
	if args.planPath != "" {
		b, err := os.ReadFile(args.planPath)
		if err != nil {
			return nil, fmt.Errorf("read --plan: %w", err)
		}
		var f fanoutPlanFile
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("parse --plan %s: %w", args.planPath, err)
		}
		subs = f.Subgoals
	} else {
		for _, p := range strings.Split(args.planInline, ";;") {
			if s := strings.TrimSpace(p); s != "" {
				subs = append(subs, s)
			}
		}
	}
	if len(subs) == 0 {
		return nil, errors.New("plan is empty — pass at least one subgoal")
	}
	if len(subs) > args.maxSubgoals {
		return nil, fmt.Errorf("plan has %d subgoals; default cap is %d", len(subs), args.maxSubgoals)
	}
	return subs, nil
}

func (a *App) runFanout(argv []string) int {
	args, err := parseFanoutArgs(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool fanout: %v\n\n%s", err, fanoutUsage)
		return 2
	}
	subs, err := loadFanoutPlan(args)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool fanout: %v\n\n%s", err, fanoutUsage)
		return 2
	}
	if args.workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool fanout: getwd: %v\n", err)
			return 1
		}
		args.workdir = wd
	}
	// Onboard guardrail — same as autonomous.go.
	if _, err := os.Stat(filepath.Join(args.workdir, ".clawtool")); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(a.Stderr, "clawtool fanout: %q is not onboarded (no .clawtool/ directory)\n", args.workdir)
		fmt.Fprintln(a.Stderr, "  run `clawtool onboard` (or call OnboardStatus + InitApply via MCP) first.")
		return 1
	}
	if args.maxConc > len(subs) {
		args.maxConc = len(subs)
	}

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	if args.dryRun {
		return a.printFanoutPlan(args, subs, runID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintln(a.Stderr, "clawtool fanout: interrupt received, cancelling in-flight subgoals")
			cancel()
		case <-ctx.Done():
		}
	}()
	return a.runFanoutLoop(ctx, args, subs, runID)
}

func (a *App) printFanoutPlan(args fanoutArgs, subs []string, runID string) int {
	fmt.Fprintln(a.Stdout, "clawtool fanout — dry-run plan")
	fmt.Fprintf(a.Stdout, "  run-id:                  %s\n", runID)
	fmt.Fprintf(a.Stdout, "  agent:                   %s\n", args.agent)
	fmt.Fprintf(a.Stdout, "  max-concurrent:          %d\n", args.maxConc)
	fmt.Fprintf(a.Stdout, "  cooldown:                %s\n", args.cooldown)
	fmt.Fprintf(a.Stdout, "  workdir:                 %s\n", args.workdir)
	fmt.Fprintf(a.Stdout, "  max-iterations-per-sub:  %d\n", args.maxIterPer)
	fmt.Fprintf(a.Stdout, "  subgoals (%d):\n", len(subs))
	for i, s := range subs {
		fmt.Fprintf(a.Stdout, "    [%d] %s\n", i+1, s)
		fmt.Fprintf(a.Stdout, "        worktree: %s\n", filepath.Join(args.workdir, ".clawtool", "fanout", fmt.Sprintf("wt-%d", i+1)))
		fmt.Fprintf(a.Stdout, "        branch:   fanout/%s/sub-%d\n", runID, i+1)
	}
	return 0
}

// runFanoutLoop is the live orchestrator. Three phases:
//  1. set up the run dir + per-sub worktrees
//  2. parallel dispatch (semaphore-bounded goroutines), each running
//     a mini autonomous loop of at most maxIterPer iterations
//  3. sequential ff-merge of completed subs in COMPLETION order,
//     with cooldown between merges
//
// Ctrl-C cancels in-flight subs, partial summary written, unstarted
// worktrees torn down.
func (a *App) runFanoutLoop(ctx context.Context, args fanoutArgs, subs []string, runID string) int {
	runDir := filepath.Join(args.workdir, ".clawtool", "fanout", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool fanout: mkdir run-dir: %v\n", err)
		return 1
	}

	results := make([]fanoutSubResult, len(subs))
	for i, s := range subs {
		results[i] = fanoutSubResult{
			Index:        i + 1,
			Subgoal:      s,
			Branch:       fmt.Sprintf("fanout/%s/sub-%d", runID, i+1),
			WorktreePath: filepath.Join(args.workdir, ".clawtool", "fanout", fmt.Sprintf("wt-%d", i+1)),
			Status:       "pending",
		}
	}

	startedAt := time.Now().UTC()
	fmt.Fprintf(a.Stdout, "clawtool fanout: run %s — %d subgoal(s), max-concurrent=%d, cooldown=%s\n",
		runID, len(subs), args.maxConc, args.cooldown)

	// Phase 1: create worktrees up front. If any worktree create
	// fails we tear down what landed and bail — partial worktree
	// graphs confuse `git worktree list`.
	for i := range results {
		if ctx.Err() != nil {
			break
		}
		if err := defaultFanoutGit(ctx, args.workdir, "worktree", "add", "-b", results[i].Branch, results[i].WorktreePath); err != nil {
			results[i].Status = "failed"
			results[i].Error = "worktree create: " + err.Error()
			fmt.Fprintf(a.Stderr, "  sub-%d worktree failed: %v\n", i+1, err)
		}
	}

	// Phase 2: parallel dispatch. mergeQueue carries indices in the
	// order subs *complete*, so the merge phase replays completion
	// order — not plan order. Buffered to len(subs) so emitters
	// never block.
	mergeQueue := make(chan int, len(subs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, args.maxConc)

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
			a.runFanoutSub(ctx, args, &results[idx])
			if results[idx].Status == "ready" {
				mergeQueue <- idx
			}
		}(i)
	}
	go func() { wg.Wait(); close(mergeQueue) }()

	// Phase 3: sequential ff-merge with cooldown. We drain the
	// merge queue in arrival (completion) order. ctx.Err() bails
	// us out cleanly so the partial summary still lands.
	mergedCount := 0
	for idx := range mergeQueue {
		if ctx.Err() != nil {
			results[idx].Status = "timeout"
			results[idx].Error = "cancelled before merge"
			break
		}
		fmt.Fprintf(a.Stdout, "  sub-%d ready → ff-merge into main\n", idx+1)
		if err := defaultFanoutGit(ctx, args.workdir, "merge", "--ff-only", results[idx].Branch); err != nil {
			results[idx].Status = "failed"
			results[idx].Error = "merge: " + err.Error()
			fmt.Fprintf(a.Stderr, "  sub-%d merge failed: %v\n", idx+1, err)
			continue
		}
		if err := defaultFanoutGit(ctx, args.workdir, "push"); err != nil {
			results[idx].Status = "failed"
			results[idx].Error = "push: " + err.Error()
			fmt.Fprintf(a.Stderr, "  sub-%d push failed: %v\n", idx+1, err)
			continue
		}
		results[idx].Status = "merged"
		mergedCount++
		fmt.Fprintf(a.Stdout, "  sub-%d merged + pushed (%d/%d done)\n", idx+1, mergedCount, len(subs))
		// Cooldown between merges; honors the autodev 5-min memory.
		// Skipped after the last merge or on interrupt.
		if mergedCount < len(subs) && args.cooldown > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(args.cooldown):
			}
		}
	}

	stopped := "ok"
	if ctx.Err() != nil {
		stopped = "interrupted"
		// Tear down any worktrees that never started so the next
		// run isn't blocked by stale `git worktree list` entries.
		for i := range results {
			if results[i].Status == "pending" || results[i].Status == "timeout" {
				_ = defaultFanoutGit(context.Background(), args.workdir, "worktree", "remove", "--force", results[i].WorktreePath)
			}
		}
	}

	summary := fanoutSummary{
		RunID:      runID,
		Goal:       subs,
		Agent:      args.agent,
		Cooldown:   args.cooldown.String(),
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		Subs:       results,
		Stopped:    stopped,
	}
	if err := writeFanoutSummary(runDir, summary); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool fanout: write summary: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "clawtool fanout: stopped (%s); summary at %s\n",
		stopped, filepath.Join(runDir, "summary.json"))
	if stopped != "ok" || mergedCount != len(subs) {
		return 1
	}
	return 0
}

// runFanoutSub drives a single subgoal's mini autonomous loop.
// On success sets Status="ready" so the merge phase will pick it up;
// on failure / timeout sets Status accordingly + Error string.
func (a *App) runFanoutSub(ctx context.Context, args fanoutArgs, r *fanoutSubResult) {
	for iter := 1; iter <= args.maxIterPer; iter++ {
		if ctx.Err() != nil {
			r.Status = "timeout"
			r.Error = "cancelled mid-loop"
			return
		}
		prompt := buildSessionPrompt(r.Subgoal, iter, args.maxIterPer, r.WorktreePath)
		tick, err := defaultDispatcher.Dispatch(ctx, args.agent, prompt, r.WorktreePath, iter)
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
	// Hit max-iterations without DONE — treat as ready anyway so
	// the merge phase still tries to land partial work; the
	// summary records done=false so the operator can re-dispatch.
	r.Status = "ready"
}

func writeFanoutSummary(runDir string, s fanoutSummary) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "summary.json"), b, 0o644)
}
