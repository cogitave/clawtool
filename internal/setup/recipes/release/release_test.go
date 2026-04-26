package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

// ── release-please ─────────────────────────────────────────────────

func TestReleasePlease_Registered(t *testing.T) {
	r := setup.Lookup("release-please")
	if r == nil {
		t.Fatal("release-please should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryRelease {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestReleasePlease_DetectAbsent(t *testing.T) {
	r := setup.Lookup("release-please")
	dir := t.TempDir()
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
}

func TestReleasePlease_ApplyDropsAllThreeFiles(t *testing.T) {
	r := setup.Lookup("release-please")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, rel := range []string{
		"release-please-config.json",
		".release-please-manifest.json",
		".github/workflows/release-please.yml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s after Apply: %v", rel, err)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusApplied {
		t.Errorf("after Apply, Detect = %q, want Applied", status)
	}
}

func TestReleasePlease_RefusesPreexistingJSON(t *testing.T) {
	r := setup.Lookup("release-please")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "release-please-config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse to overwrite an existing config.json")
	}
}

func TestReleasePlease_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("release-please")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when nothing is installed")
	}
}

// ── goreleaser ─────────────────────────────────────────────────────

func TestGoreleaser_Registered(t *testing.T) {
	r := setup.Lookup("goreleaser")
	if r == nil {
		t.Fatal("goreleaser should self-register")
	}
	if r.Meta().Category != setup.CategoryRelease {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestGoreleaser_DetectAbsent(t *testing.T) {
	r := setup.Lookup("goreleaser")
	dir := t.TempDir()
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestGoreleaser_ApplyThenVerify(t *testing.T) {
	r := setup.Lookup("goreleaser")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusApplied {
		t.Errorf("after Apply, Detect = %q, want Applied", status)
	}
}

func TestGoreleaser_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("goreleaser")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".goreleaser.yaml"), []byte("# user-authored\nversion: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse to overwrite unmanaged .goreleaser.yaml")
	}
}

func TestGoreleaser_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("goreleaser")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply should succeed; got %v", err)
	}
}
