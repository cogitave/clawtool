package agents

import (
	"os"
	"path/filepath"
	"testing"
)

// withGenericAdapterTempPath retargets one of the registered
// generic adapters at a tempdir-rooted settings.json. Returns the
// directory + the settings file path; cleanup restores the
// override.
func withGenericAdapterTempPath(t *testing.T, name string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	prev := ""
	for _, ad := range Registry {
		if g, ok := ad.(*genericAdapter); ok && g.name == name {
			prev = g.pathOverride
			g.pathOverride = settings
			t.Cleanup(func() { g.pathOverride = prev })
			return dir, settings
		}
	}
	t.Fatalf("generic adapter %q not registered", name)
	return "", ""
}

func TestHermesAgent_Registered(t *testing.T) {
	ad, err := Find("hermes-agent")
	if err != nil {
		t.Fatalf("Find(hermes-agent): %v", err)
	}
	if ad.Name() != "hermes-agent" {
		t.Errorf("Name = %q", ad.Name())
	}
}

func TestOpenclaw_Registered(t *testing.T) {
	ad, err := Find("openclaw")
	if err != nil {
		t.Fatalf("Find(openclaw): %v", err)
	}
	if ad.Name() != "openclaw" {
		t.Errorf("Name = %q", ad.Name())
	}
}

func TestGenericAdapter_DetectsConfigDirAndClaims(t *testing.T) {
	_, settings := withGenericAdapterTempPath(t, "hermes-agent")

	// Pre-create the config dir so Detected returns true.
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}

	ad, _ := Find("hermes-agent")
	plan, err := ad.Claim(Options{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if plan.WasNoop {
		t.Error("first Claim should not be a no-op")
	}
	if len(plan.ToolsAdded) != len(ClaimedToolsForClawtool) {
		t.Errorf("ToolsAdded count = %d, want %d", len(plan.ToolsAdded), len(ClaimedToolsForClawtool))
	}

	// File should now contain the disabled_tools field with our list.
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if got := string(body); got == "" {
		t.Error("settings.json empty after Claim")
	}
}

func TestGenericAdapter_ReleaseRoundTrip(t *testing.T) {
	_, settings := withGenericAdapterTempPath(t, "openclaw")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	ad, _ := Find("openclaw")
	if _, err := ad.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.Release(Options{}); err != nil {
		t.Fatal(err)
	}
	s, _ := ad.Status()
	if s.Claimed {
		t.Error("after Release, Status.Claimed should be false")
	}
}

func TestGenericAdapter_DetectFalseWhenConfigAbsent(t *testing.T) {
	dir, _ := withGenericAdapterTempPath(t, "hermes-agent")
	// Re-target the override to a path under a sub-directory we
	// never create, so Detected (which checks the parent dir's
	// existence) returns false.
	for _, ad := range Registry {
		if g, ok := ad.(*genericAdapter); ok && g.name == "hermes-agent" {
			g.pathOverride = filepath.Join(dir, "no-such-dir", "settings.json")
		}
	}
	ad, _ := Find("hermes-agent")
	s, _ := ad.Status()
	if s.Detected {
		t.Errorf("Status.Detected should be false when config dir is absent; got %+v", s)
	}
}
