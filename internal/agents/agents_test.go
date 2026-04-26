package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// withTempSettings creates a temp settings.json + redirects the
// claudeCodeAdapter to use it. Returns paths and a cleanup func.
func withTempSettings(t *testing.T, initial map[string]any) (settingsPath, markerPath string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	settingsPath = filepath.Join(dir, "settings.json")
	markerPath = filepath.Join(dir, "settings.clawtool.lock")
	if initial != nil {
		body, err := json.MarshalIndent(initial, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settingsPath, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prev := claudeCodePathOverride
	claudeCodePathOverride = settingsPath
	cleanup = func() {
		claudeCodePathOverride = prev
	}
	return
}

func loadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func disabledTools(t *testing.T, path string) []string {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	raw := loadJSON(t, path)
	v, ok := raw["disabledTools"]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func TestClaim_AddsClawtoolToolsToEmptySettings(t *testing.T) {
	settings, _, cleanup := withTempSettings(t, nil)
	defer cleanup()

	a := &claudeCodeAdapter{}
	plan, err := a.Claim(Options{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if plan.WasNoop {
		t.Error("first Claim should not be a no-op")
	}
	if len(plan.ToolsAdded) != len(ClaimedToolsForClawtool) {
		t.Errorf("ToolsAdded = %v, want all %d clawtool tools", plan.ToolsAdded, len(ClaimedToolsForClawtool))
	}

	got := disabledTools(t, settings)
	want := append([]string{}, ClaimedToolsForClawtool...)
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Errorf("disabledTools = %v, want %v", got, want)
	}
}

func TestClaim_PreservesUserState(t *testing.T) {
	initial := map[string]any{
		"theme":         "dark",
		"disabledTools": []any{"SomethingTheUserDisabled"},
		"permissions":   map[string]any{"allow": []any{"Bash"}},
	}
	settings, _, cleanup := withTempSettings(t, initial)
	defer cleanup()

	a := &claudeCodeAdapter{}
	if _, err := a.Claim(Options{}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	got := loadJSON(t, settings)
	if got["theme"] != "dark" {
		t.Errorf("theme dropped: %v", got["theme"])
	}
	if _, ok := got["permissions"]; !ok {
		t.Error("permissions field dropped")
	}

	disabled := disabledTools(t, settings)
	hasSomething := false
	for _, d := range disabled {
		if d == "SomethingTheUserDisabled" {
			hasSomething = true
		}
	}
	if !hasSomething {
		t.Errorf("user's disable list lost: %v", disabled)
	}
}

func TestClaim_Idempotent(t *testing.T) {
	settings, _, cleanup := withTempSettings(t, nil)
	defer cleanup()
	a := &claudeCodeAdapter{}

	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	first := disabledTools(t, settings)

	plan2, err := a.Claim(Options{})
	if err != nil {
		t.Fatal(err)
	}
	second := disabledTools(t, settings)

	if !plan2.WasNoop {
		t.Error("second Claim should be a no-op")
	}
	if !equalStrings(first, second) {
		t.Errorf("disabledTools changed across idempotent claims: %v -> %v", first, second)
	}
}

func TestClaim_DryRunWritesNothing(t *testing.T) {
	settings, marker, cleanup := withTempSettings(t, nil)
	defer cleanup()
	a := &claudeCodeAdapter{}

	plan, err := a.Claim(Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DryRun {
		t.Error("plan.DryRun should be true")
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Error("settings.json should not exist after dry-run on empty workspace")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("marker should not exist after dry-run")
	}
}

func TestRelease_RestoresExactly(t *testing.T) {
	initial := map[string]any{
		"disabledTools": []any{"UserKept"},
	}
	settings, _, cleanup := withTempSettings(t, initial)
	defer cleanup()
	a := &claudeCodeAdapter{}

	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	// After Claim: UserKept + ClaimedToolsForClawtool all present.

	if _, err := a.Release(Options{}); err != nil {
		t.Fatal(err)
	}
	got := disabledTools(t, settings)
	if !equalStrings(got, []string{"UserKept"}) {
		t.Errorf("after Release, disabledTools = %v, want [UserKept]", got)
	}
}

func TestRelease_NoMarkerIsNoop(t *testing.T) {
	_, _, cleanup := withTempSettings(t, nil)
	defer cleanup()
	a := &claudeCodeAdapter{}

	plan, err := a.Release(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.WasNoop {
		t.Error("Release without prior Claim must be a no-op")
	}
}

func TestStatus_BeforeAndAfterClaim(t *testing.T) {
	_, _, cleanup := withTempSettings(t, nil)
	defer cleanup()
	a := &claudeCodeAdapter{}

	s1, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s1.Claimed {
		t.Error("Status before Claim should report not claimed")
	}

	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}

	s2, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Claimed {
		t.Error("Status after Claim should report claimed=true")
	}
	if len(s2.DisabledByUs) != len(ClaimedToolsForClawtool) {
		t.Errorf("DisabledByUs count = %d, want %d", len(s2.DisabledByUs), len(ClaimedToolsForClawtool))
	}
}

func TestRegistry_HasClaudeCode(t *testing.T) {
	a, err := Find("claude-code")
	if err != nil {
		t.Fatalf("Find('claude-code'): %v", err)
	}
	if a.Name() != "claude-code" {
		t.Errorf("Name = %q, want claude-code", a.Name())
	}
}

func TestFind_UnknownReturnsSentinel(t *testing.T) {
	_, err := Find("not-a-real-agent")
	if err == nil || err != ErrUnknownAgent {
		t.Errorf("expected ErrUnknownAgent, got %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
