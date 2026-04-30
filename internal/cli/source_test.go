package cli

import (
	"bytes"
	"encoding/json"
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

// TestSourceList_EmptyJSON pins the structured empty-state
// contract: `--format json` on a fresh config emits the empty
// array `[]\n` so a pipeline like `clawtool source list
// --format json | jq '. | length'` returns 0 instead of choking
// on the human banner.
func TestSourceList_EmptyJSON(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "list", "--format", "json"}); rc != 0 {
		t.Fatalf("list --format json exit = %d", rc)
	}
	body := strings.TrimSpace(out.String())
	if body != "[]" {
		t.Errorf("expected '[]' on empty-state JSON; got %q", body)
	}
	var arr []map[string]string
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array; got %d entries", len(arr))
	}
}

// TestSourceList_EmptyTSV exercises the TSV path's empty-state.
// A header-only line means `awk 'NR>1{...}'` consumers stop
// cleanly without seeing the human banner mid-pipe.
func TestSourceList_EmptyTSV(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "list", "--format", "tsv"}); rc != 0 {
		t.Fatalf("list --format tsv exit = %d", rc)
	}
	body := strings.TrimRight(out.String(), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 header line on empty-state TSV; got %d: %q", len(lines), body)
	}
	cells := strings.Split(lines[0], "\t")
	if len(cells) != 3 || cells[0] != "INSTANCE" || cells[1] != "AUTH" || cells[2] != "PACKAGE" {
		t.Errorf("expected INSTANCE\\tAUTH\\tPACKAGE header; got %q", lines[0])
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

func TestSourceRename_HappyPath(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add failed: %s", errb.String())
	}
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"source", "rename", "github", "github-personal"}); rc != 0 {
		t.Fatalf("rename exit = %d, stderr=%q", rc, errb.String())
	}
	if !strings.Contains(out.String(), `renamed source "github" → "github-personal"`) {
		t.Errorf("missing rename confirmation: %q", out.String())
	}
	// Listing should show the new name and not the old.
	out.Reset()
	if rc := app.Run([]string{"source", "list"}); rc != 0 {
		t.Fatalf("list exit = %d", rc)
	}
	got := out.String()
	if !strings.Contains(got, "github-personal") {
		t.Errorf("list missing new name: %q", got)
	}
	if strings.Contains(got, "\ngithub ") || strings.Contains(got, "\ngithub\n") {
		t.Errorf("list should not show old name: %q", got)
	}
}

func TestSourceRename_MissingSourceErrors(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	rc := app.Run([]string{"source", "rename", "ghost", "ghost-renamed"})
	if rc != 1 {
		t.Errorf("rename of absent instance exit = %d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "no instance \"ghost\"") {
		t.Errorf("expected 'no instance' error, got: %q", errb.String())
	}
}

func TestSourceRename_CollisionErrors(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatal("add github failed")
	}
	if rc := app.Run([]string{"source", "add", "github", "--as", "github-work"}); rc != 0 {
		t.Fatal("add github-work failed")
	}
	rc := app.Run([]string{"source", "rename", "github", "github-work"})
	if rc != 1 {
		t.Errorf("collision rename exit = %d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %q", errb.String())
	}
}

func TestSourceRename_InvalidKebabRejected(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatal("add failed")
	}
	rc := app.Run([]string{"source", "rename", "github", "Github_Bad"})
	if rc != 2 {
		t.Errorf("invalid kebab exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "kebab-case") {
		t.Errorf("expected kebab-case error, got: %q", errb.String())
	}
}

func TestSourceRename_SameNameRejected(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatal("add failed")
	}
	rc := app.Run([]string{"source", "rename", "github", "github"})
	if rc != 2 {
		t.Errorf("same-name rename exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "same") {
		t.Errorf("expected 'same' error, got: %q", errb.String())
	}
}

func TestSourceRename_MigratesSecrets(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatal("add failed")
	}
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN", "--value", "ghp_secret"}); rc != 0 {
		t.Fatal("set-secret failed")
	}
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"source", "rename", "github", "github-personal"}); rc != 0 {
		t.Fatalf("rename exit = %d, stderr=%q", rc, errb.String())
	}
	if !strings.Contains(out.String(), "secrets scope migrated") {
		t.Errorf("expected 'secrets scope migrated' line, got: %q", out.String())
	}
	// Auth check: github-personal should report ready (because the token
	// followed the rename); the 'check' command refuses if any required
	// env is missing.
	out.Reset()
	if rc := app.Run([]string{"source", "check"}); rc != 0 {
		t.Fatalf("check after rename exit = %d, want 0; secrets did not migrate. stderr=%q", rc, errb.String())
	}
	if !strings.Contains(out.String(), "github-personal") {
		t.Errorf("check should mention new name: %q", out.String())
	}
	if !strings.Contains(out.String(), "ready") {
		t.Errorf("check should report ready: %q", out.String())
	}
}

