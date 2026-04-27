package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

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
