package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// setupPortalConfigForRemove writes a minimal config.toml under
// the per-test XDG_CONFIG_HOME with one portal entry. Returns the
// config path so tests can assert it was/wasn't touched.
func setupPortalConfigForRemove(t *testing.T, name string) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfgPath := filepath.Join(tmp, "clawtool", "config.toml")

	cfg := config.Default()
	cfg.Portals = map[string]config.PortalConfig{
		name: {
			BaseURL:      "https://example.com",
			SecretsScope: "portal." + name,
			Selectors: config.PortalSelectors{
				Input:    "#input",
				Submit:   "#submit",
				Response: ".reply",
			},
			ResponseDonePredicate: config.PortalPredicate{
				Type:  "selector_visible",
				Value: ".done",
			},
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return cfgPath
}

// TestPortalRemove_DryRunPreservesConfig confirms `--dry-run`
// prints the preview banner without mutating config.toml.
// Sister of `source remove --dry-run` (b364ec6) and `source
// rename --dry-run` (ca918b8).
func TestPortalRemove_DryRunPreservesConfig(t *testing.T) {
	cfgPath := setupPortalConfigForRemove(t, "deepseek")

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config before: %v", err)
	}

	if rc := app.Run([]string{"portal", "remove", "deepseek", "--dry-run"}); rc != 0 {
		t.Fatalf("dry-run rc=%d, stderr=%q", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{"(dry-run)", "would remove portal", "deepseek", "would be left in place"} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing %q\n--- output ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "✓ portal") {
		t.Errorf("dry-run leaked success verb: %q", body)
	}

	// Critical: config bytes must be byte-identical after dry-run.
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("config.toml mutated after dry-run\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestPortalRemove_DryRunUnknownPortal keeps the not-found error
// path intact when --dry-run is in play. Operators catch typos
// at preview time.
func TestPortalRemove_DryRunUnknownPortal(t *testing.T) {
	setupPortalConfigForRemove(t, "deepseek")

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.Run([]string{"portal", "remove", "ghost", "--dry-run"})
	if rc != 1 {
		t.Errorf("dry-run on absent portal rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "portal \"ghost\" not found") {
		t.Errorf("expected not-found error, got: %q", errb.String())
	}
	if strings.Contains(out.String(), "(dry-run)") {
		t.Errorf("dry-run banner leaked despite not-found error: %q", out.String())
	}
}

// TestPortalRemove_RealStillWorks confirms the existing
// real-write path is unaffected by the dry-run plumbing — same
// signature change ripples through both branches.
func TestPortalRemove_RealStillWorks(t *testing.T) {
	cfgPath := setupPortalConfigForRemove(t, "deepseek")

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "remove", "deepseek"}); rc != 0 {
		t.Fatalf("real remove rc=%d, stderr=%q", rc, errb.String())
	}
	if !strings.Contains(out.String(), "✓ portal deepseek removed") {
		t.Errorf("missing success banner: %q", out.String())
	}

	// And the portal block must actually be gone now.
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read after real-remove: %v", err)
	}
	if strings.Contains(string(body), "[portals.deepseek]") {
		t.Errorf("portals block survived real-remove:\n%s", body)
	}
}
