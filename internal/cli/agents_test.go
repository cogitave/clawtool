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

// TestAgentsList_JSONOutput confirms `agents list --json` emits a
// JSON array of {name, detected} entries — the wire contract for
// shell pipelines that want the registered-adapters roster without
// parsing the human table.
func TestAgentsList_JSONOutput(t *testing.T) {
	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "list", "--json"}); rc != 0 {
		t.Fatalf("list --json rc=%d", rc)
	}
	var got []struct {
		Name     string `json:"name"`
		Detected bool   `json:"detected"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, out.String())
	}
	if len(got) == 0 {
		t.Fatal("expected at least one entry (claude-code adapter is registered)")
	}
	// Required keys must appear in the literal output (catches
	// accidental field-name divergence between code and tag).
	body := out.String()
	for _, key := range []string{`"name"`, `"detected"`} {
		if !strings.Contains(body, key) {
			t.Errorf("JSON output missing required key %s; body: %s", key, body)
		}
	}
	// claude-code must be in the array.
	found := false
	for _, e := range got {
		if e.Name == "claude-code" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("claude-code missing from JSON list: %+v", got)
	}
}

// TestAgentsList_JSONStableShape confirms the JSON path produces an
// ARRAY (not an object) so `jq '.[]'` consumers stay uniform with
// `agents status --json`.
func TestAgentsList_JSONStableShape(t *testing.T) {
	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "list", "--json"}); rc != 0 {
		t.Fatalf("list --json rc=%d", rc)
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '[' {
		t.Errorf("expected output to start with '[' (array); got: %q", body)
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

// TestAgentsClaim_JSONOutput emits an agents.Plan as indented
// JSON when --json is set. Drives the wire contract for
// automation pipelines that log claim events structurally
// (snake_case keys via the JSON tags on the agents.Plan struct).
func TestAgentsClaim_JSONOutput(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "claim", "claude-code", "--json"}); rc != 0 {
		t.Fatalf("claim --json rc=%d, stdout=%s", rc, out.String())
	}

	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("expected JSON object; got: %q", body)
	}
	for _, lit := range []string{`"adapter":`, `"action":`, `"settings_path":`, `"marker_path":`, `"tools_added":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got agents.Plan
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if got.Adapter != "claude-code" {
		t.Errorf("Adapter = %q, want claude-code", got.Adapter)
	}
	if got.Action != "claim" {
		t.Errorf("Action = %q, want claim", got.Action)
	}
	if got.WasNoop {
		t.Error("WasNoop should be false on first claim")
	}
	if len(got.ToolsAdded) == 0 {
		t.Error("ToolsAdded should not be empty after a fresh claim")
	}
}

// TestAgentsRelease_JSONOutput exercises the inverse path:
// after claim, release --json must report Action=release with
// ToolsRemoved populated. Same wire contract as claim.
func TestAgentsRelease_JSONOutput(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	// Seed a claim so release isn't a noop.
	if rc := app.Run([]string{"agents", "claim", "claude-code"}); rc != 0 {
		t.Fatalf("seed claim rc=%d", rc)
	}
	out.Reset()

	if rc := app.Run([]string{"agents", "release", "claude-code", "--json"}); rc != 0 {
		t.Fatalf("release --json rc=%d", rc)
	}
	var got agents.Plan
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, out.String())
	}
	if got.Action != "release" {
		t.Errorf("Action = %q, want release", got.Action)
	}
	if len(got.ToolsRemoved) == 0 {
		t.Error("ToolsRemoved should not be empty after a paired release")
	}
}

// TestAgentsClaim_JSONDryRun confirms `--dry-run --json` carries
// the dry_run bit through the JSON wire so a script can branch
// on it without parsing human output. Also confirms the plan
// shape doesn't change between dry-run and real runs.
func TestAgentsClaim_JSONDryRun(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "claim", "claude-code", "--dry-run", "--json"}); rc != 0 {
		t.Fatalf("dry-run --json rc=%d", rc)
	}
	var got agents.Plan
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, out.String())
	}
	if !got.DryRun {
		t.Error("DryRun = false, want true on --dry-run path")
	}
	if got.Action != "claim" {
		t.Errorf("Action = %q, want claim", got.Action)
	}
}

// TestAgentsClaim_JSONStableShape confirms output is an OBJECT
// (single result), not an array — claim/release act on one
// adapter at a time. `jq '.action'` consumers rely on object
// shape.
func TestAgentsClaim_JSONStableShape(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "claim", "claude-code", "--json"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Errorf("expected object (starts with '{'); got: %q", body)
	}
}

