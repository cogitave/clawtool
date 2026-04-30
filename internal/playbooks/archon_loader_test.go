package playbooks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeWorkflow drops a fixture under <dir>/.archon/workflows/<name>.
// All four tests share this helper so the directory layout stays
// identical to LoadFromDir's expected shape.
func writeWorkflow(t *testing.T, dir, name, body string) {
	t.Helper()
	wfDir := filepath.Join(dir, ".archon", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadFromDir_EmptyDirReturnsNil(t *testing.T) {
	wfs, err := LoadFromDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadFromDir on empty dir: %v", err)
	}
	if len(wfs) != 0 {
		t.Errorf("got %d workflows from empty dir, want 0", len(wfs))
	}
}

func TestLoadFromDir_WellFormedWorkflow(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "build-feature.yaml", `name: build-feature
description: |
  Plan, implement, validate.
nodes:
  - id: plan
    prompt: "Explore the codebase."
  - id: run-tests
    depends_on: [plan]
    bash: "go test ./..."
  - id: review
    depends_on: [run-tests]
    loop:
      prompt: "Review the diff."
      until: APPROVED
      max_iterations: 3
      fresh_context: true
  - id: dispatch
    depends_on: [review]
    command: archon-create-pr
`)
	wfs, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if len(wfs) != 1 {
		t.Fatalf("got %d workflows, want 1", len(wfs))
	}
	w := wfs[0]
	if w.Name != "build-feature" {
		t.Errorf("Name = %q, want build-feature", w.Name)
	}
	if w.Description == "" {
		t.Error("Description should be populated")
	}
	if len(w.Nodes) != 4 {
		t.Fatalf("got %d nodes, want 4", len(w.Nodes))
	}
	want := []string{"prompt", "bash", "loop", "command"}
	for i, k := range want {
		if w.Nodes[i].Kind != k {
			t.Errorf("nodes[%d].Kind = %q, want %q", i, w.Nodes[i].Kind, k)
		}
	}
	if w.Nodes[2].Loop == nil || w.Nodes[2].Loop.Until != "APPROVED" || w.Nodes[2].Loop.MaxIterations != 3 {
		t.Errorf("loop projection wrong: %+v", w.Nodes[2].Loop)
	}
	if got := w.Summary(); got != "build-feature — 4 nodes" {
		t.Errorf("Summary = %q", got)
	}
}

func TestLoadFromDir_UnknownKindIsTagged(t *testing.T) {
	dir := t.TempDir()
	// Future kinds upstream might add (parallel, mcp_call) — phase 1
	// must surface them as unsupported:<kind> rather than erroring.
	writeWorkflow(t, dir, "future.yaml", `name: future
nodes:
  - id: par
    parallel:
      branches: [a, b]
  - id: mcp
    mcp_call:
      tool: github.create_issue
`)
	wfs, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if len(wfs) != 1 || len(wfs[0].Nodes) != 2 {
		t.Fatalf("unexpected shape: %+v", wfs)
	}
	wantKinds := map[string]bool{
		"unsupported:parallel": false,
		"unsupported:mcp_call": false,
	}
	for _, n := range wfs[0].Nodes {
		if _, ok := wantKinds[n.Kind]; ok {
			wantKinds[n.Kind] = true
		} else {
			t.Errorf("unexpected node kind: %q (id=%s)", n.Kind, n.ID)
		}
	}
	for k, seen := range wantKinds {
		if !seen {
			t.Errorf("missing expected kind %q", k)
		}
	}
}

func TestLoadFromDir_MalformedYAMLReturnsTypedError(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "broken.yaml", "name: broken\nnodes: [\n  - id: x\n  invalid: : :")
	_, err := LoadFromDir(dir)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !errors.Is(err, ErrArchonYAMLParse) {
		t.Errorf("error %v should wrap ErrArchonYAMLParse", err)
	}
}
