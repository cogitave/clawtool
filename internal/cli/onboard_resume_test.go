package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOnboardProgress_RoundTrip confirms save → load returns the
// same state + step index. The on-disk JSON is what survives a
// terminal close mid-wizard, so the round-trip is the contract.
func TestOnboardProgress_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	state := onboardState{
		Found:          map[string]bool{"claude": true, "codex": true},
		MissingBridges: []string{"gemini"},
		PrimaryCLI:     "codex",
		StartDaemon:    true,
		CreateIdentity: false,
		InitSecrets:    true,
		Telemetry:      false,
	}
	if err := saveOnboardProgress(3, &state, "v0.22.39"); err != nil {
		t.Fatalf("saveOnboardProgress: %v", err)
	}
	loaded, err := loadOnboardProgress()
	if err != nil {
		t.Fatalf("loadOnboardProgress: %v", err)
	}
	if loaded == nil {
		t.Fatalf("loaded progress is nil")
	}
	if loaded.StepIdx != 3 {
		t.Errorf("StepIdx = %d, want 3", loaded.StepIdx)
	}
	if loaded.State.PrimaryCLI != "codex" {
		t.Errorf("PrimaryCLI = %q, want codex", loaded.State.PrimaryCLI)
	}
	if loaded.State.Telemetry {
		t.Errorf("Telemetry = true, want false")
	}
	if loaded.ClawtoolVersion != "v0.22.39" {
		t.Errorf("ClawtoolVersion = %q, want v0.22.39", loaded.ClawtoolVersion)
	}

	// File must be 0600 — the state can include identity hints
	// or telemetry preferences the operator hasn't ratified yet.
	info, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "clawtool", ".onboard-progress.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("progress file perm = %v, want 0600", perm)
	}
}

// TestOnboardProgress_LoadAbsentReturnsNil confirms a missing
// progress file is reported as (nil, nil) — the caller treats this
// as "fresh wizard, no resume prompt needed".
func TestOnboardProgress_LoadAbsentReturnsNil(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, err := loadOnboardProgress()
	if err != nil {
		t.Fatalf("expected nil error for missing file; got %v", err)
	}
	if p != nil {
		t.Errorf("expected nil progress; got %+v", p)
	}
}

// TestOnboardProgress_LoadCorruptReturnsError confirms a malformed
// JSON file surfaces an error so the caller can warn + start fresh
// rather than silently masking corruption.
func TestOnboardProgress_LoadCorruptReturnsError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "clawtool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".onboard-progress.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOnboardProgress(); err == nil {
		t.Error("expected error parsing corrupt progress file")
	}
}

// TestOnboardProgress_LoadSchemaMismatchReturnsError confirms a
// schema-version mismatch is surfaced as an error so the caller
// starts the wizard from scratch instead of crashing midway.
func TestOnboardProgress_LoadSchemaMismatchReturnsError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "clawtool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Schema = 999 will never equal current onboardProgressSchema.
	if err := os.WriteFile(
		filepath.Join(dir, ".onboard-progress.json"),
		[]byte(`{"schema_version":999,"step_idx":2,"state":{},"saved_at":"2026-04-30T00:00:00Z"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOnboardProgress(); err == nil {
		t.Error("expected schema mismatch error")
	}
}

// TestOnboardProgress_ClearIsIdempotent confirms clearOnboardProgress
// returns nil whether or not the file existed. The wizard's finish
// path calls it unconditionally, so it must not error on a fresh
// machine.
func TestOnboardProgress_ClearIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := clearOnboardProgress(); err != nil {
		t.Errorf("clear on missing file: %v", err)
	}
	state := onboardState{Found: map[string]bool{}}
	if err := saveOnboardProgress(1, &state, "v0.22.39"); err != nil {
		t.Fatal(err)
	}
	if err := clearOnboardProgress(); err != nil {
		t.Errorf("clear on existing file: %v", err)
	}
	if err := clearOnboardProgress(); err != nil {
		t.Errorf("clear after delete: %v", err)
	}
}

// TestOnboardModel_StartStepClampsOutOfRange confirms a stale
// progress file with a step index past the current wizard's step
// list resets to step 0 instead of pushing the cursor off the end.
func TestOnboardModel_StartStepClampsOutOfRange(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		MissingBridges: nil,
		MCPClaimable:   nil,
	}
	m := newOnboardModelAt(&state, onboardDeps{}, func(string, map[string]any) {}, 999)
	if m.stepIdx != 0 {
		t.Errorf("stepIdx = %d, want 0 (clamped)", m.stepIdx)
	}
}

// TestOnboardModel_StartStepResumesMidWizard confirms a valid
// in-range startStep lands the wizard on that step.
func TestOnboardModel_StartStepResumesMidWizard(t *testing.T) {
	state := onboardState{
		Found:          map[string]bool{"claude": true},
		MissingBridges: nil,
		MCPClaimable:   nil,
	}
	m := newOnboardModelAt(&state, onboardDeps{}, func(string, map[string]any) {}, 2)
	if m.stepIdx != 2 {
		t.Errorf("stepIdx = %d, want 2 (resumed)", m.stepIdx)
	}
}
