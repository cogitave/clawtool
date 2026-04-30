package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestArchonTemplate_Registered(t *testing.T) {
	r := setup.Lookup("archon-template")
	if r == nil {
		t.Fatal("archon-template should self-register")
	}
	if r.Meta().Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestArchonTemplate_DetectAbsent(t *testing.T) {
	r := setup.Lookup("archon-template")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestArchonTemplate_ApplyDropsTemplate(t *testing.T) {
	r := setup.Lookup("archon-template")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".archon", "workflows", "idea-to-pr.yaml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Sample must demonstrate every supported node kind so
	// `clawtool playbook list-archon` shows non-zero coverage.
	for _, want := range []string{"prompt:", "bash:", "loop:", "until:"} {
		if !strings.Contains(s, want) {
			t.Errorf("template missing %q", want)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestArchonTemplate_RefusesUnmanaged(t *testing.T) {
	r := setup.Lookup("archon-template")
	dir := t.TempDir()
	target := filepath.Join(dir, ".archon", "workflows", "idea-to-pr.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\nname: mine\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged template")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestArchonTemplate_Idempotent(t *testing.T) {
	r := setup.Lookup("archon-template")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}
