package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubFanoutDispatcher counts in-flight calls + max overlap.
// Each dispatch sleeps for hold so concurrent tests can observe
// overlap (or its absence).
type stubFanoutDispatcher struct {
	mu          sync.Mutex
	inFlight    int32
	maxInFlight int32
	calls       int
	hold        time.Duration
}

func (s *stubFanoutDispatcher) Dispatch(ctx context.Context, _, _, workdir string, _ int) (Tick, error) {
	cur := atomic.AddInt32(&s.inFlight, 1)
	defer atomic.AddInt32(&s.inFlight, -1)
	for {
		max := atomic.LoadInt32(&s.maxInFlight)
		if cur <= max || atomic.CompareAndSwapInt32(&s.maxInFlight, max, cur) {
			break
		}
	}
	if s.hold > 0 {
		select {
		case <-ctx.Done():
			return Tick{}, ctx.Err()
		case <-time.After(s.hold):
		}
	}
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return Tick{Summary: "did " + workdir, Done: true}, nil
}

// recordingGit captures every git invocation so tests can assert
// merge order without exec'ing real git.
type recordingGit struct {
	mu    sync.Mutex
	calls [][]string
}

func (g *recordingGit) Exec(_ context.Context, dir string, args ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, append([]string{dir}, args...))
	return nil
}

func (g *recordingGit) Calls() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([][]string, len(g.calls))
	for i, c := range g.calls {
		out[i] = append([]string{}, c...)
	}
	return out
}

// withFanoutStubs swaps the package-level dispatcher + git seam for
// the duration of t.
func withFanoutStubs(t *testing.T, d AutonomousDispatcher, g *recordingGit) {
	t.Helper()
	prevD := SetAutonomousDispatcher(d)
	prevG := SetFanoutGitExec(g.Exec)
	t.Cleanup(func() {
		SetAutonomousDispatcher(prevD)
		SetFanoutGitExec(prevG)
	})
}

// TestFanout_DryRunPrintsPlan — --dry-run echoes the parsed
// subgoals and worktree paths without dispatching or git-ing.
func TestFanout_DryRunPrintsPlan(t *testing.T) {
	repo := onboardedRepo(t)
	stub := &stubFanoutDispatcher{}
	gitRec := &recordingGit{}
	withFanoutStubs(t, stub, gitRec)

	app, out, _, _ := newApp(t)
	rc := app.Run([]string{
		"fanout",
		"--workdir", repo,
		"--cooldown", "0s",
		"--dry-run",
		"add catalog A ;; refactor module B ;; tidy docs",
	})
	if rc != 0 {
		t.Fatalf("dry-run exit = %d, want 0", rc)
	}
	got := out.String()
	for _, want := range []string{
		"dry-run plan",
		"add catalog A",
		"refactor module B",
		"tidy docs",
		"max-concurrent:          3", // capped to len(subgoals)
		"wt-1",
		"wt-3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if stub.calls != 0 {
		t.Errorf("dispatcher should not run on --dry-run; got %d", stub.calls)
	}
	if len(gitRec.Calls()) != 0 {
		t.Errorf("git should not run on --dry-run; got %v", gitRec.Calls())
	}
}

// TestFanout_RejectsEmptyPlan — an empty positional arg + no --plan
// must exit 2 with a usage error and never invoke the dispatcher.
func TestFanout_RejectsEmptyPlan(t *testing.T) {
	repo := onboardedRepo(t)
	stub := &stubFanoutDispatcher{}
	gitRec := &recordingGit{}
	withFanoutStubs(t, stub, gitRec)

	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{
		"fanout",
		"--workdir", repo,
		"--cooldown", "0s",
		// no plan arg, no --plan
	})
	if rc != 2 {
		t.Fatalf("empty-plan exit = %d, want 2", rc)
	}
	gotErr := errb.String()
	if !strings.Contains(gotErr, "empty") {
		t.Errorf("stderr should mention empty plan; got %q", gotErr)
	}
	if stub.calls != 0 {
		t.Errorf("dispatcher must not run on empty plan; got %d calls", stub.calls)
	}
}

