package setuptools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// stubAutonomousDispatcher is the test seam — fixed reply + a
// counter so tests assert iteration count and stop-on-done
// without exercising any real BIAM peer. Same pattern
// init_apply_test.go uses for its dispatcher equivalents.
type stubAutonomousDispatcher struct {
	calls       int
	doneOnIter  int // 0 = never; 1-indexed
	filesPerHit []string
}

func (d *stubAutonomousDispatcher) Dispatch(_ context.Context, _ string) (AutonomousTick, error) {
	d.calls++
	t := AutonomousTick{
		Summary:      "iter " + itoa(d.calls),
		FilesChanged: d.filesPerHit,
	}
	if d.doneOnIter > 0 && d.calls >= d.doneOnIter {
		t.Done = true
	}
	return t, nil
}

// itoa avoids strconv just for this micro-helper. Mirrors the
// minimalist style of init_apply_test.go's contains/stat.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// withStubDispatcher swaps defaultDispatcher for the duration of
// the test, restoring the original on cleanup. Ensures parallel
// tests (if any) don't see each other's stubs.
func withStubDispatcher(t *testing.T, d AutonomousDispatcher) {
	t.Helper()
	prev := defaultDispatcher
	defaultDispatcher = d
	t.Cleanup(func() { defaultDispatcher = prev })
}

// mkAutonomousReq fabricates an MCP CallToolRequest. Empty
// strings + zero numeric args fall through to the runner's
// default handling — that's the same shape mkInitApplyReq
// uses in init_apply_test.go.
func mkAutonomousReq(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "AutonomousRun",
			Arguments: args,
		},
	}
}

// repoWithClawtool creates a tmp dir + an empty `.clawtool/`
// subdir so the onboarding gate passes. The runner doesn't
// inspect the dir's contents — presence is enough.
func repoWithClawtool(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".clawtool"), 0o755); err != nil {
		t.Fatalf("mkdir .clawtool: %v", err)
	}
	return repo
}

// TestAutonomousRun_DryRunReturnsPlan — dry_run=true emits the
// planned prompt sequence without calling the dispatcher.
func TestAutonomousRun_DryRunReturnsPlan(t *testing.T) {
	disp := &stubAutonomousDispatcher{}
	withStubDispatcher(t, disp)

	repo := repoWithClawtool(t)
	res, err := runAutonomousRun(context.Background(), mkAutonomousReq(map[string]any{
		"goal":           "build the foo plugin",
		"repo":           repo,
		"dry_run":        true,
		"max_iterations": float64(3),
	}))
	if err != nil {
		t.Fatalf("runAutonomousRun: %v", err)
	}
	got, ok := res.StructuredContent.(autonomousRunResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want autonomousRunResult", res.StructuredContent)
	}
	if !got.Planned {
		t.Error("Planned = false; want true on dry_run")
	}
	if got.Goal != "build the foo plugin" {
		t.Errorf("Goal = %q, want echoed back", got.Goal)
	}
	if len(got.PlannedPrompts) != 3 {
		t.Errorf("PlannedPrompts = %d, want 3", len(got.PlannedPrompts))
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher called %d times under dry_run; want 0", disp.calls)
	}
}

// TestAutonomousRun_AbortsWithoutOnboard — a fresh repo lacking
// `.clawtool/` returns a structured error pointing at OnboardWizard.
func TestAutonomousRun_AbortsWithoutOnboard(t *testing.T) {
	disp := &stubAutonomousDispatcher{}
	withStubDispatcher(t, disp)

	repo := t.TempDir() // no .clawtool/ subdir
	res, err := runAutonomousRun(context.Background(), mkAutonomousReq(map[string]any{
		"goal": "anything",
		"repo": repo,
	}))
	if err != nil {
		t.Fatalf("runAutonomousRun: %v", err)
	}
	got, ok := res.StructuredContent.(autonomousRunResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want autonomousRunResult", res.StructuredContent)
	}
	if !strings.Contains(got.ErrorReason, "OnboardWizard") {
		t.Errorf("ErrorReason = %q, want mention of OnboardWizard", got.ErrorReason)
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher called %d times despite onboard gate; want 0", disp.calls)
	}
}

// TestAutonomousRun_RespectsMaxIterations — stub never reports
// done; the loop must stop after max_iterations.
func TestAutonomousRun_RespectsMaxIterations(t *testing.T) {
	disp := &stubAutonomousDispatcher{} // doneOnIter=0 → never done
	withStubDispatcher(t, disp)

	repo := repoWithClawtool(t)
	res, err := runAutonomousRun(context.Background(), mkAutonomousReq(map[string]any{
		"goal":             "spin forever",
		"repo":             repo,
		"max_iterations":   float64(4),
		"cooldown_seconds": float64(0), // skip the wait so the test runs fast
	}))
	if err != nil {
		t.Fatalf("runAutonomousRun: %v", err)
	}
	got := res.StructuredContent.(autonomousRunResult)
	if got.Done {
		t.Error("Done = true; want false (stub never signals done)")
	}
	if got.IterationsRun != 4 {
		t.Errorf("IterationsRun = %d, want 4", got.IterationsRun)
	}
	if disp.calls != 4 {
		t.Errorf("dispatcher called %d times, want 4", disp.calls)
	}
}

// TestAutonomousRun_StopsOnDone — stub reports done on iter 2;
// the loop must stop there even though max_iterations=10.
func TestAutonomousRun_StopsOnDone(t *testing.T) {
	disp := &stubAutonomousDispatcher{
		doneOnIter:  2,
		filesPerHit: []string{"a.go", "b.go"},
	}
	withStubDispatcher(t, disp)

	repo := repoWithClawtool(t)
	res, err := runAutonomousRun(context.Background(), mkAutonomousReq(map[string]any{
		"goal":             "do the thing",
		"repo":             repo,
		"max_iterations":   float64(10),
		"cooldown_seconds": float64(0),
	}))
	if err != nil {
		t.Fatalf("runAutonomousRun: %v", err)
	}
	got := res.StructuredContent.(autonomousRunResult)
	if !got.Done {
		t.Error("Done = false; want true")
	}
	if got.IterationsRun != 2 {
		t.Errorf("IterationsRun = %d, want 2", got.IterationsRun)
	}
	if len(got.FilesChanged) != 2 {
		t.Errorf("FilesChanged = %v, want 2 entries (deduped across iters)", got.FilesChanged)
	}
}

// TestAutonomousRun_DefaultsApplied — call with only the
// required goal + a repo; assert the runner stamped the
// documented defaults onto the result struct.
func TestAutonomousRun_DefaultsApplied(t *testing.T) {
	disp := &stubAutonomousDispatcher{doneOnIter: 1}
	withStubDispatcher(t, disp)

	repo := repoWithClawtool(t)
	res, err := runAutonomousRun(context.Background(), mkAutonomousReq(map[string]any{
		"goal": "minimal call",
		"repo": repo,
	}))
	if err != nil {
		t.Fatalf("runAutonomousRun: %v", err)
	}
	got := res.StructuredContent.(autonomousRunResult)
	if got.Agent != "claude" {
		t.Errorf("Agent default = %q, want \"claude\"", got.Agent)
	}
	if got.MaxIterations != 10 {
		t.Errorf("MaxIterations default = %d, want 10", got.MaxIterations)
	}
	if got.Cooldown != 300 {
		t.Errorf("Cooldown default = %d, want 300", got.Cooldown)
	}
	if !got.CoreOnly {
		t.Error("CoreOnly default = false; want true")
	}
	if got.DryRun {
		t.Error("DryRun default = true; want false")
	}
}
