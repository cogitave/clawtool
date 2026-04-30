package setuptools

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// fanoutStubDispatcher mirrors stubAutonomousDispatcher but adds
// in-flight tracking for the max-concurrent assertion. It satisfies
// the SAME AutonomousDispatcher seam that AutonomousRun uses, so
// installing it via withStubDispatcher covers both surfaces.
type fanoutStubDispatcher struct {
	mu          sync.Mutex
	calls       int
	inFlight    int32
	maxInFlight int32
	hold        time.Duration
}

func (d *fanoutStubDispatcher) Dispatch(ctx context.Context, _ string) (AutonomousTick, error) {
	cur := atomic.AddInt32(&d.inFlight, 1)
	defer atomic.AddInt32(&d.inFlight, -1)
	for {
		max := atomic.LoadInt32(&d.maxInFlight)
		if cur <= max || atomic.CompareAndSwapInt32(&d.maxInFlight, max, cur) {
			break
		}
	}
	if d.hold > 0 {
		select {
		case <-ctx.Done():
			return AutonomousTick{}, ctx.Err()
		case <-time.After(d.hold):
		}
	}
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return AutonomousTick{Summary: "ok", Done: true}, nil
}

// recordingGit captures every git invocation so tests can assert
// merge order without hitting real git. Mirror of CLI's recordingGit.
type fanoutRecordingGit struct {
	mu    sync.Mutex
	calls [][]string
}

func (g *fanoutRecordingGit) Exec(_ context.Context, dir string, args ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, append([]string{dir}, args...))
	return nil
}

func (g *fanoutRecordingGit) Calls() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([][]string, len(g.calls))
	for i, c := range g.calls {
		out[i] = append([]string{}, c...)
	}
	return out
}

// withStubGit swaps defaultGitExec for the duration of t.
func withStubGit(t *testing.T, g *fanoutRecordingGit) {
	t.Helper()
	prev := defaultGitExec
	defaultGitExec = g.Exec
	t.Cleanup(func() { defaultGitExec = prev })
}

// mkFanoutReq fabricates an MCP CallToolRequest with subgoals as
// []any (mcp-go's default unmarshal shape).
func mkFanoutReq(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "Fanout",
			Arguments: args,
		},
	}
}

// TestFanout_DryRunReturnsPlan — dry_run=true emits subgoals + per-sub
// worktree paths without dispatching or git-ing.
func TestFanout_DryRunReturnsPlan(t *testing.T) {
	disp := &fanoutStubDispatcher{}
	withStubDispatcher(t, disp)
	gitRec := &fanoutRecordingGit{}
	withStubGit(t, gitRec)

	repo := repoWithClawtool(t)
	res, err := runFanout(context.Background(), mkFanoutReq(map[string]any{
		"subgoals": []any{"add catalog A", "refactor module B", "tidy docs"},
		"repo":     repo,
		"dry_run":  true,
	}))
	if err != nil {
		t.Fatalf("runFanout: %v", err)
	}
	got, ok := res.StructuredContent.(fanoutResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want fanoutResult", res.StructuredContent)
	}
	if !got.Planned {
		t.Error("Planned = false; want true on dry_run")
	}
	if len(got.Subs) != 3 {
		t.Errorf("Subs = %d, want 3", len(got.Subs))
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher called %d times under dry_run; want 0", disp.calls)
	}
	if len(gitRec.Calls()) != 0 {
		t.Errorf("git called under dry_run; want 0 calls, got %v", gitRec.Calls())
	}
	for i, sub := range got.Subs {
		wantSuffix := "wt-" + itoa(i+1)
		if !strings.HasSuffix(sub.WorktreePath, wantSuffix) {
			t.Errorf("Subs[%d].WorktreePath = %q, want suffix %q", i, sub.WorktreePath, wantSuffix)
		}
		if sub.Status != "pending" {
			t.Errorf("Subs[%d].Status = %q, want pending", i, sub.Status)
		}
	}
}

