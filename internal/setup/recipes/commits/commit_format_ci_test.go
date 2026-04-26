package commits

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestCommitFormatCI_Registered(t *testing.T) {
	r := setup.Lookup("conventional-commits-ci")
	if r == nil {
		t.Fatal("recipe should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryCommits {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set (wrap-don't-reinvent enforcement)")
	}
}

func TestCommitFormatCI_DetectAbsent(t *testing.T) {
	r := setup.Lookup("conventional-commits-ci")
	dir := t.TempDir()
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
}

func TestCommitFormatCI_ApplyThenVerify(t *testing.T) {
	r := setup.Lookup("conventional-commits-ci")
	dir := t.TempDir()

	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	written, err := os.ReadFile(filepath.Join(dir, ".github/workflows/commit-format.yml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(written, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by: clawtool marker")
	}

	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}

	// Detect should now report StatusApplied.
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if status != setup.StatusApplied {
		t.Errorf("after Apply, Detect = %q, want %q", status, setup.StatusApplied)
	}
}

func TestCommitFormatCI_RefusesOverwriteOfUnmanagedFile(t *testing.T) {
	r := setup.Lookup("conventional-commits-ci")
	dir := t.TempDir()
	target := filepath.Join(dir, ".github/workflows/commit-format.yml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored workflow, no marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := r.Apply(context.Background(), dir, nil)
	if err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged file")
	}

	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged existing file should detect as Partial; got %q", status)
	}
}

func TestCommitFormatCI_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("conventional-commits-ci")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	// Re-applying over our own marker is allowed (idempotent).
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after re-Apply: %v", err)
	}
}

func TestCommitFormatCI_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("conventional-commits-ci")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when file is missing")
	}
}
