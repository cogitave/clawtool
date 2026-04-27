package telemetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

func TestNew_DisabledIsNoop(t *testing.T) {
	c := New(config.TelemetryConfig{Enabled: false})
	if c.Enabled() {
		t.Error("disabled config should yield Enabled=false")
	}
	c.Track("anything", map[string]any{"command": "cli"})
	_ = c.Close()
}

func TestNew_NoAPIKeyIsNoop(t *testing.T) {
	c := New(config.TelemetryConfig{Enabled: true})
	if c.Enabled() {
		t.Error("enabled without APIKey should still be no-op")
	}
}

func TestNilClient_TrackSafe(t *testing.T) {
	var c *Client
	c.Track("smoke", nil) // must not panic
	if c.Enabled() {
		t.Error("nil client cannot be enabled")
	}
	if err := c.Close(); err != nil {
		t.Errorf("nil Close should be no-op; got %v", err)
	}
}

func TestSilentDisabled(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     true,
		"false": true,
		"off":   true,
		"1":     false,
	}
	for v, want := range cases {
		t.Setenv("CLAWTOOL_TELEMETRY", v)
		if got := SilentDisabled(); got != want {
			t.Errorf("SilentDisabled(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestAnonymousID_StableAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	a, err := loadOrCreateAnonymousID()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 {
		t.Errorf("ID should be 32 hex chars; got %d", len(a))
	}
	b, err := loadOrCreateAnonymousID()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("loadOrCreateAnonymousID should be stable across calls")
	}
	// File mode should be 0600.
	info, err := os.Stat(filepath.Join(dir, "clawtool", "telemetry-id"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("telemetry-id mode: got %v, want 0600", info.Mode().Perm())
	}
}

func TestSetGetGlobal(t *testing.T) {
	old := Get()
	t.Cleanup(func() { SetGlobal(old) })
	c := New(config.TelemetryConfig{Enabled: false})
	SetGlobal(c)
	if Get() != c {
		t.Error("SetGlobal/Get round-trip mismatch")
	}
	SetGlobal(nil)
	if Get() != nil {
		t.Error("SetGlobal(nil) should clear")
	}
}

func TestAllowedKeys_FilterStrips(t *testing.T) {
	for _, k := range []string{"command", "version", "duration_ms", "exit_code"} {
		if !allowedKeys[k] {
			t.Errorf("key %q should be allowed", k)
		}
	}
	for _, k := range []string{"prompt", "path", "secret", "instance", "file_content"} {
		if allowedKeys[k] {
			t.Errorf("key %q must be filtered (potential PII)", k)
		}
	}
}
