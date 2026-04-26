package governance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestCodeowners_Registered(t *testing.T) {
	r := setup.Lookup("codeowners")
	if r == nil {
		t.Fatal("codeowners recipe should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryGovernance {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestCodeowners_DetectAbsent(t *testing.T) {
	r := setup.Lookup("codeowners")
	dir := t.TempDir()
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
}

func TestCodeowners_ApplyThenVerify(t *testing.T) {
	r := setup.Lookup("codeowners")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, setup.Options{
		"owners": []string{"@bahadirarda"},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, ".github/CODEOWNERS"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	body := string(written)
	if !setup.HasMarker(written, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	if !strings.Contains(body, "* @bahadirarda") {
		t.Errorf("catch-all rule missing: %s", body)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}
}

func TestCodeowners_GeneratesRulesFromOptions(t *testing.T) {
	r := setup.Lookup("codeowners")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, setup.Options{
		"owners": []string{"@x"},
		"paths": []map[string]any{
			{"pattern": "*.go", "owners": []string{"@y"}},
			{"pattern": "/docs/", "owners": []string{"@z"}},
		},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".github/CODEOWNERS"))
	s := string(body)
	if !strings.Contains(s, "* @x") {
		t.Error("catch-all rule missing")
	}
	if !strings.Contains(s, "*.go @y") {
		t.Error("per-pattern rule for *.go missing")
	}
	if !strings.Contains(s, "/docs/ @z") {
		t.Error("per-pattern rule for /docs/ missing")
	}
}

func TestCodeowners_RequiresOwners(t *testing.T) {
	r := setup.Lookup("codeowners")
	err := r.Apply(context.Background(), t.TempDir(), setup.Options{})
	if err == nil {
		t.Fatal("Apply should require owners option")
	}
}

func TestCodeowners_RefusesOverwriteOfUnmanagedFile(t *testing.T) {
	r := setup.Lookup("codeowners")
	dir := t.TempDir()
	target := filepath.Join(dir, ".github/CODEOWNERS")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("* @user-wrote-this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := r.Apply(context.Background(), dir, setup.Options{"owners": []string{"@x"}})
	if err == nil {
		t.Fatal("Apply should refuse to overwrite unmanaged CODEOWNERS")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged existing file should detect as Partial; got %q", status)
	}
}

func TestCodeowners_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("codeowners")
	dir := t.TempDir()
	opts := setup.Options{"owners": []string{"@x"}}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestCodeowners_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("codeowners")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when file is missing")
	}
}
