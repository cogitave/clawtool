package quality

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

// ── prettier ───────────────────────────────────────────────────────

func TestPrettier_Registered(t *testing.T) {
	r := setup.Lookup("prettier")
	if r == nil {
		t.Fatal("prettier should self-register")
	}
	if r.Meta().Category != setup.CategoryQuality {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestPrettier_DetectAbsent(t *testing.T) {
	r := setup.Lookup("prettier")
	dir := t.TempDir()
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestPrettier_ApplyDropsBothFiles(t *testing.T) {
	r := setup.Lookup("prettier")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, rel := range []string{".prettierrc.json", ".prettierignore"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s after Apply: %v", rel, err)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestPrettier_RefusesPreexistingConfig(t *testing.T) {
	r := setup.Lookup("prettier")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".prettierrc.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse pre-existing .prettierrc.json (JSON can't be marker-tagged)")
	}
}

func TestPrettier_RefusesUnmanagedIgnore(t *testing.T) {
	r := setup.Lookup("prettier")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".prettierignore"), []byte("# user-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged .prettierignore")
	}
}

func TestPrettier_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("prettier")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when files are missing")
	}
}

// ── golangci-lint ──────────────────────────────────────────────────

func TestGolangciLint_Registered(t *testing.T) {
	r := setup.Lookup("golangci-lint")
	if r == nil {
		t.Fatal("golangci-lint should self-register")
	}
	if r.Meta().Category != setup.CategoryQuality {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestGolangciLint_DetectAbsentNoGoMod(t *testing.T) {
	r := setup.Lookup("golangci-lint")
	dir := t.TempDir()
	status, detail, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
	if !strings.Contains(detail, "go.mod") {
		t.Errorf("detail should mention missing go.mod: %q", detail)
	}
}

func TestGolangciLint_ApplyRequiresGoMod(t *testing.T) {
	r := setup.Lookup("golangci-lint")
	if err := r.Apply(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("Apply should require go.mod")
	}
}

func TestGolangciLint_ApplyThenVerify(t *testing.T) {
	r := setup.Lookup("golangci-lint")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".golangci.yml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("golangci config lacks marker")
	}
	if !strings.Contains(string(body), "errcheck") {
		t.Errorf("config missing errcheck linter: %s", body)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestGolangciLint_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("golangci-lint")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".golangci.yml"), []byte("# user-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged .golangci.yml")
	}
}

func TestGolangciLint_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("golangci-lint")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply should succeed; got %v", err)
	}
}
