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

// TestTelemetry_OnAndOffRoundTrip writes the flag through the CLI
// path and reads it back through config.LoadOrDefault — confirms
// the verb's persistence side-effect actually lands.
func TestTelemetry_OnAndOffRoundTrip(t *testing.T) {
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

	if rc := app.runTelemetry([]string{"off"}); rc != 0 {
		t.Fatalf("`off` rc=%d", rc)
	}
	cfg, err = config.LoadOrDefault(app.Path())
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if cfg.Telemetry.Enabled {
		t.Error("after `telemetry off`, config Telemetry.Enabled must be false")
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
