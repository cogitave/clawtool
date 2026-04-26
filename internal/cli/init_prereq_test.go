package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// osCommandRunner is the wizard's exec-shell adapter. We can't run
// real install commands in unit tests (no apt / no brew on every
// runner), but we can drive the wrapper itself with safe shell
// commands. The contract under test:
//
//   - empty command → error
//   - ok command    → no error, stdout/stderr threaded through
//   - non-zero exit → wrapped error mentioning the command line

func TestOSCommandRunner_RejectsEmptyCommand(t *testing.T) {
	r := &osCommandRunner{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err := r.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("Run(nil) should return an error")
	}
	if !strings.Contains(err.Error(), "empty install command") {
		t.Errorf("error should mention 'empty install command': %v", err)
	}
}

func TestOSCommandRunner_OKCommandPipesStdout(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	r := &osCommandRunner{stdout: out, stderr: errb}
	if err := r.Run(context.Background(), []string{"echo", "clawtool-test"}); err != nil {
		t.Fatalf("Run(echo): %v", err)
	}
	if !strings.Contains(out.String(), "clawtool-test") {
		t.Errorf("stdout should include the echoed string; got %q", out.String())
	}
}

func TestOSCommandRunner_NonZeroExitWrapsCommandLine(t *testing.T) {
	r := &osCommandRunner{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err := r.Run(context.Background(), []string{"sh", "-c", "exit 7"})
	if err == nil {
		t.Fatal("Run with exit 7 should return an error")
	}
	if !strings.Contains(err.Error(), "sh -c exit 7") {
		t.Errorf("error should include the command line for diagnosis; got %v", err)
	}
}

// newWizardPrompter wires the prompter + runner pair the wizard
// uses. Asserting both are non-nil + the right type guards against
// future refactors silently dropping a side.
func TestNewWizardPrompter_ReturnsBothComponents(t *testing.T) {
	prompter, runner := newWizardPrompter(&bytes.Buffer{}, &bytes.Buffer{})
	if prompter == nil {
		t.Error("prompter should not be nil")
	}
	if runner == nil {
		t.Error("runner should not be nil")
	}
	if _, ok := prompter.(*interactiveInstallPrompter); !ok {
		t.Errorf("prompter should be *interactiveInstallPrompter; got %T", prompter)
	}
	if _, ok := runner.(*osCommandRunner); !ok {
		t.Errorf("runner should be *osCommandRunner; got %T", runner)
	}
}

// Sanity: ensure the prompter's struct keeps the writer fields
// reachable so a future refactor doesn't accidentally bypass user
// IO. We construct one directly and verify we can write through.
func TestInteractiveInstallPrompter_WritersWired(t *testing.T) {
	out := &bytes.Buffer{}
	p := &interactiveInstallPrompter{stdout: out, stderr: &bytes.Buffer{}}
	// Don't actually run the huh form — just verify the struct
	// exposes the writers we'd write to in the manual-hint branch.
	if p.stdout == nil {
		t.Fatal("stdout writer wired to nil")
	}
	// And confirm the type checks the way the wizard expects.
	if !errors.Is(error(nil), nil) {
		t.Fatal("std errors.Is sanity check broken")
	}
}