func TestSourceRename_AliasMv(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatal("add failed")
	}
	out.Reset()
	if rc := app.Run([]string{"source", "mv", "github", "github-renamed"}); rc != 0 {
		t.Fatalf("mv alias exit = %d, stderr=%q", rc, errb.String())
	}
	if !strings.Contains(out.String(), "renamed source") {
		t.Errorf("mv alias should produce same confirmation: %q", out.String())
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

// TestSourceCheck_JSONReady emits structured `[{name, ready}]`
// when every required env var resolves. Pipelines that gate on
// `.[].ready == true` no longer need to grep the human table.
func TestSourceCheck_JSONReady(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add failed")
	}
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN", "--value", "x"}); rc != 0 {
		t.Fatalf("set-secret failed")
	}
	out.Reset()
	if rc := app.Run([]string{"source", "check", "--json"}); rc != 0 {
		t.Fatalf("check --json exit = %d, want 0", rc)
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '[' {
		t.Fatalf("expected JSON array; got: %q", body)
	}
	for _, lit := range []string{`"name":`, `"ready":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got []sourceCheckEntry
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if len(got) != 1 || got[0].Name != "github" || !got[0].Ready {
		t.Errorf("entries = %+v, want one ready=true github entry", got)
	}
	if len(got[0].Missing) != 0 {
		t.Errorf("Missing should be empty when ready; got %v", got[0].Missing)
	}
}

// TestSourceCheck_JSONMissing emits `ready=false` + the
// `missing` env-var list when credentials aren't configured.
// Exit 1 propagates so `set -e` scripts also catch the failure.
func TestSourceCheck_JSONMissing(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add failed")
	}
	out.Reset()
	rc := app.Run([]string{"source", "check", "--json"})
	if rc != 1 {
		t.Errorf("rc = %d, want 1 when credentials missing", rc)
	}
	var got []sourceCheckEntry
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, out.String())
	}
	if len(got) != 1 || got[0].Name != "github" || got[0].Ready {
		t.Fatalf("entries = %+v, want one ready=false github entry", got)
	}
	if len(got[0].Missing) == 0 || got[0].Missing[0] != "GITHUB_TOKEN" {
		t.Errorf("Missing = %v, want ['GITHUB_TOKEN']", got[0].Missing)
	}
}

// TestSourceCheck_SingleNameFilter limits the report to one
// instance — useful for installer scripts that want to probe a
// specific source without spilling other instances' state.
func TestSourceCheck_SingleNameFilter(t *testing.T) {
	app, out, _, _, _ := newSrcApp(t)
	// Add two sources.
	if rc := app.Run([]string{"source", "add", "github"}); rc != 0 {
		t.Fatalf("add github failed")
	}
	if rc := app.Run([]string{"source", "set-secret", "github", "GITHUB_TOKEN", "--value", "x"}); rc != 0 {
		t.Fatalf("set-secret github failed")
	}
	out.Reset()

	if rc := app.Run([]string{"source", "check", "github", "--json"}); rc != 0 {
		t.Fatalf("check github --json rc = %d", rc)
	}
	var got []sourceCheckEntry
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("filtered output should have exactly 1 entry; got %d: %+v", len(got), got)
	}
}

// TestSourceCheck_UnknownInstance fails cleanly with exit 1 +
// stderr message when the operator names a non-existent source.
// JSON path emits an `error` object on stdout so pipelines can
// branch on it.
func TestSourceCheck_UnknownInstance(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	rc := app.Run([]string{"source", "check", "no-such-source"})
	if rc != 1 {
		t.Errorf("rc = %d, want 1 on unknown instance", rc)
	}
	if !strings.Contains(errb.String(), "not configured") {
		t.Errorf("expected 'not configured' in stderr; got %q", errb.String())
	}

	// JSON path: error object on stdout.
	out.Reset()
	errb.Reset()
	rc = app.Run([]string{"source", "check", "no-such-source", "--json"})
	if rc != 1 {
		t.Errorf("rc = %d, want 1 on unknown instance", rc)
	}
	body := strings.TrimSpace(out.String())
	if !strings.HasPrefix(body, "{") || !strings.Contains(body, `"error"`) {
		t.Errorf("expected error object on stdout; got %q", body)
	}
}
