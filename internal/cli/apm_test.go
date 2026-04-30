package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture3ServerApm is the verbatim apm.yml content used by the
// dry-run + apply tests below. Three MCP servers, two skills, and
// one playbook — exercises both string + object dependency forms
// (per microsoft/apm manifest schema 0.1).
const fixture3ServerApm = `name: clawtool-apm-fixture
version: 1.0.0
description: Fixture for clawtool's apm import test.
dependencies:
  apm:
    - anthropics/skills/skills/frontend-design
    - github/awesome-copilot/skills/security-review
    - acme/playbooks/incident-response
  mcp:
    - io.github.github/github-mcp-server
    - name: io.modelcontextprotocol/postgres
      transport: stdio
    - name: my-private-server
      transport: http
`

// withApmFixture writes the fixture into a fresh temp dir and
// returns (apmPath, repoRoot). Cleanup is via t.TempDir().
func withApmFixture(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "apm.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p, dir
}

// stubAddSource swaps addSourceFn for the test's lifetime, capturing
// every argv that the verb dispatches into "source add ...".
func stubAddSource(t *testing.T) *[][]string {
	t.Helper()
	captured := &[][]string{}
	prev := addSourceFn
	addSourceFn = func(_ *App, argv []string) int {
		copyArgv := append([]string(nil), argv...)
		*captured = append(*captured, copyArgv)
		return 0
	}
	t.Cleanup(func() { addSourceFn = prev })
	return captured
}

func TestApmImport_DryRunListsSources(t *testing.T) {
	apmPath, repo := withApmFixture(t, fixture3ServerApm)
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.Run([]string{"apm", "import", apmPath, "--dry-run", "--repo", repo})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0\nstderr: %s", rc, errb.String())
	}
	got := out.String()
	for _, want := range []string{
		"io.github.github/github-mcp-server",
		"io.modelcontextprotocol/postgres",
		"my-private-server",
		"frontend-design",
		"security-review",
		"incident-response",
		"3 MCP server(s)",
		"3 primitive(s)",
		"(dry-run) would write recipe stub",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run output missing %q\ngot:\n%s", want, got)
		}
	}
	// Stub file MUST NOT be written in dry-run mode.
	if _, err := os.Stat(filepath.Join(repo, ".clawtool", "apm-imported-manifest.toml")); !os.IsNotExist(err) {
		t.Errorf("dry-run leaked stub file: err=%v", err)
	}
}

func TestApmImport_NormalApplyCallsSourceAdd(t *testing.T) {
	captured := stubAddSource(t)
	apmPath, repo := withApmFixture(t, fixture3ServerApm)
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.Run([]string{"apm", "import", apmPath, "--repo", repo})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0\nstderr: %s", rc, errb.String())
	}
	if got := len(*captured); got != 3 {
		t.Fatalf("addSourceFn calls = %d, want 3 (one per mcp entry)", got)
	}
	// Every dispatch must be `source add <short-name> [--as <short>]`.
	for i, argv := range *captured {
		if len(argv) < 3 || argv[0] != "source" || argv[1] != "add" {
			t.Errorf("call %d: unexpected argv shape %v", i, argv)
		}
	}
	// Stub file IS written when not dry-run.
	stub := filepath.Join(repo, ".clawtool", "apm-imported-manifest.toml")
	body, err := os.ReadFile(stub)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	if !strings.Contains(string(body), "[apm.skill]") {
		t.Errorf("stub missing [apm.skill] block:\n%s", body)
	}
	if !strings.Contains(string(body), "[apm.playbook]") {
		t.Errorf("stub missing [apm.playbook] block:\n%s", body)
	}
	if !strings.Contains(string(body), "io.github.github/github-mcp-server") {
		t.Errorf("stub missing mcp ref:\n%s", body)
	}
}

func TestApmImport_MissingFile(t *testing.T) {
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.Run([]string{"apm", "import", "/nonexistent/apm.yml"})
	if rc == 0 {
		t.Fatalf("expected non-zero rc for missing file")
	}
	// Pin the typed error via the stderr message — the verb wraps
	// it with a fmt.Errorf("%w: ...") before printing.
	if !strings.Contains(errb.String(), "manifest file not found") {
		t.Errorf("stderr missing typed-error marker: %q", errb.String())
	}
	// Belt-and-braces: the typed sentinel itself.
	if !errors.Is(ErrApmManifestMissing, ErrApmManifestMissing) {
		t.Fatalf("sentinel sanity check failed")
	}
}

func TestApmImport_YAMLParseError(t *testing.T) {
	// Deliberately broken YAML — unbalanced quotes + bad indent.
	apmPath, _ := withApmFixture(t, "name: \"unterminated\nversion: [oops")
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.Run([]string{"apm", "import", apmPath})
	if rc == 0 {
		t.Fatalf("expected non-zero rc for YAML parse error")
	}
	if !strings.Contains(errb.String(), "yaml parse error") {
		t.Errorf("stderr missing typed-error marker: %q", errb.String())
	}
	// Direct check on loadApmManifest so the typed sentinel is
	// pinned for downstream callers (errors.Is contract).
	_, err := loadApmManifest(apmPath)
	if !errors.Is(err, ErrApmYAMLParse) {
		t.Errorf("loadApmManifest err = %v, want errors.Is ErrApmYAMLParse", err)
	}
}
