package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDispatcher is the test seam for autonomous mode. It records
// every dispatch and returns canned ticks per iteration. Tests
// install one via SetAutonomousDispatcher + t.Cleanup to restore.
type stubDispatcher struct {
	calls   int
	prompts []string
	ticks   []Tick // optional; if shorter than calls, the last entry is reused
	defTick Tick   // returned when ticks is nil
	// writeTick: when true, also write tick-N.json to disk so the
	// production realDispatcher's read path is exercised end-to-end.
	writeTick bool
}

func (s *stubDispatcher) Dispatch(ctx context.Context, agent, prompt, workdir string, iter int) (Tick, error) {
	s.calls++
	s.prompts = append(s.prompts, prompt)
	var t Tick
	if iter-1 < len(s.ticks) {
		t = s.ticks[iter-1]
	} else if len(s.ticks) > 0 {
		t = s.ticks[len(s.ticks)-1]
	} else {
		t = s.defTick
	}
	if s.writeTick {
		dir := filepath.Join(workdir, ".clawtool", "autonomous")
		_ = os.MkdirAll(dir, 0o755)
		b, _ := json.Marshal(t)
		_ = os.WriteFile(filepath.Join(dir, "tick-1.json"), b, 0o644)
	}
	return t, nil
}

// withStub installs s as the package dispatcher for the duration of t.
func withStub(t *testing.T, s *stubDispatcher) {
	t.Helper()
	prev := SetAutonomousDispatcher(s)
	t.Cleanup(func() { SetAutonomousDispatcher(prev) })
}

// onboardedRepo creates a tmp dir with a .clawtool/ marker so the
// guardrail check passes. Returns the absolute path.
func onboardedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatalf("mkdir .clawtool: %v", err)
	}
	return dir
}

// TestAutonomous_DryRunPrintsPlan — `--dry-run` should print the plan
// (goal / agent / max-iterations / cooldown / template) and NOT
// invoke the dispatcher.
func TestAutonomous_DryRunPrintsPlan(t *testing.T) {
	repo := onboardedRepo(t)
	stub := &stubDispatcher{}
	withStub(t, stub)
	app, out, _, _ := newApp(t)
	rc := app.Run([]string{
		"autonomous",
		"--workdir", repo,
		"--cooldown", "0s",
		"--dry-run",
		"refactor the parser",
	})
	if rc != 0 {
		t.Fatalf("dry-run exit = %d, want 0", rc)
	}
	got := out.String()
	for _, want := range []string{
		"dry-run plan",
		"refactor the parser",
		"agent:          claude",
		"max-iterations: 10",
		"session-prompt template",
		"clawtool autonomous mode",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if stub.calls != 0 {
		t.Errorf("dispatcher should not run on --dry-run; got %d calls", stub.calls)
	}
}

// TestAutonomous_AbortsWithoutOnboard — running in a repo with no
// .clawtool/ marker must refuse cleanly with exit code 1, not invoke
// the dispatcher, and print the onboard suggestion.
func TestAutonomous_AbortsWithoutOnboard(t *testing.T) {
	repo := t.TempDir() // NOT onboarded — no .clawtool/
	stub := &stubDispatcher{}
	withStub(t, stub)
	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{
		"autonomous",
		"--workdir", repo,
		"--cooldown", "0s",
		"refactor",
	})
	if rc != 1 {
		t.Fatalf("missing-onboard exit = %d, want 1", rc)
	}
	gotErr := errb.String()
	if !strings.Contains(gotErr, "not onboarded") {
		t.Errorf("stderr should mention 'not onboarded'; got %q", gotErr)
	}
	if !strings.Contains(gotErr, "OnboardStatus") && !strings.Contains(gotErr, "onboard") {
		t.Errorf("stderr should suggest onboard; got %q", gotErr)
	}
	if stub.calls != 0 {
		t.Errorf("dispatcher should not run pre-onboard; got %d calls", stub.calls)
	}
}

// TestAutonomous_RespectsMaxIterations — when the stub always returns
// done: false, the loop must stop at exactly --max-iterations and
// write final.json with stopped_reason=max-iterations.
func TestAutonomous_RespectsMaxIterations(t *testing.T) {
	repo := onboardedRepo(t)
	stub := &stubDispatcher{defTick: Tick{Summary: "made progress", Done: false}}
	withStub(t, stub)
	app, _, _, _ := newApp(t)
	rc := app.Run([]string{
		"autonomous",
		"--workdir", repo,
		"--cooldown", "0s",
		"--max-iterations", "3",
		"keep going",
	})
	// Hit max without DONE → exit 1 (CI-friendly signal).
	if rc != 1 {
		t.Fatalf("max-iterations exit = %d, want 1 (didn't reach DONE)", rc)
	}
	if stub.calls != 3 {
		t.Errorf("dispatcher calls = %d, want 3", stub.calls)
	}
	finalPath := filepath.Join(repo, ".clawtool", "autonomous", "final.json")
	b, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("final.json not written: %v", err)
	}
	var final map[string]any
	if err := json.Unmarshal(b, &final); err != nil {
		t.Fatalf("final.json parse: %v", err)
	}
	if final["stopped_reason"] != "max-iterations" {
		t.Errorf("stopped_reason = %v, want max-iterations", final["stopped_reason"])
	}
	if final["finished"] != false {
		t.Errorf("finished = %v, want false", final["finished"])
	}
	if iters, _ := final["iterations"].(float64); int(iters) != 3 {
		t.Errorf("iterations = %v, want 3", final["iterations"])
	}
}

// TestAutonomous_StopsOnDone — stub returns done=true on tick 2; the
// loop must stop at iteration 2 and write final.json with finished=true.
func TestAutonomous_StopsOnDone(t *testing.T) {
	repo := onboardedRepo(t)
	stub := &stubDispatcher{
		ticks: []Tick{
			{Summary: "iter 1: scaffolded", Done: false},
			{Summary: "iter 2: shipped", Done: true},
		},
	}
	withStub(t, stub)
	app, out, _, _ := newApp(t)
	rc := app.Run([]string{
		"autonomous",
		"--workdir", repo,
		"--cooldown", "0s",
		"--max-iterations", "10",
		"ship the feature",
	})
	if rc != 0 {
		t.Fatalf("done exit = %d, want 0\n--- stdout ---\n%s", rc, out.String())
	}
	if stub.calls != 2 {
		t.Errorf("dispatcher calls = %d, want 2 (loop should stop on done)", stub.calls)
	}
	finalPath := filepath.Join(repo, ".clawtool", "autonomous", "final.json")
	b, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("final.json not written: %v", err)
	}
	var final map[string]any
	if err := json.Unmarshal(b, &final); err != nil {
		t.Fatalf("final.json parse: %v", err)
	}
	if final["finished"] != true {
		t.Errorf("finished = %v, want true", final["finished"])
	}
	if final["stopped_reason"] != "done" {
		t.Errorf("stopped_reason = %v, want done", final["stopped_reason"])
	}
	// Bonus: confirm iteration metadata flows into the prompt.
	if !strings.Contains(stub.prompts[0], "iteration 1 of 10") {
		t.Errorf("prompt should embed iteration metadata; got: %s", stub.prompts[0])
	}
}
