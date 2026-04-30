package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestBifrostTemplate_Registered(t *testing.T) {
	r := setup.Lookup("bifrost-template")
	if r == nil {
		t.Fatal("bifrost-template should self-register")
	}
	m := r.Meta()
	if m.Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q", m.Category)
	}
	if m.Upstream != "https://github.com/maximhq/bifrost" {
		t.Errorf("Upstream = %q, want bifrost upstream", m.Upstream)
	}
	if m.Stability != setup.StabilityExperimental {
		t.Errorf("Stability = %q, want experimental for phase-1 stub", m.Stability)
	}
	if m.Core {
		t.Error("phase-1 bifrost-template must not be Core: opt-in until phase 2")
	}
}

func TestBifrostTemplate_DetectAbsent(t *testing.T) {
	r := setup.Lookup("bifrost-template")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestBifrostTemplate_ApplyDropsTemplate(t *testing.T) {
	r := setup.Lookup("bifrost-template")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool", "bifrost.yaml.template"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Phase-2 contract field names must round-trip — operators
	// editing the template before phase 2 ships rely on these
	// keys being stable.
	for _, key := range []string{"model:", "fallback_chain:", "semantic_cache:", "budget_usd_daily:", "providers:"} {
		if !strings.Contains(s, key) {
			t.Errorf("template missing expected key %q", key)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestBifrostTemplate_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("bifrost-template")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestBifrostTemplate_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("bifrost-template")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool", "bifrost.yaml.template")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\nmodel: gpt-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged bifrost.yaml.template")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestBifrostTemplate_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("bifrost-template")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when template is missing")
	}
}
