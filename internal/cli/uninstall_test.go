package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uninstallTestApp wraps App with concrete bytes.Buffer outputs so
// the tests can assert on captured stdout.
type uninstallTestApp struct {
	*App
	out *bytes.Buffer
	err *bytes.Buffer
}

func newTestApp() *uninstallTestApp {
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	return &uninstallTestApp{
		App: &App{Stdout: out, Stderr: errb},
		out: out,
		err: errb,
	}
}

func (u *uninstallTestApp) stdoutString() string { return u.out.String() }

func setupFakeClawtoolHome(t *testing.T) (cfgDir, cacheDir, dataDir, binDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("CLAWTOOL_INSTALL_DIR", filepath.Join(root, "bin"))

	cfgDir = filepath.Join(root, "cfg", "clawtool")
	cacheDir = filepath.Join(root, "cache", "clawtool")
	dataDir = filepath.Join(root, "data", "clawtool")
	binDir = filepath.Join(root, "bin")

	for _, dir := range []string{cfgDir, cacheDir, dataDir, binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Drop a few representative files clawtool would have written.
	must := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(cfgDir, "config.toml"), "[profile]\nactive = \"default\"\n")
	must(filepath.Join(cfgDir, "secrets.toml"), "[scopes.github]\nGH_TOKEN=\"x\"\n")
	must(filepath.Join(cfgDir, "active_agent"), "claude\n")
	must(filepath.Join(cfgDir, "active_portal"), "my-deepseek\n")
	must(filepath.Join(cfgDir, "listener-token"), "deadbeef\n")
	must(filepath.Join(cfgDir, "identity.ed25519"), "private=...\n")
	must(filepath.Join(cacheDir, "biam.db"), "")
	must(filepath.Join(dataDir, "telemetry-id"), "uuid\n")
	must(filepath.Join(binDir, "clawtool"), "binary\n")
	return
}

func TestUninstall_DryRun_RemovesNothing(t *testing.T) {
	cfgDir, cacheDir, dataDir, _ := setupFakeClawtoolHome(t)

	app := newTestApp()
	if err := app.Uninstall(uninstallArgs{dryRun: true, yes: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(cfgDir, "config.toml"),
		filepath.Join(cfgDir, "secrets.toml"),
		filepath.Join(cacheDir, "biam.db"),
		filepath.Join(dataDir, "telemetry-id"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("dry-run should have left %s in place: %v", want, err)
		}
	}
	out := app.stdoutString()
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("dry-run output should announce itself: %q", out)
	}
}

func TestUninstall_FullSweep(t *testing.T) {
	cfgDir, cacheDir, dataDir, binDir := setupFakeClawtoolHome(t)

	app := newTestApp()
	if err := app.Uninstall(uninstallArgs{yes: true}); err != nil {
		t.Fatal(err)
	}
	// config + cache + data should be gone.
	for _, gone := range []string{cfgDir, cacheDir, dataDir} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("expected %s to be removed", gone)
		}
	}
	// Binary should NOT have been touched (no --purge-binary).
	if _, err := os.Stat(filepath.Join(binDir, "clawtool")); err != nil {
		t.Errorf("binary should survive without --purge-binary: %v", err)
	}
}

func TestUninstall_PurgeBinary(t *testing.T) {
	_, _, _, binDir := setupFakeClawtoolHome(t)

	app := newTestApp()
	if err := app.Uninstall(uninstallArgs{yes: true, purgeBinary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "clawtool")); err == nil {
		t.Error("expected binary to be removed with --purge-binary")
	}
}

func TestUninstall_KeepConfig_RemovesOnlyEphemera(t *testing.T) {
	cfgDir, cacheDir, dataDir, _ := setupFakeClawtoolHome(t)

	app := newTestApp()
	if err := app.Uninstall(uninstallArgs{yes: true, keepConfig: true}); err != nil {
		t.Fatal(err)
	}
	// config.toml + secrets.toml + identity stay.
	for _, keep := range []string{
		filepath.Join(cfgDir, "config.toml"),
		filepath.Join(cfgDir, "secrets.toml"),
		filepath.Join(cfgDir, "identity.ed25519"),
	} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("--keep-config should preserve %s: %v", keep, err)
		}
	}
	// Sticky pointers + listener token go.
	for _, gone := range []string{
		filepath.Join(cfgDir, "active_agent"),
		filepath.Join(cfgDir, "active_portal"),
		filepath.Join(cfgDir, "listener-token"),
	} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("--keep-config should still drop sticky pointer %s", gone)
		}
	}
	// Cache + data still go regardless of --keep-config.
	if _, err := os.Stat(cacheDir); err == nil {
		t.Error("cache dir should be removed even with --keep-config")
	}
	if _, err := os.Stat(dataDir); err == nil {
		t.Error("data dir should be removed even with --keep-config")
	}
}

func TestUninstall_NothingToDo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("CLAWTOOL_INSTALL_DIR", filepath.Join(root, "bin"))

	app := newTestApp()
	if err := app.Uninstall(uninstallArgs{yes: true}); err != nil {
		t.Fatal(err)
	}
	out := app.stdoutString()
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("expected 'nothing to remove' message, got: %q", out)
	}
}

func TestParseUninstallArgs(t *testing.T) {
	got, err := parseUninstallArgs([]string{"--yes", "--dry-run", "--purge-binary", "--keep-config"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.yes || !got.dryRun || !got.purgeBinary || !got.keepConfig {
		t.Errorf("flags wrong: %+v", got)
	}
	if _, err := parseUninstallArgs([]string{"--bogus"}); err == nil {
		t.Error("expected error for unknown flag")
	}
}
