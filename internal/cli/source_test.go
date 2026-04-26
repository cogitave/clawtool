package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func newSrcApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	sec := filepath.Join(dir, "secrets.toml")
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb, ConfigPath: cfg}
	app.SetSecretsPath(sec)
	return app, out, errb, cfg, sec
}

func TestSourceAdd_KnownGithub(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	rc := app.Run([]string{"source", "add", "github"})
	if rc != 0 {
		t.Fatalf("source add github exit = %d, want 0", rc)
	}
	got := out.String()
	if !strings.Contains(got, "added source \"github\"") {
		t.Errorf("missing added confirmation: %q", got)
	}
	if !strings.Contains(got, "@modelcontextprotocol/server-github") {
		t.Errorf("missing package name in confirmation: %q", got)
	}
	// Auth hint must appear because GITHUB_TOKEN is required.
	if !strings.Contains(got, "credentials needed") {
		t.Errorf("missing 'credentials needed' warning: %q", got)
	}
	if !strings.Contains(got, "GITHUB_TOKEN") {
		t.Errorf("missing GITHUB_TOKEN in warning: %q", got)
	}
	if !strings.Contains(got, "set-secret github GITHUB_TOKEN") {
		t.Errorf("missing actionable set-secret command: %q", got)
	}
}

func TestSourceAdd_UnknownSuggests(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	rc := app.Run([]string{"source", "add", "github-typo"})
	if rc != 1 {
		t.Fatalf("unknown source exit = %d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "did you mean") {
		t.Errorf("expected suggestion, got: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "github") {
		t.Errorf("suggestion should include github: %q", errb.String())
	}
}

func TestSourceAdd_DuplicateRequiresAs(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("first add failed")
	}
	rc := app.Run([]string{"source", "add", "github"})
	if rc != 1 {
		t.Fatalf("second add should fail, got rc=%d", rc)
	}
	if !strings.Contains(errb.String(), "already exists") {
		t.Errorf("expected 'already exists', got: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "--as") {
		t.Errorf("expected --as suggestion, got: %q", errb.String())
	}
}

func TestSourceAdd_AsOverride(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github", "--as", "github-personal"}); rc != 0 {
		t.Fatalf("add --as github-personal failed")
	}
	if !strings.Contains(out.String(), `added source "github-personal"`) {
		t.Errorf("expected confirmation for github-personal, got: %q", out.String())
	}
}

func TestSourceAdd_AsValidatesKebab(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	rc := app.Run([]string{"source", "add", "github", "--as", "Github_Bad"})
	if rc != 2 {
		t.Errorf("invalid instance name exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "kebab-case") {
		t.Errorf("expected kebab-case error, got: %q", errb.String())
	}
}

func TestSourceList_Empty(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "list"}); rc != 0 {
		t.Fatalf("list exit = %d", rc)
	}
	if !strings.Contains(out.String(), "no sources configured") {
		t.Errorf("expected 'no sources configured', got: %q", out.String())
	}
}

func TestSourceList_AuthStatus(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add failed")
	}
	out.Reset()
	if rc := app.Run([]string{"source", "list"}); rc != 0 {
		t.Fatalf("list exit = %d", rc)
	}
	if !strings.Contains(out.String(), "github") {
		t.Errorf("list missing github: %q", out.String())
	}
	if !strings.Contains(out.String(), "missing") {
		t.Errorf("list should report missing auth before set-secret: %q", out.String())
	}

	// Set the secret and re-check.
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN", "--value", "ghp_test"}); rc != 0 {
		t.Fatalf("set-secret exit = %d", rc)
	}
	out.Reset()
	if rc := app.Run([]string{"source", "list"}); rc != 0 {
		t.Fatalf("list2 exit = %d", rc)
	}
	if !strings.Contains(out.String(), "ready") {
		t.Errorf("list should report ready after secret set: %q", out.String())
	}
}

func TestSourceRemove(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add failed")
	}
	out.Reset()
	if rc := app.Run([]string{"source", "remove", "github"}); rc != 0 {
		t.Fatalf("remove exit = %d, stderr=%q", rc, errb.String())
	}
	if !strings.Contains(out.String(), "removed source") {
		t.Errorf("remove confirmation missing: %q", out.String())
	}
	// Removing again should error.
	rc := app.Run([]string{"source", "remove", "github"})
	if rc == 0 {
		t.Errorf("removing absent instance should fail")
	}
}

func TestSourceSetSecret_PersistsAcrossLoad(t *testing.T) {
	app, out, _, _, secPath := newSrcApp(t)
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN", "--value", "ghp_round_trip"}); rc != 0 {
		t.Fatalf("set-secret exit = %d", rc)
	}
	if !strings.Contains(out.String(), "stored secret GITHUB_TOKEN") {
		t.Errorf("missing confirmation: %q", out.String())
	}

	// New App with same paths should read the same secret back.
	app2, out2, _, _, _ := newSrcApp(t)
	app2.ConfigPath = app.ConfigPath
	app2.SetSecretsPath(secPath)
	// We don't have a public 'get-secret' subcommand by design (secrets
	// don't print). Use source check after registering github to confirm
	// the secret resolves.
	if rc := app2.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add on app2 exit %d", rc)
	}
	out2.Reset()
	if rc := app2.Run([]string{"source", "check"}); rc != 0 {
		t.Fatalf("check exit = %d, want 0 (secret should be present)", rc)
	}
	if !strings.Contains(out2.String(), "ready") {
		t.Errorf("check should report ready after persisted secret: %q", out2.String())
	}
}

func TestSourceSetSecret_MissingValueErrors(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	// Empty stdin reader; no --value flag → must error rather than write empty secret.
	app.Stdin = strings.NewReader("")
	rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN"})
	if rc != 2 {
		t.Errorf("empty value exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "empty value") {
		t.Errorf("expected 'empty value' error, got: %q", errb.String())
	}
}

func TestSourceSetSecret_StdinFallback(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	app.Stdin = strings.NewReader("ghp_from_stdin\n")
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN"}); rc != 0 {
		t.Fatalf("set-secret via stdin exit = %d", rc)
	}
	if !strings.Contains(out.String(), "stored secret GITHUB_TOKEN") {
		t.Errorf("missing confirmation: %q", out.String())
	}
}

func TestSourceCheck_AllReady(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	// Add and satisfy a source, then check.
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add failed")
	}
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN", "--value", "x"}); rc != 0 {
		t.Fatalf("set-secret failed")
	}
	out.Reset()
	if rc := app.Run([]string{"source", "check"}); rc != 0 {
		t.Fatalf("check exit = %d, want 0 (all ready)", rc)
	}
	if !strings.Contains(out.String(), "ready") {
		t.Errorf("check missing 'ready': %q", out.String())
	}
}
