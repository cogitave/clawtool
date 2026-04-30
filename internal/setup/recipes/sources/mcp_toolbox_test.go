package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestMcpToolbox_Registered(t *testing.T) {
	r := setup.Lookup("mcp-toolbox")
	if r == nil {
		t.Fatal("mcp-toolbox should self-register")
	}
	m := r.Meta()
	if m.Category != setup.CategoryRuntime {
		t.Errorf("wrong category: %q", m.Category)
	}
	if m.Upstream == "" {
		t.Error("Upstream must be set")
	}
	if m.Stability != setup.StabilityBeta {
		t.Errorf("stability = %q, want beta", m.Stability)
	}
	if m.Core {
		t.Error("Core must be false — DB integration is opt-in")
	}
}

func TestMcpToolbox_DetectAbsent(t *testing.T) {
	r := setup.Lookup("mcp-toolbox")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestMcpToolbox_ApplyDropsConfig(t *testing.T) {
	r := setup.Lookup("mcp-toolbox")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool/mcp-toolbox/tools.yaml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Both starter source profiles must be present (commented).
	for _, kind := range []string{"kind: postgres", "kind: sqlite"} {
		if !strings.Contains(s, kind) {
			t.Errorf("template missing %q profile", kind)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestMcpToolbox_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("mcp-toolbox")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool/mcp-toolbox/tools.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\nsources: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged tools.yaml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestMcpToolbox_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("mcp-toolbox")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}
