package supplychain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestDependabot_Registered(t *testing.T) {
	r := setup.Lookup("dependabot")
	if r == nil {
		t.Fatal("dependabot recipe should self-register via init()")
	}
	if r.Meta().Category != setup.CategorySupplyChain {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestDependabot_DetectAbsent(t *testing.T) {
	r := setup.Lookup("dependabot")
	dir := t.TempDir()
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
}

func TestDependabot_ErrorsWhenNoEcosystem(t *testing.T) {
	r := setup.Lookup("dependabot")
	err := r.Apply(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Apply should error on a repo with no recognized ecosystem")
	}
	if !strings.Contains(err.Error(), "no recognized package ecosystems") {
		t.Errorf("error should mention 'no recognized package ecosystems': %v", err)
	}
}

func TestDependabot_DetectsGoEcosystem(t *testing.T) {
	r := setup.Lookup("dependabot")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".github/dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `package-ecosystem: "gomod"`) {
		t.Errorf("gomod ecosystem missing from dependabot.yml: %s", s)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
}

func TestDependabot_DetectsMultipleEcosystems(t *testing.T) {
	r := setup.Lookup("dependabot")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte("name: ci\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".github/dependabot.yml"))
	s := string(body)
	for _, eco := range []string{"gomod", "npm", "github-actions"} {
		if !strings.Contains(s, `package-ecosystem: "`+eco+`"`) {
			t.Errorf("ecosystem %q missing from dependabot.yml", eco)
		}
	}
}

func TestDependabot_RefusesOverwriteOfUnmanagedFile(t *testing.T) {
	r := setup.Lookup("dependabot")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, ".github/dependabot.yml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored, no marker\nversion: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged dependabot.yml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestDependabot_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("dependabot")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestDependabot_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("dependabot")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when file is missing")
	}
}
