package setup

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// recordingRunner captures install commands for assertion.
type recordingRunner struct {
	mu       sync.Mutex
	commands [][]string
	failOn   string // if a command's joined first arg matches this, return err
}

func (r *recordingRunner) Run(_ context.Context, cmd []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, append([]string(nil), cmd...))
	if len(cmd) > 0 && cmd[0] == r.failOn {
		return errors.New("runner: forced failure")
	}
	return nil
}

// programmableRecipe lets tests script every step.
type programmableRecipe struct {
	meta       RecipeMeta
	prereqs    []Prereq
	applyErr   error
	verifyErr  error
	applyCalls int
}

func (p *programmableRecipe) Meta() RecipeMeta { return p.meta }
func (p *programmableRecipe) Detect(context.Context, string) (Status, string, error) {
	return StatusAbsent, "", nil
}
func (p *programmableRecipe) Prereqs() []Prereq { return p.prereqs }
func (p *programmableRecipe) Apply(context.Context, string, Options) error {
	p.applyCalls++
	return p.applyErr
}
func (p *programmableRecipe) Verify(context.Context, string) error { return p.verifyErr }

func newProgrammable() *programmableRecipe {
	return &programmableRecipe{
		meta: RecipeMeta{
			Name:        "fake",
			Category:    CategoryRelease,
			Description: "test",
			Upstream:    "https://example.com",
		},
	}
}

// trackingPrompter records the calls and returns scripted decisions.
type trackingPrompter struct {
	decisions []PromptDecision
	calls     int
}

func (t *trackingPrompter) OnMissingPrereq(_ context.Context, _ Recipe, _ Prereq, _ error) (PromptDecision, error) {
	if t.calls >= len(t.decisions) {
		return PromptSkip, nil
	}
	d := t.decisions[t.calls]
	t.calls++
	return d, nil
}

func TestApplyHappyPath_NoPrereqs(t *testing.T) {
	r := newProgrammable()
	rr := &recordingRunner{}
	res, err := Apply(context.Background(), r, ApplyOptions{
		Repo:     t.TempDir(),
		Prompter: AlwaysSkip{},
		Runner:   rr,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.VerifyOK {
		t.Errorf("expected VerifyOK; result %+v", res)
	}
	if r.applyCalls != 1 {
		t.Errorf("Apply should have been called once; got %d", r.applyCalls)
	}
	if len(rr.commands) != 0 {
		t.Errorf("no install commands expected; got %v", rr.commands)
	}
}

func TestApplyInstallsMissingPrereq(t *testing.T) {
	checked := 0
	r := newProgrammable()
	r.prereqs = []Prereq{
		{
			Name: "fake-tool",
			Check: func(context.Context) error {
				checked++
				if checked == 1 {
					return errors.New("not installed")
				}
				return nil
			},
			Install: map[Platform][]string{
				CurrentPlatform(): {"apt", "install", "fake-tool"},
			},
		},
	}
	rr := &recordingRunner{}
	res, err := Apply(context.Background(), r, ApplyOptions{
		Repo:     t.TempDir(),
		Prompter: AlwaysInstall{},
		Runner:   rr,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(rr.commands) != 1 {
		t.Fatalf("expected one install command; got %v", rr.commands)
	}
	if !reflect.DeepEqual(rr.commands[0], []string{"apt", "install", "fake-tool"}) {
		t.Errorf("install command mismatch: %v", rr.commands[0])
	}
	if len(res.Installed) != 1 || res.Installed[0] != "fake-tool" {
		t.Errorf("Result.Installed wrong: %v", res.Installed)
	}
}

func TestApplySkippedWhenUserDeclines(t *testing.T) {
	r := newProgrammable()
	r.prereqs = []Prereq{{
		Name:  "obsidian",
		Check: func(context.Context) error { return errors.New("missing") },
	}}
	res, err := Apply(context.Background(), r, ApplyOptions{
		Repo:     t.TempDir(),
		Prompter: AlwaysSkip{},
		Runner:   &recordingRunner{},
	})
	if !errors.Is(err, ErrSkippedByUser) {
		t.Fatalf("expected ErrSkippedByUser; got %v", err)
	}
	if !res.Skipped {
		t.Error("Result.Skipped should be true")
	}
	if r.applyCalls != 0 {
		t.Error("Apply should NOT be called when prereq is skipped")
	}
}

func TestApplyFailsOnInstallError(t *testing.T) {
	r := newProgrammable()
	r.prereqs = []Prereq{{
		Name:  "broken",
		Check: func(context.Context) error { return errors.New("missing") },
		Install: map[Platform][]string{
			CurrentPlatform(): {"failing-installer"},
		},
	}}
	rr := &recordingRunner{failOn: "failing-installer"}
	_, err := Apply(context.Background(), r, ApplyOptions{
		Repo:     t.TempDir(),
		Prompter: AlwaysInstall{},
		Runner:   rr,
	})
	if err == nil {
		t.Fatal("expected Apply to fail when installer errors")
	}
	if r.applyCalls != 0 {
		t.Error("recipe.Apply should not be called after install failure")
	}
}

func TestApplyVerifyFailureSurfacesButDoesNotErr(t *testing.T) {
	r := newProgrammable()
	r.verifyErr = errors.New("post-condition: missing file")
	res, err := Apply(context.Background(), r, ApplyOptions{
		Repo:     t.TempDir(),
		Prompter: AlwaysSkip{},
		Runner:   &recordingRunner{},
	})
	if err != nil {
		t.Fatalf("Verify failure should be non-fatal; got err=%v", err)
	}
	if res.VerifyOK {
		t.Error("VerifyOK should be false")
	}
	if res.VerifyErr == nil {
		t.Error("VerifyErr should carry the verify error")
	}
}

func TestApplyManualPathReChecks(t *testing.T) {
	calls := 0
	// First check: missing. Manual prompt → re-check still missing → skip.
	r := newProgrammable()
	r.prereqs = []Prereq{{
		Name: "manual-only",
		Check: func(context.Context) error {
			calls++
			return errors.New("still missing")
		},
		ManualHint: "see https://example.com/install",
	}}
	prompter := &trackingPrompter{decisions: []PromptDecision{PromptManual}}
	res, err := Apply(context.Background(), r, ApplyOptions{
		Repo:     t.TempDir(),
		Prompter: prompter,
		Runner:   &recordingRunner{},
	})
	if !errors.Is(err, ErrSkippedByUser) {
		t.Fatalf("expected skip after failed manual re-check; got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected Check called twice (initial + post-manual); got %d", calls)
	}
	if len(res.ManualHints) == 0 {
		t.Error("Result.ManualHints should record the prereq that needed manual install")
	}
}

func TestApplyRequiresPrompter(t *testing.T) {
	r := newProgrammable()
	_, err := Apply(context.Background(), r, ApplyOptions{
		Repo: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Apply should refuse a nil Prompter")
	}
}

func TestPrereqCheckHandlesNilCheck(t *testing.T) {
	r := newProgrammable()
	r.prereqs = []Prereq{{Name: "nilcheck"}}
	out := PrereqCheck(context.Background(), r)
	if !AllSatisfied(out) {
		t.Error("nil Check should be treated as satisfied")
	}
}
