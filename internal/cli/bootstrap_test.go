package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// stubBootstrap installs deterministic lookPath + spawnAgent
// replacements for one test's lifetime. captured is mutated by
// spawnAgent so tests can assert what would have been spawned.
type capturedSpawn struct {
	bin    string
	argv   []string
	prompt string
	calls  int
}

func stubBootstrap(t *testing.T, lookErr error) *capturedSpawn {
	t.Helper()
	cap := &capturedSpawn{}
	prevLook := lookPath
	prevSpawn := spawnAgent
	lookPath = func(bin string) (string, error) {
		if lookErr != nil {
			return "", lookErr
		}
		return "/usr/bin/" + bin, nil
	}
	spawnAgent = func(_ context.Context, bin string, argv []string, prompt string, _ io.Writer, _ io.Writer) int {
		cap.calls++
		cap.bin = bin
		cap.argv = append([]string(nil), argv...)
		cap.prompt = prompt
		return 0
	}
	t.Cleanup(func() {
		lookPath = prevLook
		spawnAgent = prevSpawn
	})
	return cap
}

// TestBootstrap_DryRunPrintsPlan: --dry-run renders the spawn argv +
// the prompt template and never invokes the real agent.
func TestBootstrap_DryRunPrintsPlan(t *testing.T) {
	cap := stubBootstrap(t, nil)
	app, out, _, _ := newApp(t)
	rc := app.Run([]string{"bootstrap", "--dry-run"})
	if rc != 0 {
		t.Fatalf("dry-run exit = %d, want 0", rc)
	}
	got := out.String()
	for _, want := range []string{
		"dry-run plan",
		"agent:   claude",
		"--dangerously-skip-permissions",
		"--print",
		"# clawtool bootstrap",
		"OnboardWizard",
		"InitApply",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if cap.calls != 0 {
		t.Errorf("dry-run must not spawn the agent; got %d calls", cap.calls)
	}
}

// TestBootstrap_AbortsOnMissingAgent: when LookPath misses the binary,
// the verb prints a helpful install hint and exits 2.
func TestBootstrap_AbortsOnMissingAgent(t *testing.T) {
	cap := stubBootstrap(t, errors.New("not found"))
	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"bootstrap"})
	if rc != 2 {
		t.Fatalf("missing-agent exit = %d, want 2", rc)
	}
	got := errb.String()
	for _, want := range []string{
		"claude not found on PATH",
		"@anthropic-ai/claude-code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if cap.calls != 0 {
		t.Errorf("must not spawn when LookPath misses; got %d calls", cap.calls)
	}
}

// TestBootstrap_BuildsExpectedArgv: the spawn argv for claude must
// include the elevation flag + --print + --output-format text.
func TestBootstrap_BuildsExpectedArgv(t *testing.T) {
	cap := stubBootstrap(t, nil)
	app, _, _, _ := newApp(t)
	rc := app.Run([]string{"bootstrap"})
	if rc != 0 {
		t.Fatalf("bootstrap exit = %d, want 0", rc)
	}
	if cap.calls != 1 {
		t.Fatalf("spawn calls = %d, want 1", cap.calls)
	}
	if cap.bin != "claude" {
		t.Errorf("spawn bin = %q, want %q", cap.bin, "claude")
	}
	joined := strings.Join(cap.argv, " ")
	for _, want := range []string{
		"--dangerously-skip-permissions",
		"--print",
		"--output-format text",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q. got: %v", want, cap.argv)
		}
	}
	if !strings.Contains(cap.prompt, "OnboardWizard") {
		t.Errorf("prompt missing OnboardWizard reference. got:\n%s", cap.prompt)
	}
}

// TestBootstrap_SwitchesElevationFlagPerAgent: --agent gemini must
// flip the spawned binary + elevation flag to the gemini variant.
func TestBootstrap_SwitchesElevationFlagPerAgent(t *testing.T) {
	cases := []struct {
		family   string
		wantBin  string
		wantFlag string
	}{
		{"gemini", "gemini", "--yolo"},
		{"codex", "codex", "--dangerously-bypass-approvals-and-sandbox"},
		{"opencode", "opencode", "--yolo"},
		{"aider", "aider", "--yes-always"},
	}
	for _, tc := range cases {
		t.Run(tc.family, func(t *testing.T) {
			cap := stubBootstrap(t, nil)
			app, _, _, _ := newApp(t)
			rc := app.Run([]string{"bootstrap", "--agent", tc.family})
			if rc != 0 {
				t.Fatalf("bootstrap %s exit = %d, want 0", tc.family, rc)
			}
			if cap.bin != tc.wantBin {
				t.Errorf("%s: bin = %q, want %q", tc.family, cap.bin, tc.wantBin)
			}
			joined := strings.Join(cap.argv, " ")
			if !strings.Contains(joined, tc.wantFlag) {
				t.Errorf("%s: argv missing %q. got: %v", tc.family, tc.wantFlag, cap.argv)
			}
		})
	}
}

// TestBootstrap_RejectsUnknownAgent: --agent foo must usage-error
// without spawning anything.
func TestBootstrap_RejectsUnknownAgent(t *testing.T) {
	cap := stubBootstrap(t, nil)
	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"bootstrap", "--agent", "foo"})
	if rc != 2 {
		t.Fatalf("unknown-agent exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "unknown agent") {
		t.Errorf("stderr should explain unknown agent; got %q", errb.String())
	}
	if cap.calls != 0 {
		t.Errorf("must not spawn for unknown agent; got %d calls", cap.calls)
	}
}
