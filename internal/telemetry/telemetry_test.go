package telemetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

func TestNew_DisabledIsNoop(t *testing.T) {
	// Pre-v1.0 lock: when version.Resolved() reports major=0
	// (the project's current state), New() overrides
	// Enabled=false → true and surfaces a stderr warning. This
	// test runs under the dev-build path where version.Resolved
	// returns "(devel)" / a tag-derived "0.x.y" — both trigger
	// the lock. We therefore assert the OPPOSITE of the
	// pre-policy contract: a disabled config yields an enabled
	// client. When v1.0.0 ships, majorIsZero returns false and
	// the test will need to flip back. The post-v1 expectation
	// is locked in TestNew_DisabledIsNoop_PostV1 below (driven
	// by a swapped versionResolved hook).
	c := New(config.TelemetryConfig{Enabled: false})
	if !c.Enabled() {
		t.Error("pre-v1.0 policy: disabled config must be force-overridden to enabled")
	}
	c.Track("anything", map[string]any{"command": "cli"})
	_ = c.Close()
}

func TestNew_DisabledIsNoop_PostV1(t *testing.T) {
	// Simulate the post-v1.0 world by swapping the version-resolver
	// hook. Once we tag v1.0.0 the regular path takes over and the
	// pre-v1 override branch returns false, so a disabled config
	// produces a disabled client (the original contract).
	orig := versionResolved
	versionResolved = func() string { return "v1.0.0" }
	t.Cleanup(func() { versionResolved = orig })

	c := New(config.TelemetryConfig{Enabled: false})
	if c.Enabled() {
		t.Error("post-v1.0: disabled config must produce a disabled client")
	}
	_ = c.Close()
}

func TestMajorIsZero(t *testing.T) {
	cases := map[string]bool{
		"v0.22.35":    true,
		"0.22.35":     true,
		"0.0.0-old":   true,
		"v1.0.0":      false,
		"v1.2.3-rc.4": false,
		"2.5.1":       false,
		"(devel)":     false,
		"(unknown)":   false,
		"":            false,
		"garbage":     false,
		"99":          false, // no dot — unparseable
	}
	for in, want := range cases {
		if got := majorIsZero(in); got != want {
			t.Errorf("majorIsZero(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNew_NoAPIKeyFallsBackToBakedDefault(t *testing.T) {
	// New behaviour: empty APIKey + Enabled=true falls back to the
	// baked-in cogitave PostHog project key. Same convention as
	// posthog-js shipping a public client-side key. Operators
	// override by setting their own [telemetry] api_key.
	c := New(config.TelemetryConfig{Enabled: true})
	if !c.Enabled() {
		t.Error("Enabled=true with no APIKey should fall back to the embedded default and produce an enabled client")
	}
	_ = c.Close()
}

func TestNew_OperatorAPIKeyOverridesBakedDefault(t *testing.T) {
	c := New(config.TelemetryConfig{Enabled: true, APIKey: "phc_operator_override"})
	if !c.Enabled() {
		t.Error("explicit operator APIKey should produce an enabled client")
	}
	_ = c.Close()
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
	for _, k := range []string{"command", "version", "duration_ms", "exit_code", "install_method"} {
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

func TestAllowedKeys_PostHogSessionConventions(t *testing.T) {
	// $session_id + $lib + $lib_version are PostHog-reserved
	// property names that light up the Sessions view and
	// session-bound funnel queries. Stripping them silently
	// (the pre-fix behaviour) was the root cause of the
	// "sessions tab is empty even though events are flowing"
	// observation on 2026-04-29.
	for _, k := range []string{"$session_id", "$lib", "$lib_version"} {
		if !allowedKeys[k] {
			t.Errorf("PostHog convention key %q must be allowed (Sessions view depends on it)", k)
		}
	}
}

func TestNewSessionID_UniquePerCall(t *testing.T) {
	// Each call must produce a fresh ID so two concurrent
	// daemons (or a daemon + a one-shot CLI) don't collide
	// into the same Sessions-view row.
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newSessionID()
		if id == "" {
			t.Fatalf("empty session ID")
		}
		if len(id) < 16 {
			t.Fatalf("session ID too short: %q", id)
		}
		if seen[id] {
			t.Fatalf("session ID collision: %q (iteration %d)", id, i)
		}
		seen[id] = true
	}
}

func TestDetectInstallMethod_KnownTaxonomy(t *testing.T) {
	// Isolate from the host's install-method file (install.sh
	// writes one under ~/.config/clawtool/install-method when
	// the user installed via the script). The file-fallback in
	// detectInstallMethod would otherwise leak the host's value
	// into the test and break the empty-input → "unknown" case.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cases := map[string]string{
		"script":     "script",
		"brew":       "brew",
		"go-install": "go-install",
		"release":    "release",
		"docker":     "docker",
		"manual":     "manual",
		"  Brew  ":   "brew", // trim+lowercase
		"":           "unknown",
		"random":     "unknown",
	}
	for in, want := range cases {
		t.Setenv("CLAWTOOL_INSTALL_METHOD", in)
		if got := detectInstallMethod(); got != want {
			t.Errorf("detectInstallMethod(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmitInstallOnce_WritesMarkerOnFirstCall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("CLAWTOOL_INSTALL_METHOD", "release")

	c := New(config.TelemetryConfig{Enabled: true})
	defer c.Close()
	if !c.Enabled() {
		t.Skip("Enabled=true should produce a real client; skipping if posthog SDK refused init")
	}

	EmitInstallOnce(c, "v9.9.9-test")

	markerPath := filepath.Join(dir, "clawtool", "install-emitted")
	info, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("install-emitted marker not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("marker mode: got %v, want 0600", info.Mode().Perm())
	}
}

func TestEmitInstallOnce_NoOpAfterMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dir, "clawtool", "install-emitted")
	if err := os.WriteFile(markerPath, []byte("pre-existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := New(config.TelemetryConfig{Enabled: true})
	defer c.Close()
	if !c.Enabled() {
		t.Skip("client not enabled; skipping")
	}

	EmitInstallOnce(c, "v9.9.9-test")

	// Marker contents should NOT have been overwritten — proves
	// the function detected the marker and bailed.
	got, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "pre-existing\n" {
		t.Errorf("marker overwritten: got %q, want pre-existing", got)
	}
}

func TestEmitInstallOnce_NilClientSafe(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	EmitInstallOnce(nil, "v0.0.0")

	if _, err := os.Stat(filepath.Join(dir, "clawtool", "install-emitted")); err == nil {
		t.Error("nil client should NOT write the marker — would dedupe a real install event later")
	}
}