// TestFanout_HonorsMaxConcurrent — the dispatcher counts in-flight
// goroutines; max-in-flight must never exceed --max-concurrent.
func TestFanout_HonorsMaxConcurrent(t *testing.T) {
	repo := onboardedRepo(t)
	stub := &stubFanoutDispatcher{hold: 30 * time.Millisecond}
	gitRec := &recordingGit{}
	withFanoutStubs(t, stub, gitRec)

	app, _, _, _ := newApp(t)
	rc := app.Run([]string{
		"fanout",
		"--workdir", repo,
		"--cooldown", "0s",
		"--max-concurrent", "2",
		"--max-iterations-per-sub", "1",
		"sub1 ;; sub2 ;; sub3 ;; sub4 ;; sub5",
	})
	if rc != 0 {
		t.Fatalf("fanout exit = %d, want 0", rc)
	}
	if got := atomic.LoadInt32(&stub.maxInFlight); got > 2 {
		t.Errorf("max-in-flight = %d, want ≤ 2", got)
	}
	if stub.calls != 5 {
		t.Errorf("dispatcher calls = %d, want 5", stub.calls)
	}
}

// TestFanout_SequentialMergeOrder — when N subs all complete, assert
// the merge phase processes them in completion order. We force a
// known completion order by giving sub-1 a longer hold so it
// completes LAST despite being scheduled first.
func TestFanout_SequentialMergeOrder(t *testing.T) {
	repo := onboardedRepo(t)
	gitRec := &recordingGit{}

	// orderedDispatcher returns done=true after N ms specific to
	// each subgoal so completion order is deterministic.
	disp := &orderedDispatcher{}
	prevD := SetAutonomousDispatcher(disp)
	prevG := SetFanoutGitExec(gitRec.Exec)
	t.Cleanup(func() {
		SetAutonomousDispatcher(prevD)
		SetFanoutGitExec(prevG)
	})

	app, _, _, _ := newApp(t)
	rc := app.Run([]string{
		"fanout",
		"--workdir", repo,
		"--cooldown", "0s",
		"--max-concurrent", "4",
		"--max-iterations-per-sub", "1",
		// sub2 finishes first, sub3 second, sub1 last.
		"sub1 ;; sub2 ;; sub3",
	})
	if rc != 0 {
		t.Fatalf("fanout exit = %d, want 0", rc)
	}

	// Pull merge calls in invocation order; assert branch arg is
	// sub-2 first, then sub-3, then sub-1.
	var mergeBranches []string
	for _, c := range gitRec.Calls() {
		// c[0] = dir; c[1..] = git args
		if len(c) >= 3 && c[1] == "merge" && c[2] == "--ff-only" {
			mergeBranches = append(mergeBranches, c[3])
		}
	}
	if len(mergeBranches) != 3 {
		t.Fatalf("merge calls = %d, want 3 (got %v)", len(mergeBranches), mergeBranches)
	}
	if !strings.HasSuffix(mergeBranches[0], "/sub-2") {
		t.Errorf("first merge = %q, want ending /sub-2", mergeBranches[0])
	}
	if !strings.HasSuffix(mergeBranches[1], "/sub-3") {
		t.Errorf("second merge = %q, want ending /sub-3", mergeBranches[1])
	}
	if !strings.HasSuffix(mergeBranches[2], "/sub-1") {
		t.Errorf("third merge = %q, want ending /sub-1", mergeBranches[2])
	}
}

// orderedDispatcher returns a controlled completion order: sub-2
// completes first (10ms), sub-3 second (40ms), sub-1 last (80ms).
type orderedDispatcher struct{}

