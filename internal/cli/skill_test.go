package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeClaudeHomeForCLI redirects skill-install paths to a
// tempdir so `clawtool skill new` doesn't pollute ~/.claude.
func withFakeClaudeHomeForCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_HOME", dir)
	return dir
}

func TestSkillNew_ScaffoldsAgentSkillsLayout(t *testing.T) {
	dir := withFakeClaudeHomeForCLI(t)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.runSkillNew([]string{
		"my-test-skill",
		"--description", "A skill for testing the scaffolder.",
		"--triggers", "do x, do y",
	})
	if rc != 0 {
		t.Fatalf("runSkillNew rc = %d, stderr=%s", rc, errb.String())
	}

	skillDir := filepath.Join(dir, "skills", "my-test-skill")
	body, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	bodyStr := string(body)
	for _, want := range []string{
		"name: my-test-skill",
		"A skill for testing the scaffolder.",
		`"do x"`,
		`"do y"`,
		"# my-test-skill",
		"## How to apply",
		"## Resources",
	} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("SKILL.md missing %q\n---\n%s", want, bodyStr)
		}
	}

	// scripts/, references/, assets/ + their .gitkeep stubs.
	for _, sub := range []string{"scripts", "references", "assets"} {
		if _, err := os.Stat(filepath.Join(skillDir, sub, ".gitkeep")); err != nil {
			t.Errorf("subdir %s/.gitkeep missing: %v", sub, err)
		}
	}

	if !strings.Contains(out.String(), "✓ created skill") {
		t.Errorf("stdout should celebrate the create; got %q", out.String())
	}
}

func TestSkillNew_RejectsInvalidName(t *testing.T) {
	withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := app.runSkillNew([]string{"Invalid_Name", "--description", "x"})
	if rc != 2 {
		t.Errorf("invalid name rc = %d, want 2", rc)
	}
}

func TestSkillNew_RequiresDescription(t *testing.T) {
	withFakeClaudeHomeForCLI(t)
	errb := &bytes.Buffer{}
	app := &App{Stdout: &bytes.Buffer{}, Stderr: errb}
	rc := app.runSkillNew([]string{"valid-name"})
	if rc != 2 {
		t.Errorf("missing description rc = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "description is required") {
		t.Errorf("expected description-required hint; got %q", errb.String())
	}
}

func TestSkillNew_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	if rc := app.runSkillNew([]string{"existing", "--description", "first"}); rc != 0 {
		t.Fatalf("first create rc = %d", rc)
	}
	rc := app.runSkillNew([]string{"existing", "--description", "second"})
	if rc != 1 {
		t.Errorf("second create without --force should fail with rc=1; got %d", rc)
	}

	// With --force it succeeds.
	if rc := app.runSkillNew([]string{"existing", "--description", "third", "--force"}); rc != 0 {
		t.Errorf("with --force rc = %d", rc)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "skills", "existing", "SKILL.md"))
	if !strings.Contains(string(body), "third") {
		t.Errorf("--force should overwrite with new description; got:\n%s", body)
	}
}

func TestSkillList_EnumeratesInstalled(t *testing.T) {
	dir := withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	for _, n := range []string{"alpha", "bravo"} {
		if rc := app.runSkillNew([]string{n, "--description", "x"}); rc != 0 {
			t.Fatalf("seeding %s: rc=%d", n, rc)
		}
	}

	out := &bytes.Buffer{}
	app.Stdout = out
	if rc := app.runSkillList(nil); rc != 0 {
		t.Fatalf("list rc = %d", rc)
	}
	got := out.String()
	for _, want := range []string{filepath.Join(dir, "skills"), "alpha", "bravo"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q\n---\n%s", want, got)
		}
	}
}

// TestSkillList_JSONOutput emits a parseable JSON array of
// {skill, root} objects when `--format json` is set. Continues
// the JSON wire-contract series alongside `agents list --json`,
// `rules list --json`, etc.
func TestSkillList_JSONOutput(t *testing.T) {
	dir := withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	for _, n := range []string{"alpha", "bravo"} {
		if rc := app.runSkillNew([]string{n, "--description", "x"}); rc != 0 {
			t.Fatalf("seeding %s: rc=%d", n, rc)
		}
	}

	out := &bytes.Buffer{}
	app.Stdout = out
	if rc := app.runSkillList([]string{"--format", "json"}); rc != 0 {
		t.Fatalf("list --format json rc = %d", rc)
	}

	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '[' {
		t.Fatalf("expected JSON array; got: %q", body)
	}
	var got []struct {
		Skill string `json:"skill"`
		Root  string `json:"root"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	wantRoot := filepath.Join(dir, "skills")
	names := map[string]string{}
	for _, e := range got {
		names[e.Skill] = e.Root
	}
	for _, n := range []string{"alpha", "bravo"} {
		if names[n] != wantRoot {
			t.Errorf("skill %q root = %q, want %q", n, names[n], wantRoot)
		}
	}
}

// TestSkillList_JSONNoSkills emits an empty array (NOT the human
// "(no skills installed)" hint) when the JSON path runs against
// a fresh box. Pipelines must see the same shape across
// configured / unconfigured machines.
func TestSkillList_JSONNoSkills(t *testing.T) {
	withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	out := &bytes.Buffer{}
	app.Stdout = out
	if rc := app.runSkillList([]string{"--format", "json"}); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	got := strings.TrimSpace(out.String())
	if got != "[]" {
		t.Errorf("output = %q, want %q (empty JSON array)", got, "[]")
	}
}

// TestSkillList_TSVOutput confirms the tab-separated path also
// works — same listfmt machinery as the JSON path.
func TestSkillList_TSVOutput(t *testing.T) {
	withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if rc := app.runSkillNew([]string{"alpha", "--description", "x"}); rc != 0 {
		t.Fatalf("seeding rc=%d", rc)
	}

	out := &bytes.Buffer{}
	app.Stdout = out
	if rc := app.runSkillList([]string{"--format", "tsv"}); rc != 0 {
		t.Fatalf("list --format tsv rc = %d", rc)
	}
	body := out.String()
	// First line is header `SKILL\tROOT`, second line is the
	// data row.
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + ≥1 data row; got %q", body)
	}
	if !strings.Contains(lines[0], "SKILL") || !strings.Contains(lines[0], "ROOT") {
		t.Errorf("header line wrong: %q", lines[0])
	}
	if !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[1], "\t") {
		t.Errorf("data row missing alpha or tab: %q", lines[1])
	}
}

// TestSkillList_HumanNoSkills preserves the "(no skills
// installed)" hint when the table path runs against a fresh
// box — interactive operators shouldn't suddenly see just a
// header line.
func TestSkillList_HumanNoSkills(t *testing.T) {
	withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	out := &bytes.Buffer{}
	app.Stdout = out
	if rc := app.runSkillList(nil); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if !strings.Contains(out.String(), "no skills installed") {
		t.Errorf("missing hint; got %q", out.String())
	}
}

func TestSkillPath_FindsByName(t *testing.T) {
	dir := withFakeClaudeHomeForCLI(t)
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if rc := app.runSkillNew([]string{"findable", "--description", "x"}); rc != 0 {
		t.Fatal(rc)
	}
	out := &bytes.Buffer{}
	app.Stdout = out
	rc := app.runSkillPath([]string{"findable"})
	if rc != 0 {
		t.Errorf("path rc = %d", rc)
	}
	if !strings.Contains(out.String(), filepath.Join(dir, "skills", "findable")) {
		t.Errorf("path output wrong: %q", out.String())
	}
}
