package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestSemble_Registered(t *testing.T) {
	r := setup.Lookup("semble")
	if r == nil {
		t.Fatal("semble should self-register")
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
		t.Error("Core must be false — code-search MCP is opt-in")
	}
}

func TestSemble_DetectAbsent(t *testing.T) {
	r := setup.Lookup("semble")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestSemble_ApplyDropsConfig(t *testing.T) {
	r := setup.Lookup("semble")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool/semble/config.yaml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Marker file must point operators at the real install step.
	for _, want := range []string{"clawtool source add semble", "uvx"} {
		if !strings.Contains(s, want) {
			t.Errorf("template missing %q hint", want)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestSemble_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("semble")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool/semble/config.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\nsemble: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged config.yaml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestSemble_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("semble")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}
