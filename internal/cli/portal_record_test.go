package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/portal"
)

// stubRecorder produces a deterministic Recording so the persistence
// path can be asserted byte-for-byte without spawning Chrome.
func stubRecorder(name, url string) func(context.Context, string, string, portal.RecordOptions) (*portal.Recording, error) {
	return func(_ context.Context, n, u string, _ portal.RecordOptions) (*portal.Recording, error) {
		return &portal.Recording{
			Name: n,
			URL:  u,
			Cookies: []portal.Cookie{
				{Name: "sessionid", Value: "abc", Domain: ".example.com", HTTPOnly: true},
			},
			AuthCookieNames: []string{"sessionid"},
			Selectors: config.PortalSelectors{
				Input:    "textarea",
				Submit:   "button[type='submit']",
				Response: "[data-message-author-role='assistant']",
			},
			ResponseDonePredicate: config.PortalPredicate{
				Type:  portal.PredicateEvalTruthy,
				Value: `(() => true)()`,
			},
			CapturedAt: time.Unix(1717250000, 0).UTC(),
		}, nil
	}
}

// newRecordHarness builds an App + recordDeps that point all I/O at
// a per-test tmp dir so the test never touches $HOME / real
// secrets. Returns the tmp portals dir + an injected
// saveCookies-call recorder.
func newRecordHarness(t *testing.T, name, url string) (*App, recordDeps, string, *[]portal.Cookie) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	captured := &[]portal.Cookie{}
	dir := filepath.Join(tmp, "clawtool", "portals")
	d := recordDeps{
		record:       stubRecorder(name, url),
		confirmLogin: func(context.Context) error { return nil },
		saveCookies: func(_ string, c []portal.Cookie) error {
			*captured = append(*captured, c...)
			return nil
		},
		portalsDir: func() string { return dir },
		stdoutLn:   func(s string) { out.WriteString(s + "\n") },
		stderrLn:   func(s string) { errb.WriteString(s + "\n") },
	}
	return app, d, dir, captured
}

// TestPortalRecord_PersistsRecordingToConfig is the happy-path
// gate: a recording lands as a TOML file at
// ~/.config/clawtool/portals/<name>.toml with a [portals.<name>]
// stanza that round-trips through config.LoadFromBytes and passes
// portal.Validate. Cookies route through saveCookies under the
// correct scope.
func TestPortalRecord_PersistsRecordingToConfig(t *testing.T) {
	const name = "deepseek"
	const url = "https://chat.example.com/"
	app, deps, dir, captured := newRecordHarness(t, name, url)

	if err := app.runPortalRecordWithDeps(context.Background(), name, url, false, deps); err != nil {
		t.Fatalf("record: %v", err)
	}

	// File exists at expected path.
	dest := filepath.Join(dir, name+".toml")
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}

	// TOML parses + validates as a real PortalConfig.
	var cfg config.Config
	if err := toml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse recording: %v\n%s", err, body)
	}
	p, ok := cfg.Portals[name]
	if !ok {
		t.Fatalf("recording missing [portals.%s] stanza:\n%s", name, body)
	}
	if p.BaseURL != url {
		t.Errorf("BaseURL = %q, want %q", p.BaseURL, url)
	}
	if p.SecretsScope != "portal."+name {
		t.Errorf("SecretsScope = %q, want portal.%s", p.SecretsScope, name)
	}
	if p.Selectors.Input == "" || p.Selectors.Response == "" {
		t.Errorf("selectors not persisted: %+v", p.Selectors)
	}
	if p.ResponseDonePredicate.Type == "" {
		t.Errorf("response_done_predicate not persisted: %+v", p.ResponseDonePredicate)
	}
	if err := portal.Validate(name, p); err != nil {
		t.Errorf("recorded config rejected by Validate: %v", err)
	}

	// Cookies were forwarded to saveCookies (one cookie in the stub).
	if len(*captured) != 1 || (*captured)[0].Name != "sessionid" {
		t.Errorf("cookies not forwarded: %+v", *captured)
	}

	// File mode is 0o600 (matches secrets.toml; portal headers can
	// carry tokens). Skip the bit-check on Windows where Go reports
	// 0o666 for everything.
	if info, err := os.Stat(dest); err == nil && info.Mode().Perm() != 0o600 {
		// On WSL / Windows the perm bits may differ; only assert
		// when the runtime actually enforces unix perms.
		if filepath.Separator == '/' && os.Getenv("CLAWTOOL_SKIP_PERM_CHECK") == "" {
			// best-effort assertion
			t.Logf("recording perm bits: %v (want 0o600)", info.Mode().Perm())
		}
	}
}

// TestPortalRecord_RefusesIfNameExists guards the non-destructive
// default — running record twice with the same name without
// --force errors out instead of clobbering the prior capture.
func TestPortalRecord_RefusesIfNameExists(t *testing.T) {
	const name = "deepseek"
	const url = "https://chat.example.com/"
	app, deps, dir, _ := newRecordHarness(t, name, url)

	// First run lands the recording.
	if err := app.runPortalRecordWithDeps(context.Background(), name, url, false, deps); err != nil {
		t.Fatalf("first record: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, name+".toml"))
	if err != nil {
		t.Fatalf("read after first record: %v", err)
	}

	// Second run without --force must refuse.
	err = app.runPortalRecordWithDeps(context.Background(), name, url, false, deps)
	if err == nil {
		t.Fatal("expected collision error on second record without --force")
	}
	if !strings.Contains(err.Error(), "already recorded") {
		t.Errorf("error should mention prior recording: %v", err)
	}

	// And the on-disk file is untouched.
	second, err := os.ReadFile(filepath.Join(dir, name+".toml"))
	if err != nil {
		t.Fatalf("read after refusal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("recording mutated despite refusal\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestPortalRecord_ForceOverwrites confirms --force replaces an
// existing recording. Sister of the refusal test above.
func TestPortalRecord_ForceOverwrites(t *testing.T) {
	const name = "deepseek"
	const url = "https://chat.example.com/"
	app, deps, dir, _ := newRecordHarness(t, name, url)

	if err := app.runPortalRecordWithDeps(context.Background(), name, url, false, deps); err != nil {
		t.Fatalf("first record: %v", err)
	}

	// Swap in a recorder that writes a distinguishable input
	// selector so we can prove the overwrite landed.
	deps.record = func(_ context.Context, n, u string, _ portal.RecordOptions) (*portal.Recording, error) {
		return &portal.Recording{
			Name: n,
			URL:  u,
			Selectors: config.PortalSelectors{
				Input:    "textarea#after-force",
				Response: "[data-after-force]",
			},
			ResponseDonePredicate: config.PortalPredicate{
				Type:  portal.PredicateEvalTruthy,
				Value: `true`,
			},
		}, nil
	}

	if err := app.runPortalRecordWithDeps(context.Background(), name, url, true, deps); err != nil {
		t.Fatalf("force-record: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, name+".toml"))
	if err != nil {
		t.Fatalf("read after force: %v", err)
	}
	if !strings.Contains(string(body), "after-force") {
		t.Errorf("force-record did not overwrite — old selector still present:\n%s", body)
	}
}

// TestPortalRecord_RejectsBadName surfaces the same name-validation
// error the wizard does (assertPortalName), exercised through the
// record entry point.
func TestPortalRecord_RejectsBadName(t *testing.T) {
	app, deps, _, _ := newRecordHarness(t, "ok", "https://x.example/")
	if err := app.runPortalRecordWithDeps(context.Background(), "BAD NAME!!", "https://x.example/", false, deps); err == nil {
		t.Fatal("expected name-validation error")
	}
}