func (orderedDispatcher) Dispatch(ctx context.Context, agent, prompt, workdir string, iter int) (Tick, error) {
	delay := 80 * time.Millisecond // default = sub-1
	switch {
	case strings.HasSuffix(workdir, "wt-2"):
		delay = 10 * time.Millisecond
	case strings.HasSuffix(workdir, "wt-3"):
		delay = 40 * time.Millisecond
	}
	select {
	case <-ctx.Done():
		return Tick{}, ctx.Err()
	case <-time.After(delay):
	}
	return Tick{Summary: workdir, Done: true}, nil
}

// TestFanout_CleanShutdownOnCtrlC — context cancel mid-run produces
// a partial summary.json with stopped=interrupted. Worktree teardown
// is best-effort; we just assert summary lands and records the
// cancellation cleanly.
func TestFanout_CleanShutdownOnCtrlC(t *testing.T) {
	repo := onboardedRepo(t)

	// Block forever in the dispatcher so the run is mid-flight
	// when we cancel; cancellable via the dispatch ctx.
	disp := &blockingDispatcher{}
	gitRec := &recordingGit{}
	prevD := SetAutonomousDispatcher(disp)
	prevG := SetFanoutGitExec(gitRec.Exec)
	t.Cleanup(func() {
		SetAutonomousDispatcher(prevD)
		SetFanoutGitExec(prevG)
	})

	// Drive runFanoutLoop directly with a cancellable context so
	// we don't have to fight with signal.Notify in the test
	// harness. Same code path otherwise.
	app, _, _, _ := newApp(t)
	args := fanoutArgs{
		agent:       "claude",
		maxConc:     2,
		cooldown:    0,
		workdir:     repo,
		maxIterPer:  1,
		maxSubgoals: 8,
	}
	subs := []string{"sub1", "sub2"}
	runID := "test-cancel"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		// Cancel after a brief delay so dispatch is in flight.
		time.AfterFunc(20*time.Millisecond, cancel)
		done <- app.runFanoutLoop(ctx, args, subs, runID)
	}()
	select {
	case rc := <-done:
		// Non-zero is expected on interrupt.
		if rc == 0 {
			t.Errorf("rc = 0; want non-zero on cancellation")
		}
	case <-time.After(2 * time.Second):
		cancel()
		disp.cancel()
		t.Fatalf("runFanoutLoop did not return within 2s")
	}

	summaryPath := filepath.Join(repo, ".clawtool", "fanout", runID, "summary.json")
	b, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("summary.json missing: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("summary.json parse: %v", err)
	}
	// Either "interrupted" (ctx cancelled in main loop) or "ok"
	// with timeout subs is acceptable; but at least one sub must
	// be marked failed/timeout/pending — none should be merged.
	subsArr, _ := s["subs"].([]any)
	if len(subsArr) != 2 {
		t.Fatalf("subs len = %d, want 2", len(subsArr))
	}
	for _, raw := range subsArr {
		sub, _ := raw.(map[string]any)
		status, _ := sub["status"].(string)
		if status == "merged" {
			t.Errorf("sub merged despite cancellation: %v", sub)
		}
	}
}

// blockingDispatcher honours its own .cancel() — once invoked, all
// in-flight Dispatch calls return ctx.Err() promptly. Tests use
// this to simulate Ctrl-C without juggling signals.
type blockingDispatcher struct {
	mu       sync.Mutex
	cancelCh chan struct{}
}

func (b *blockingDispatcher) ensure() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelCh == nil {
		b.cancelCh = make(chan struct{})
	}
	return b.cancelCh
}
func (b *blockingDispatcher) cancel() {
	ch := b.ensure()
	select {
	case <-ch:
	default:
		close(ch)
	}
}
func (b *blockingDispatcher) Dispatch(ctx context.Context, _ string, _ string, _ string, _ int) (Tick, error) {
	ch := b.ensure()
	select {
	case <-ctx.Done():
		return Tick{}, ctx.Err()
	case <-ch:
		return Tick{}, context.Canceled
	}
}
