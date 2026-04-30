package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlaybookListArchon_TextAndJSON exercises the new verb end-to-end:
// drops a fixture workflow under <tmp>/.archon/workflows, then runs the
// CLI verb in both default-text and --format json modes and asserts the
// surface the operator (and phase 2) will rely on.
func TestPlaybookListArchon_TextAndJSON(t *testing.T) {
	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".archon", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `name: sample-feature
description: Test workflow.
nodes:
  - id: plan
    prompt: "Plan it."
  - id: build
    depends_on: [plan]
    bash: "echo build"
`
	if err := os.WriteFile(filepath.Join(wfDir, "sample.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Text mode.
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"playbook", "list-archon", "--dir", dir})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0\nstderr: %s", rc, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "sample-feature — 2 nodes") {
		t.Errorf("text output missing summary line:\n%s", got)
	}

	// JSON mode.
	out.Reset()
	errb.Reset()
	rc = app.Run([]string{"playbook", "list-archon", "--dir", dir, "--format", "json"})
	if rc != 0 {
		t.Fatalf("rc = %d (json), want 0\nstderr: %s", rc, errb.String())
	}
	var rows []struct {
		Name      string `json:"name"`
		NodeCount int    `json:"node_count"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json decode: %v\nbody: %s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Name != "sample-feature" || rows[0].NodeCount != 2 {
		t.Errorf("json shape wrong: %+v", rows)
	}
	if !strings.HasSuffix(rows[0].Path, "sample.yaml") {
		t.Errorf("path not surfaced: %q", rows[0].Path)
	}
}
