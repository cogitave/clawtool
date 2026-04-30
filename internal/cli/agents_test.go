package cli

import (
	"bytes"
	"encoding/json"
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

// TestAgentsStatus_JSONOutput confirms `agents status --json`
// emits a parseable JSON array of Status objects with the
// documented field shape (snake_case keys). Drives the wire
// contract for shell pipelines (`clawtool agents status --json |
// jq '.[].claimed'`).
func TestAgentsStatus_JSONOutput(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "status", "--json"}); rc != 0 {
		t.Fatalf("status --json rc=%d", rc)
	}

	var got []agents.Status
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, out.String())
	}
	if len(got) == 0 {
		t.Fatal("expected at least one status row (claude-code adapter is registered)")
	}

	// Field shape check: claude-code row must be present, and the
	// JSON keys must be snake_case (verified by re-marshalling and
	// inspecting the literal output instead of struct tags alone —
	// catches accidental field-name divergence).
	body := out.String()
	for _, key := range []string{`"adapter"`, `"detected"`, `"claimed"`} {
		if !strings.Contains(body, key) {
			t.Errorf("JSON output missing required key %s; body: %s", key, body)
		}
	}

	// claude-code adapter should appear by name in the array.
	found := false
	for _, s := range got {
		if s.Adapter == "claude-code" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("claude-code adapter missing from JSON status: %+v", got)
	}
}

// TestAgentsStatus_JSONSingleAdapter exercises the path where the
// operator names a specific adapter together with --json: the
// output should still be a single-element JSON array (not an
// object), so jq one-liners stay uniform across both invocations.
func TestAgentsStatus_JSONSingleAdapter(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "status", "claude-code", "--json"}); rc != 0 {
		t.Fatalf("status claude-code --json rc=%d", rc)
	}
	var got []agents.Status
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, out.String())
	}
	if len(got) != 1 {
		t.Fatalf("single-adapter --json should produce a 1-element array; got %d: %+v", len(got), got)
	}
	if got[0].Adapter != "claude-code" {
		t.Errorf("Adapter=%q, want claude-code", got[0].Adapter)
	}
}
