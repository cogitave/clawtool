package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// TestPrimaryDefault_PicksClaudeCodeWhenDetected confirms claude
// is the priority pick — clawtool runs inside Claude Code most of
// the time, so the wizard's first guess should be claude-code when
// the binary is on PATH.
func TestPrimaryDefault_PicksClaudeCodeWhenDetected(t *testing.T) {
	cases := []struct {
		name  string
		found map[string]bool
		want  string
	}{
		{"claude-detected wins", map[string]bool{"claude": true, "codex": true}, "claude-code"},
		{"falls through to codex", map[string]bool{"claude": false, "codex": true}, "codex"},
		{"falls through to gemini", map[string]bool{"gemini": true}, "gemini"},
		{"none detected", map[string]bool{}, ""},
	}
	for _, c := range cases {
		if got := primaryDefault(c.found); got != c.want {
			t.Errorf("%s: primaryDefault(%v) = %q, want %q", c.name, c.found, got, c.want)
		}
	}
}

// TestPrimaryCLIOptions_DetectedFirst confirms detected hosts sort
// before undetected ones so the cursor lands on something installed
// when the wizard renders. The "none" sentinel is always last.
func TestPrimaryCLIOptions_DetectedFirst(t *testing.T) {
	found := map[string]bool{"claude": true, "codex": true, "gemini": false, "opencode": false, "hermes": false}
	opts := primaryCLIOptions(found)
	if len(opts) != 6 {
		t.Fatalf("expected 6 options (5 families + 1 sentinel), got %d", len(opts))
	}
	// First two should be the detected ones (claude-code + codex)
	// in the canonical order, with the "✓ detected" label.
	if !strings.Contains(opts[0].Key, "claude-code") || !strings.Contains(opts[0].Key, "detected") {
		t.Errorf("first option label = %q, want claude-code/detected", opts[0].Key)
	}
	if !strings.Contains(opts[1].Key, "codex") || !strings.Contains(opts[1].Key, "detected") {
		t.Errorf("second option label = %q, want codex/detected", opts[1].Key)
	}
	// Last is the sentinel.
	last := opts[len(opts)-1]
	if last.Value != "" {
		t.Errorf("last option value = %q, want empty sentinel", last.Value)
	}
	if !strings.Contains(last.Key, "none") {
		t.Errorf("last option label = %q, want 'none / decide later'", last.Key)
	}
}

// fakeDeps drives the onboard wizard without a TTY. The test sets
// `state` upfront via the form-runner stub so we can assert which
// side effects fire.
type fakeDeps struct {
	pathHits     map[string]bool
	formCalled   bool
	formErr      error
	bridgeCalled []string
	identityHit  bool
	stdout       *bytes.Buffer
}

func newFakeDeps(found map[string]bool) (*fakeDeps, onboardDeps) {
	f := &fakeDeps{
		pathHits: found,
		stdout:   &bytes.Buffer{},
	}
	return f, onboardDeps{
		lookPath: func(bin string) error {
			if f.pathHits[bin] {
				return nil
			}
			return errors.New("not on PATH")
		},
		runForm: func(form *huh.Form) error {
			f.formCalled = true
			return f.formErr
		},
		bridgeAdd: func(fam string) error {
			f.bridgeCalled = append(f.bridgeCalled, fam)
			return nil
		},
		createIdentity: func() error {
			f.identityHit = true
			return nil
		},
		identityExists: func() bool { return false },
		stdoutLn:       func(s string) { f.stdout.WriteString(s + "\n") },
	}
}

func TestOnboard_HostMissingEverything(t *testing.T) {
	app := New()
	f, deps := newFakeDeps(map[string]bool{}) // nothing on PATH
	if err := app.onboard(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !f.formCalled {
		t.Error("form should be presented even when no CLIs found")
	}
	// No bridge installs because the form runner stub left the
	// default empty slice.
	if len(f.bridgeCalled) != 0 {
		t.Errorf("expected 0 bridge installs (form not exercised); got %v", f.bridgeCalled)
	}
}

func TestOnboard_AllPresent_NoMissingBridges(t *testing.T) {
	app := New()
	f, deps := newFakeDeps(map[string]bool{
		"claude": true, "codex": true, "opencode": true, "gemini": true,
	})
	if err := app.onboard(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !f.formCalled {
		t.Error("form should still be presented (identity + telemetry pages)")
	}
	if !strings.Contains(f.stdout.String(), "callable agents") {
		t.Errorf("final hint should mention `clawtool send --list`; got %q", f.stdout.String())
	}
}

func TestOnboard_FormAborted_ReturnsCleanly(t *testing.T) {
	app := New()
	f, deps := newFakeDeps(map[string]bool{"claude": true})
	f.formErr = huh.ErrUserAborted
	if err := app.onboard(context.Background(), deps); err != nil {
		t.Errorf("user-aborted form should not surface as error; got %v", err)
	}
	if !strings.Contains(f.stdout.String(), "aborted") {
		t.Errorf("aborted run should print an explanatory line; got %q", f.stdout.String())
	}
}

func TestOnboard_FormErrorPropagates(t *testing.T) {
	app := New()
	f, deps := newFakeDeps(map[string]bool{"claude": true})
	f.formErr = errors.New("boom")
	if err := app.onboard(context.Background(), deps); err == nil {
		t.Error("non-abort form error should propagate")
	}
}

func TestDetectHost_MissingBridgeList(t *testing.T) {
	state := detectHost(func(bin string) error {
		if bin == "claude" || bin == "codex" {
			return nil
		}
		return errors.New("missing")
	})
	if !state.Found["claude"] || !state.Found["codex"] {
		t.Errorf("found map wrong: %+v", state.Found)
	}
	if state.Found["opencode"] || state.Found["gemini"] {
		t.Errorf("found map wrong (false-positives): %+v", state.Found)
	}
	wantMissing := map[string]bool{"opencode": true, "gemini": true, "hermes": true}
	for _, fam := range state.MissingBridges {
		if !wantMissing[fam] {
			t.Errorf("unexpected missing-bridge entry: %q", fam)
		}
		delete(wantMissing, fam)
	}
	if len(wantMissing) != 0 {
		t.Errorf("missing-bridge entries not surfaced: %v", wantMissing)
	}
	// claude is reported as a prereq, never as a bridge.
	for _, fam := range state.MissingBridges {
		if fam == "claude" {
			t.Error("claude should never appear in the bridge list")
		}
	}
}

func TestHostSummary_FormatsAllFour(t *testing.T) {
	out := hostSummary(map[string]bool{
		"claude": true, "codex": false, "opencode": true, "gemini": false,
	})
	for _, fam := range []string{"claude", "codex", "opencode", "gemini"} {
		if !strings.Contains(out, fam) {
			t.Errorf("hostSummary missing %q", fam)
		}
	}
	if !strings.Contains(out, "✓") || !strings.Contains(out, "✗") {
		t.Errorf("hostSummary should mark found / missing: %q", out)
	}
}
