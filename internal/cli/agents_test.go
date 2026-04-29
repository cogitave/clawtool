package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/agents"
)

// withTmpClaudeSettings redirects the claude-code adapter to a tmp
// settings.json so tests don't touch the real one. Returns settings
// path and a cleanup func.
func withTmpClaudeSettings(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)
	return settings, func() { agents.SetClaudeCodeSettingsPath("") }
}

func newAgentsApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	return &App{Stdout: out, Stderr: errb}, out, errb
}

func TestAgentsList_HasClaudeCode(t *testing.T) {
	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "list"}); rc != 0 {
		t.Fatalf("agents list rc=%d", rc)
	}
	if !strings.Contains(out.String(), "claude-code") {
		t.Errorf("agents list missing claude-code: %s", out.String())
	}
}

func TestAgentsClaim_AddsToolsToSettings(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	rc := app.Run([]string{"agents", "claim", "claude-code"})
	if rc != 0 {
		t.Fatalf("claim rc=%d", rc)
	}
	got := out.String()
	if !strings.Contains(got, "claimed claude-code") {
		t.Errorf("claim did not confirm: %s", got)
	}
	for _, tool := range agents.ClaimedToolsForClawtool {
		if !strings.Contains(got, tool) {
			t.Errorf("claim output missing %q: %s", tool, got)
		}
	}
}

func TestAgentsClaim_DryRunDoesNotWrite(t *testing.T) {
	settings, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	rc := app.Run([]string{"agents", "claim", "claude-code", "--dry-run"})
	if rc != 0 {
		t.Fatalf("claim dry-run rc=%d", rc)
	}
	if !strings.Contains(out.String(), "(dry-run)") {
		t.Errorf("output should mark dry-run: %s", out.String())
	}
	// settings.json should not have been created on dry-run.
	if _, err := exists(settings); err == nil {
		t.Errorf("settings.json should not exist after dry-run")
	}
}

func TestAgentsRelease_AfterClaim_NoopWithoutMarker(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "release", "claude-code"}); rc != 0 {
		t.Fatalf("release rc=%d", rc)
	}
	if !strings.Contains(out.String(), "no changes needed") {
		t.Errorf("release without prior claim should be noop: %s", out.String())
	}
}

func TestAgentsClaimReleaseRoundTrip(t *testing.T) {
	settings, cleanup := withTmpClaudeSettings(t)
	defer cleanup()
	_ = settings

	app, out, _ := newAgentsApp(t)

	if rc := app.Run([]string{"agents", "claim", "claude-code"}); rc != 0 {
		t.Fatalf("claim rc=%d", rc)
	}
	out.Reset()

	if rc := app.Run([]string{"agents", "release", "claude-code"}); rc != 0 {
		t.Fatalf("release rc=%d", rc)
	}
	if !strings.Contains(out.String(), "released claude-code") {
		t.Errorf("release confirmation missing: %s", out.String())
	}
	out.Reset()

	// Status now should show not claimed.
	if rc := app.Run([]string{"agents", "status", "claude-code"}); rc != 0 {
		t.Fatalf("status rc=%d", rc)
	}
	// Status renders as a table — `claude-code ... no` line means not claimed.
	if !strings.Contains(out.String(), "claude-code") {
		t.Errorf("status missing claude-code row: %s", out.String())
	}
}

func TestAgentsClaim_UnknownAgent(t *testing.T) {
	app, _, errb := newAgentsApp(t)
	rc := app.Run([]string{"agents", "claim", "not-real"})
	if rc != 1 {
		t.Errorf("unknown agent rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "unknown agent") {
		t.Errorf("expected 'unknown agent' message: %s", errb.String())
	}
	if !strings.Contains(errb.String(), "claude-code") {
		t.Errorf("error should list known adapters: %s", errb.String())
	}
}

func TestAgents_NoSubcommandPrintsUsage(t *testing.T) {
	app, _, errb := newAgentsApp(t)
	rc := app.Run([]string{"agents"})
	if rc != 2 {
		t.Errorf("rc=%d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "agents claim") {
		t.Errorf("usage should describe claim: %s", errb.String())
	}
}

// exists is a small helper used only by tests; returns (true, nil)
// when the path exists, (false, err) when it doesn't.
func exists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else {
		return false, err
	}
}
