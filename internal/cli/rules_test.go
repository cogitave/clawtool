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

// TestRulesShow_JSONOutput renders a single rule object (not an
// array — show is a single-result query). Asserts every relevant
// field comes through with snake_case keys + the source path is
// surfaced so a script can correlate.
func TestRulesShow_JSONOutput(t *testing.T) {
	wantSource := withTmpRulesFile(t, sampleRulesTOML)
	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "show", "gofmt-clean", "--json"}); rc != 0 {
		t.Fatalf("rules show --json rc=%d, stderr=%s", rc, out.String())
	}
	body := out.String()
	for _, lit := range []string{`"name":`, `"when":`, `"severity":`, `"condition":`, `"source":`, `"hint":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got ruleListEntry
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if got.Name != "gofmt-clean" {
		t.Errorf("Name = %q, want gofmt-clean", got.Name)
	}
	if got.When != "pre_commit" {
		t.Errorf("When = %q, want pre_commit", got.When)
	}
	if got.Severity != "warn" {
		t.Errorf("Severity = %q, want warn", got.Severity)
	}
	if got.Hint != "Run gofmt -w ." {
		t.Errorf("Hint = %q, want exact 'Run gofmt -w .'", got.Hint)
	}
	if got.Source != wantSource {
		t.Errorf("Source = %q, want %q", got.Source, wantSource)
	}
}

// TestRulesShow_JSONStableShape confirms the JSON path produces
// an object (single result), NOT an array — show is a singular
// query, list is the array form. Pipelines that pipe show output
// into `jq '.name'` rely on object shape.
func TestRulesShow_JSONStableShape(t *testing.T) {
	withTmpRulesFile(t, sampleRulesTOML)
	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "show", "gofmt-clean", "--json"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Errorf("expected object (starts with '{'); got: %q", body)
	}
}

// TestRulesShow_JSONNotFound emits a structured error object on
// stdout so a JSON-driven pipeline can detect "rule not found"
// without inspecting stderr. Exit code stays 1 to match the
// human path.
func TestRulesShow_JSONNotFound(t *testing.T) {
	withTmpRulesFile(t, sampleRulesTOML)
	app, out, errb := newRulesApp(t)
	rc := app.Run([]string{"rules", "show", "no-such-rule", "--json"})
	if rc != 1 {
		t.Errorf("rc=%d, want 1", rc)
	}
	body := strings.TrimSpace(out.String())
	if !strings.HasPrefix(body, "{") {
		t.Errorf("expected JSON object on stdout; got %q", body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Errorf("expected 'error' field in JSON; got %q", body)
	}
	if !strings.Contains(body, "no-such-rule") {
		t.Errorf("expected the missing rule name in error; got %q", body)
	}
	// Human stderr should be silent on the JSON path so scripts
	// aren't fed a duplicate human banner.
	if errb.String() != "" {
		t.Errorf("stderr should be empty on --json path; got %q", errb.String())
	}
}

// TestRulesShow_HumanOutput preserves the existing key:value
// block (no --json flag) so the unscripted operator workflow
// keeps working.
func TestRulesShow_HumanOutput(t *testing.T) {
	withTmpRulesFile(t, sampleRulesTOML)
	app, out, _ := newRulesApp(t)
	if rc := app.Run([]string{"rules", "show", "gofmt-clean"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	body := out.String()
	for _, want := range []string{
		"name:        gofmt-clean",
		"when:        pre_commit",
		"severity:    warn",
		"hint:        Run gofmt -w .",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestRulesNew_DryRunDoesNotWrite confirms `--dry-run` prints
// the would-be-added rule without persisting to rules.toml.
// Operators preview a complex condition; CI gates can validate
// without mutating the project's rules file.
func TestRulesNew_DryRunDoesNotWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	t.Chdir(dir)

	app, out, _ := newRulesApp(t)
	rc := app.Run([]string{
		"rules", "new", "preview-rule",
		"--when", "pre_commit",
		"--condition", `changed("**/*.go")`,
		"--severity", "warn",
		"--description", "preview only",
		"--hint", "won't be written",
		"--dry-run",
	})
	if rc != 0 {
		t.Fatalf("dry-run rc=%d, stdout=%s", rc, out.String())
	}
	body := out.String()
	for _, want := range []string{
		"(dry-run)",
		"preview-rule",
		"pre_commit",
		"warn",
		`changed("**/*.go")`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, body)
		}
	}
	// rules.toml must NOT have been created.
	rulesFile := filepath.Join(dir, ".clawtool", "rules.toml")
	if _, err := os.Stat(rulesFile); err == nil {
		t.Errorf("rules.toml should not exist after dry-run; got file at %s", rulesFile)
	}
}

// TestRulesNew_DryRunValidatesCondition rejects a malformed
// condition before claiming success — the whole point of
// dry-run is catching syntax errors without committing them.
func TestRulesNew_DryRunValidatesCondition(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	app, _, errb := newRulesApp(t)
	rc := app.Run([]string{
		"rules", "new", "bad-rule",
		"--when", "pre_commit",
		"--condition", "this is not a valid expression",
		"--dry-run",
	})
	if rc != 1 {
		t.Errorf("rc=%d, want 1 (validation should fail)", rc)
	}
	if !strings.Contains(errb.String(), "condition") {
		t.Errorf("expected condition-error mention in stderr; got %q", errb.String())
	}
}

// TestRulesNew_DryRunDetectsDuplicate flags an attempt to add a
// rule with the same Name as an existing one — same check
// AppendRule runs, surfaced before any write so CI can fail
// loud.
func TestRulesNew_DryRunDetectsDuplicate(t *testing.T) {
	withTmpRulesFile(t, sampleRulesTOML)

	app, _, errb := newRulesApp(t)
	rc := app.Run([]string{
		"rules", "new", "gofmt-clean", // exists in sampleRulesTOML
		"--when", "pre_commit",
		"--condition", `changed("**/*.go")`,
		"--dry-run",
	})
	if rc != 1 {
		t.Errorf("rc=%d, want 1 on duplicate", rc)
	}
	if !strings.Contains(errb.String(), "already exists") {
		t.Errorf("expected duplicate error in stderr; got %q", errb.String())
	}
}

// TestRulesShow_JSONNoConfig emits an error object (not an
// empty result) when no rules.toml exists — the script-side
// failure mode mirrors `not found`, so pipelines can branch on
// the structured error field.
func TestRulesShow_JSONNoConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	app, out, _ := newRulesApp(t)
	rc := app.Run([]string{"rules", "show", "anything", "--json"})
	if rc != 1 {
		t.Errorf("rc=%d, want 1", rc)
	}
	body := strings.TrimSpace(out.String())
	if !strings.Contains(body, `"error"`) {
		t.Errorf("expected 'error' field on stdout; got %q", body)
	}
	if !strings.Contains(body, "no rules configured") {
		t.Errorf("expected 'no rules configured' message in error; got %q", body)
	}
}
