package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestGuardiansStub_Registered(t *testing.T) {
	r := setup.Lookup("guardians-stub")
	if r == nil {
		t.Fatal("guardians-stub should self-register")
	}
	m := r.Meta()
	if m.Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q", m.Category)
	}
	if m.Upstream != "https://github.com/metareflection/guardians" {
		t.Errorf("Upstream = %q, want metareflection/guardians upstream", m.Upstream)
	}
	if m.Stability != setup.StabilityExperimental {
		t.Errorf("Stability = %q, want experimental for phase-1 stub", m.Stability)
	}
	if m.Core {
		t.Error("phase-1 guardians-stub must not be Core: opt-in until phase 2")
	}
}

func TestGuardiansStub_DetectAbsent(t *testing.T) {
	r := setup.Lookup("guardians-stub")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestGuardiansStub_ApplyDropsTemplate(t *testing.T) {
	r := setup.Lookup("guardians-stub")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool", "rules", "guardians-stub.toml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Phase-1 rule shape — the predicate name + event name +
	// quoted plan arg must round-trip exactly. Operators editing
	// the template before phase 2 ships rely on these tokens
	// being stable.
	for _, key := range []string{
		`when = "pre_send"`,
		`condition = 'guardians_check("plan")'`,
		`name = "guardians-presend"`,
		"managed-by: clawtool",
	} {
		if !strings.Contains(s, key) {
			t.Errorf("template missing expected token %q", key)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestGuardiansStub_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("guardians-stub")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestGuardiansStub_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("guardians-stub")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool", "rules", "guardians-stub.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\n[[rule]]\nname = \"mine\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged guardians-stub.toml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestGuardiansStub_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("guardians-stub")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when template is missing")
	}
}
