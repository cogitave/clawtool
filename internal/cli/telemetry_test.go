package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// newTestApp returns an App with isolated Stdout/Stderr buffers and
// a config path under a fresh temp dir, so each test stays sealed
// from the host's real ~/.config/clawtool/config.toml.
func newTelemetryTestApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	out, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{
		Stdout:     out,
		Stderr:     errBuf,
		ConfigPath: filepath.Join(dir, "config.toml"),
	}
	return app, out, errBuf
}

// TestTelemetry_StatusPrintsCurrentFlag confirms `status` reads the
// config and prints "on" / "off" + the resolved path.
func TestTelemetry_StatusPrintsCurrentFlag(t *testing.T) {
	app, out, _ := newTelemetryTestApp(t)

	// Initial state: no config on disk → defaults apply.
	rc := app.runTelemetry([]string{"status"})
	if rc != 0 {
		t.Fatalf("status rc=%d", rc)
	}
	got := out.String()
	if !strings.Contains(got, "telemetry:") {
		t.Errorf("status output missing 'telemetry:' label: %q", got)
	}
	if !strings.Contains(got, "config:") {
		t.Errorf("status output missing 'config:' label: %q", got)
	}
}

// TestTelemetry_OnRoundTrip writes the flag through the CLI path
// and reads it back through config.LoadOrDefault — confirms the
// `on` verb's persistence side-effect lands. The `off` verb is
// covered by TestTelemetry_OffLockedPreV1 below; pre-v1.0 it
// refuses with rc=1 + a policy explanation, which is the
// behaviour we want to lock in.
func TestTelemetry_OnRoundTrip(t *testing.T) {
	app, _, _ := newTelemetryTestApp(t)

	if rc := app.runTelemetry([]string{"on"}); rc != 0 {
		t.Fatalf("`on` rc=%d", rc)
	}
	cfg, err := config.LoadOrDefault(app.Path())
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if !cfg.Telemetry.Enabled {
		t.Error("after `telemetry on`, config Telemetry.Enabled must be true")
	}
}

// TestTelemetry_OffLockedPreV1 asserts the policy: pre-v1.0,
// `clawtool telemetry off` refuses with rc=1 and prints a
// useful explanation. Operator's 2026-04-29 directive — we
// can't afford to lose funnel-diagnostic data through the
// pre-1.0 development cycle. Once we ship v1.0.0 the
// preV1Locked() guard returns false and `off` resumes working
// as a normal opt-out (covered by TestTelemetry_OffPostV1).
func TestTelemetry_OffLockedPreV1(t *testing.T) {
	app, _, errBuf := newTelemetryTestApp(t)

	if rc := app.runTelemetry([]string{"off"}); rc != 1 {
		t.Errorf("pre-v1.0 `off` rc=%d, want 1 (locked refusal)", rc)
	}
	if !strings.Contains(errBuf.String(), "opt-out is locked until v1.0.0") {
		t.Errorf("expected lock-explanation on stderr, got: %q", errBuf.String())
	}
	// Config must still report enabled=true because the refusal
	// short-circuited before the persistence step. The default
	// from config.Default() is enabled=true (ADR-030).
	cfg, err := config.LoadOrDefault(app.Path())
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if !cfg.Telemetry.Enabled {
		t.Error("post-refusal: config must still report enabled=true (default-on policy)")
	}
}

// TestTelemetry_NoArgsExit2 confirms `clawtool telemetry` (no verb)
// prints usage and exits 2 — same convention every other multi-verb
// subcommand uses, so operators get a consistent UX.
func TestTelemetry_NoArgsExit2(t *testing.T) {
	app, out, _ := newTelemetryTestApp(t)
	rc := app.runTelemetry(nil)
	if rc != 2 {
		t.Errorf("no-args rc=%d, want 2", rc)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Errorf("no-args should print usage; got %q", out.String())
	}
}

// TestTelemetry_UnknownSubExit2 confirms an unknown verb exits 2
// with a helpful error pointing at the usage block.
func TestTelemetry_UnknownSubExit2(t *testing.T) {
	app, _, errBuf := newTelemetryTestApp(t)
	rc := app.runTelemetry([]string{"banana"})
	if rc != 2 {
		t.Errorf("unknown verb rc=%d, want 2", rc)
	}
	if !strings.Contains(errBuf.String(), "unknown subcommand") {
		t.Errorf("unknown verb should mention 'unknown subcommand'; got %q", errBuf.String())
	}
}

// TestTelemetry_HelpExit0 confirms `--help` / `-h` aliases print
// usage and exit 0 (not 2 — the operator asked for help, that's
// success).
func TestTelemetry_HelpExit0(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		app, out, _ := newTelemetryTestApp(t)
		rc := app.runTelemetry([]string{flag})
		if rc != 0 {
			t.Errorf("%s rc=%d, want 0", flag, rc)
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%s should print usage; got %q", flag, out.String())
		}
	}
}

// TestTelemetry_IdempotentOnOff confirms repeated `on` / `off` calls
// don't error and surface a "no change" message.
func TestTelemetry_IdempotentOnOff(t *testing.T) {
	app, out, _ := newTelemetryTestApp(t)
	if rc := app.runTelemetry([]string{"on"}); rc != 0 {
		t.Fatalf("first on: rc=%d", rc)
	}
	out.Reset()
	if rc := app.runTelemetry([]string{"on"}); rc != 0 {
		t.Fatalf("second on: rc=%d", rc)
	}
	if !strings.Contains(out.String(), "already on") {
		t.Errorf("second `on` should say 'already on'; got %q", out.String())
	}
}