// TestFanout_AbortsWithoutOnboard — fresh repo lacking .clawtool/ → typed
// error pointing at OnboardWizard, no dispatch.
func TestFanout_AbortsWithoutOnboard(t *testing.T) {
	disp := &fanoutStubDispatcher{}
	withStubDispatcher(t, disp)
	gitRec := &fanoutRecordingGit{}
	withStubGit(t, gitRec)

	repo := t.TempDir() // no .clawtool/
	res, err := runFanout(context.Background(), mkFanoutReq(map[string]any{
		"subgoals": []any{"anything"},
		"repo":     repo,
	}))
	if err != nil {
		t.Fatalf("runFanout: %v", err)
	}
	got := res.StructuredContent.(fanoutResult)
	if !strings.Contains(got.ErrorReason, "OnboardWizard") {
		t.Errorf("ErrorReason = %q, want mention of OnboardWizard", got.ErrorReason)
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher called %d times despite onboard gate; want 0", disp.calls)
	}
}

// TestFanout_HonorsMaxConcurrent — counter-based assertion that
// max-in-flight never exceeds max_concurrent.
func TestFanout_HonorsMaxConcurrent(t *testing.T) {
	disp := &fanoutStubDispatcher{hold: 30 * time.Millisecond}
	withStubDispatcher(t, disp)
	gitRec := &fanoutRecordingGit{}
	withStubGit(t, gitRec)

	repo := repoWithClawtool(t)
	res, err := runFanout(context.Background(), mkFanoutReq(map[string]any{
		"subgoals":               []any{"s1", "s2", "s3", "s4", "s5"},
		"repo":                   repo,
		"max_concurrent":         float64(2),
		"cooldown_seconds":       float64(0),
		"max_iterations_per_sub": float64(1),
	}))
	if err != nil {
		t.Fatalf("runFanout: %v", err)
	}
	got := res.StructuredContent.(fanoutResult)
	if got.MaxConcurrent != 2 {
		t.Errorf("MaxConcurrent = %d, want 2", got.MaxConcurrent)
	}
	if max := atomic.LoadInt32(&disp.maxInFlight); max > 2 {
		t.Errorf("max-in-flight = %d, want ≤ 2", max)
	}
	if disp.calls != 5 {
		t.Errorf("dispatcher calls = %d, want 5", disp.calls)
	}
}

// TestFanout_DefaultsApplied — only required arg + repo; assert the
// runner stamped documented defaults.
func TestFanout_DefaultsApplied(t *testing.T) {
	disp := &fanoutStubDispatcher{}
	withStubDispatcher(t, disp)
	gitRec := &fanoutRecordingGit{}
	withStubGit(t, gitRec)

	repo := repoWithClawtool(t)
	res, err := runFanout(context.Background(), mkFanoutReq(map[string]any{
		"subgoals": []any{"only-one"},
		"repo":     repo,
		"dry_run":  true, // skip the heavy live path; defaults still apply
	}))
	if err != nil {
		t.Fatalf("runFanout: %v", err)
	}
	got := res.StructuredContent.(fanoutResult)
	if got.Agent != "claude" {
		t.Errorf("Agent default = %q, want \"claude\"", got.Agent)
	}
	// max_concurrent is capped to len(subs)=1 here, so the
	// default-application assertion is on Cooldown + MaxIterPerSub.
	if got.Cooldown != 300 {
		t.Errorf("Cooldown default = %d, want 300", got.Cooldown)
	}
	if got.MaxIterPerSub != 5 {
		t.Errorf("MaxIterPerSub default = %d, want 5", got.MaxIterPerSub)
	}
	if !got.DryRun {
		t.Error("DryRun echo = false; want true")
	}
	if got.MaxConcurrent != 1 {
		t.Errorf("MaxConcurrent (capped to len(subs)) = %d, want 1", got.MaxConcurrent)
	}
}

// TestFanout_RejectsEmptySubgoals — missing or empty subgoals is a
// typed error with no dispatch.
func TestFanout_RejectsEmptySubgoals(t *testing.T) {
	disp := &fanoutStubDispatcher{}
	withStubDispatcher(t, disp)

	repo := repoWithClawtool(t)
	res, err := runFanout(context.Background(), mkFanoutReq(map[string]any{
		"subgoals": []any{},
		"repo":     repo,
	}))
	if err != nil {
		t.Fatalf("runFanout: %v", err)
	}
	if res == nil || res.IsError == false {
		t.Errorf("expected error result; got %#v", res)
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher called %d times despite empty subgoals; want 0", disp.calls)
	}
}