// TestAgentsDetect_ClaimedReturnsZero exercises the happy
// installer path: claude-code is on PATH (Detected=true) AND
// has been claimed → exit 0, banner says "detected and claimed".
func TestAgentsDetect_ClaimedReturnsZero(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, _, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "claim", "claude-code"}); rc != 0 {
		t.Fatalf("seed claim rc=%d", rc)
	}

	out := &bytes.Buffer{}
	app.Stdout = out
	rc := app.Run([]string{"agents", "detect", "claude-code"})
	if rc != 0 {
		t.Errorf("detect rc=%d, want 0 (detected+claimed)", rc)
	}
	if !strings.Contains(out.String(), "detected and claimed") {
		t.Errorf("banner missing detected+claimed phrasing: %q", out.String())
	}
}

// TestAgentsDetect_DetectedNotClaimedReturnsOne stands up a fresh
// claude-code adapter (settings dir exists so Detected=true) but
// skips the claim step → exit 1, banner suggests `agents claim`.
func TestAgentsDetect_DetectedNotClaimedReturnsOne(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, out, _ := newAgentsApp(t)
	rc := app.Run([]string{"agents", "detect", "claude-code"})
	if rc != 1 {
		t.Errorf("detect rc=%d, want 1 (detected, not claimed)", rc)
	}
	if !strings.Contains(out.String(), "detected but NOT claimed") {
		t.Errorf("banner missing detected-not-claimed phrasing: %q", out.String())
	}
	if !strings.Contains(out.String(), "clawtool agents claim") {
		t.Errorf("banner should suggest the next step: %q", out.String())
	}
}

// TestAgentsDetect_NotDetectedReturnsTwo points the adapter at a
// settings path whose parent directory doesn't exist → Detected
// returns false → exit 2.
func TestAgentsDetect_NotDetectedReturnsTwo(t *testing.T) {
	// Directory that definitely doesn't exist. The claudecode
	// adapter checks parent dir existence; we use a path under
	// a non-existent root so Stat fails.
	missing := filepath.Join(t.TempDir(), "no-such-dir", "settings.json")
	agents.SetClaudeCodeSettingsPath(missing)
	t.Cleanup(func() { agents.SetClaudeCodeSettingsPath("") })

	app, out, _ := newAgentsApp(t)
	rc := app.Run([]string{"agents", "detect", "claude-code"})
	if rc != 2 {
		t.Errorf("detect rc=%d, want 2 (not detected)", rc)
	}
	if !strings.Contains(out.String(), "not detected") {
		t.Errorf("banner missing not-detected phrasing: %q", out.String())
	}
}

// TestAgentsDetect_JSONOutput emits a structured payload whose
// exit_code field matches the process exit code. Pipelines that
// log the probe AND branch on it can use the same JSON without
// double-invoking.
func TestAgentsDetect_JSONOutput(t *testing.T) {
	_, cleanup := withTmpClaudeSettings(t)
	defer cleanup()

	app, _, _ := newAgentsApp(t)
	if rc := app.Run([]string{"agents", "claim", "claude-code"}); rc != 0 {
		t.Fatalf("seed claim rc=%d", rc)
	}

	out := &bytes.Buffer{}
	app.Stdout = out
	rc := app.Run([]string{"agents", "detect", "claude-code", "--json"})
	if rc != 0 {
		t.Errorf("detect --json rc=%d, want 0", rc)
	}
	body := out.String()
	for _, lit := range []string{`"adapter":`, `"detected":`, `"claimed":`, `"exit_code":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got struct {
		Adapter  string `json:"adapter"`
		Detected bool   `json:"detected"`
		Claimed  bool   `json:"claimed"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if got.Adapter != "claude-code" {
		t.Errorf("adapter = %q, want claude-code", got.Adapter)
	}
	if !got.Detected {
		t.Error("detected should be true on a seeded settings dir")
	}
	if !got.Claimed {
		t.Error("claimed should be true after seed claim")
	}
	if got.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0 (matches rc)", got.ExitCode)
	}
}

// TestAgentsDetect_UnknownAgent rejects names not in the
// adapter registry — same exit-1 + stderr-list-known shape as
// claim/release, so installer scripts that catch this case work
// uniformly across the agents subcommand surface.
func TestAgentsDetect_UnknownAgent(t *testing.T) {
	app, _, errb := newAgentsApp(t)
	rc := app.Run([]string{"agents", "detect", "not-real"})
	if rc != 1 {
		t.Errorf("rc=%d, want 1 (unknown agent)", rc)
	}
	if !strings.Contains(errb.String(), "unknown agent") {
		t.Errorf("expected 'unknown agent' in stderr; got %q", errb.String())
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
