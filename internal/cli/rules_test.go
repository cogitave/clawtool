package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTmpRulesFile chdir's into a fresh tmpdir and writes a sample
// .clawtool/rules.toml. It also redirects XDG_CONFIG_HOME so the
// loader's user-global root can't accidentally pick up the
// developer's actual rules file. Returns the rules path.
func withTmpRulesFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	rulesDir := filepath.Join(dir, ".clawtool")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rulesDir, "rules.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	return path
}

const sampleRulesTOML = `
[[rule]]
name        = "gofmt-clean"
description = "Go sources must be gofmt-clean before commit."
when        = "pre_commit"
condition   = 'changed("**/*.go")'
severity    = "warn"
hint        = "Run gofmt -w ."

[[rule]]
name        = "no-coauthor"
description = "Reject AI-attribution trailers in commit messages."
when        = "pre_commit"
condition   = 'commit_message_contains("Co-Authored-By")'
severity    = "block"
`

func newRulesApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	return &App{Stdout: out, Stderr: errb}, out, errb
}

// TestRulesList_HumanOutput confirms `clawtool rules list`
// renders a header line, the source path, and a row per rule
// when a real rules.toml is present.
func TestRulesList_HumanOutput(t *testing.T) {
	withTmpRulesFile(t, sampleRulesTOML)
	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "list"}); rc != 0 {
		t.Fatalf("rules list rc=%d, stderr=%s", rc, out.String())
	}
	body := out.String()
	for _, want := range []string{
		"source:",
		"NAME",
		"WHEN",
		"SEVERITY",
		"gofmt-clean",
		"no-coauthor",
		"pre_commit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestRulesList_JSONOutput emits parseable JSON whose keys are
// snake_case (matches agentListEntry / agents.Status / BuildInfo
// shape) and reports every rule's source path. Drives the wire
// contract for shell pipelines.
func TestRulesList_JSONOutput(t *testing.T) {
	wantSource := withTmpRulesFile(t, sampleRulesTOML)
	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "list", "--json"}); rc != 0 {
		t.Fatalf("rules list --json rc=%d", rc)
	}
	body := out.String()
	for _, lit := range []string{`"name":`, `"when":`, `"severity":`, `"condition":`, `"source":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got []ruleListEntry
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	byName := map[string]ruleListEntry{}
	for _, e := range got {
		byName[e.Name] = e
	}
	gofmt, ok := byName["gofmt-clean"]
	if !ok {
		t.Fatal("gofmt-clean not present in JSON")
	}
	if gofmt.When != "pre_commit" {
		t.Errorf("gofmt-clean when = %q, want pre_commit", gofmt.When)
	}
	if gofmt.Severity != "warn" {
		t.Errorf("gofmt-clean severity = %q, want warn", gofmt.Severity)
	}
	if gofmt.Source != wantSource {
		t.Errorf("source = %q, want %q", gofmt.Source, wantSource)
	}
	if gofmt.Hint != "Run gofmt -w ." {
		t.Errorf("hint = %q, want exact 'Run gofmt -w .'", gofmt.Hint)
	}
	noco, ok := byName["no-coauthor"]
	if !ok {
		t.Fatal("no-coauthor not present in JSON")
	}
	if noco.Severity != "block" {
		t.Errorf("no-coauthor severity = %q, want block", noco.Severity)
	}
	if noco.Hint != "" {
		t.Errorf("no-coauthor hint = %q, want empty (omitempty path)", noco.Hint)
	}
}

// TestRulesList_JSONStableShape confirms the JSON path produces
// an ARRAY (not an object) so `jq '.[]'` consumers stay uniform
// with `agents list --json` and `agents status --json`.
func TestRulesList_JSONStableShape(t *testing.T) {
	withTmpRulesFile(t, sampleRulesTOML)
	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "list", "--json"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '[' {
		t.Errorf("expected array (starts with '['); got: %q", body)
	}
}

// TestRulesList_JSONNoConfig emits an empty array when no
// rules.toml exists — pipelines must see the same shape across
// configured / unconfigured projects.
func TestRulesList_JSONNoConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "list", "--json"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	got := strings.TrimSpace(out.String())
	if got != "[]" {
		t.Errorf("output = %q, want %q (empty JSON array)", got, "[]")
	}
}

// TestRulesList_HumanNoConfig keeps the existing human-friendly
// hint line for the "no rules configured" case — operators
// running the bare command shouldn't suddenly see an empty array.
func TestRulesList_HumanNoConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "list"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(out.String(), "no rules configured") {
		t.Errorf("missing hint line; output: %q", out.String())
	}
}
