package quality

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestRtkTokenFilter_Registered(t *testing.T) {
	r := setup.Lookup("rtk-token-filter")
	if r == nil {
		t.Fatal("rtk-token-filter should self-register")
	}
	m := r.Meta()
	if m.Category != setup.CategoryQuality {
		t.Errorf("wrong category: %q", m.Category)
	}
	if m.Upstream == "" {
		t.Error("Upstream must be set")
	}
	if !m.Core {
		t.Error("rtk-token-filter must be Core: operator wants the rewrite rule on by default")
	}
	if m.Stability != setup.StabilityBeta {
		t.Errorf("Stability = %q, want beta", m.Stability)
	}
}

func TestRtkTokenFilter_DetectAbsent(t *testing.T) {
	r := setup.Lookup("rtk-token-filter")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestRtkTokenFilter_ApplyDropsConfig(t *testing.T) {
	r := setup.Lookup("rtk-token-filter")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool", "rtk-rewrite-list.toml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Every name in the canonical default allowlist must appear in
	// the generated TOML — that's the contract the rewrite helper
	// reads for its rule decisions.
	for _, cmd := range []string{"git", "ls", "grep", "cat", "head", "tail", "find", "tree", "diff", "stat", "wc", "awk", "sed"} {
		if !strings.Contains(s, `"`+cmd+`"`) {
			t.Errorf("allowlist command %q missing from generated config", cmd)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestRtkTokenFilter_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("rtk-token-filter")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool", "rtk-rewrite-list.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\ncommands = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged rtk-rewrite-list.toml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestRtkTokenFilter_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("rtk-token-filter")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestRtkTokenFilter_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("rtk-token-filter")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when config is missing")
	}
}
