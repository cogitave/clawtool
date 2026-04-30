package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeClaudeHomeForAgent redirects agent-install paths to a
// tempdir so `clawtool agent new` doesn't pollute ~/.claude.
// Sister of withFakeClaudeHomeForCLI in skill_test.go.
func withFakeClaudeHomeForAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_HOME", dir)
	return dir
}

// TestAgentNew_DryRunDoesNotWrite confirms `--dry-run` previews
// the scaffold without creating <name>.md or the agents directory.
// Symmetric with TestSkillNew_DryRunDoesNotWrite (44a9819) and
// TestRulesNew_DryRunPreview (5824012).
func TestAgentNew_DryRunDoesNotWrite(t *testing.T) {
	dir := withFakeClaudeHomeForAgent(t)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.runAgentNew([]string{
		"preview-agent",
		"--description", "preview only",
		"--tools", "Bash, Read",
		"--instance", "claude",
		"--model", "sonnet",
		"--dry-run",
	})
	if rc != 0 {
		t.Fatalf("dry-run rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{
		"(dry-run)",
		"would create",
		"preview-agent",
		"description: preview only",
		"tools:",
		"Bash",
		"Read",
		"instance:",
		"claude",
		"model:",
		"sonnet",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing %q\n--- output ---\n%s", want, body)
		}
	}
	// Real-write success verb must NOT appear.
	if strings.Contains(body, "✓ agent →") {
		t.Errorf("dry-run leaked success verb: %q", body)
	}

	// Critical: nothing on disk.
	agentFile := filepath.Join(dir, "agents", "preview-agent.md")
	if _, err := os.Stat(agentFile); err == nil {
		t.Errorf("agent file should not exist after dry-run; got %s", agentFile)
	}
}

// TestAgentNew_DryRunRequiresDescription keeps the validation
// error path intact when --dry-run is in play. Operators catch
// missing required fields at preview time, not after the typo
// commits.
func TestAgentNew_DryRunRequiresDescription(t *testing.T) {
	withFakeClaudeHomeForAgent(t)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.runAgentNew([]string{"missing-desc", "--dry-run"})
	if rc != 2 {
		t.Errorf("dry-run without --description rc=%d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "--description is required") {
		t.Errorf("expected description-required error, got: %q", errb.String())
	}
	if strings.Contains(out.String(), "(dry-run)") {
		t.Errorf("dry-run banner leaked despite validation error: %q", out.String())
	}
}

// TestAgentNew_DryRunRefusesExistingWithoutForce preserves the
// exit-1 + "already exists" behaviour even on the dry-run path.
// The operator discovers the conflict at preview time.
func TestAgentNew_DryRunRefusesExistingWithoutForce(t *testing.T) {
	withFakeClaudeHomeForAgent(t)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	// Seed an existing agent (real write).
	if rc := app.runAgentNew([]string{"existing", "--description", "first"}); rc != 0 {
		t.Fatalf("seed rc=%d", rc)
	}

	// Dry-run without --force must refuse.
	out.Reset()
	errb.Reset()
	rc := app.runAgentNew([]string{"existing", "--description", "second", "--dry-run"})
	if rc != 1 {
		t.Errorf("dry-run on existing without --force rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "already exists") {
		t.Errorf("expected already-exists error, got: %q", errb.String())
	}

	// With --force the dry-run runs but flags it as "would overwrite".
	out.Reset()
	errb.Reset()
	if rc := app.runAgentNew([]string{"existing", "--description", "third", "--force", "--dry-run"}); rc != 0 {
		t.Fatalf("dry-run --force rc=%d, stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "would overwrite") {
		t.Errorf("expected 'would overwrite' verb on --force dry-run, got: %q", out.String())
	}
}

// TestAgentNew_RealStillWorks confirms the existing real-write
// path is unaffected by the dry-run plumbing.
func TestAgentNew_RealStillWorks(t *testing.T) {
	dir := withFakeClaudeHomeForAgent(t)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	if rc := app.runAgentNew([]string{"smoke", "--description", "real write"}); rc != 0 {
		t.Fatalf("real rc=%d, stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "✓ agent →") {
		t.Errorf("missing success banner: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "smoke.md")); err != nil {
		t.Errorf("agent file should exist after real-write: %v", err)
	}
}
