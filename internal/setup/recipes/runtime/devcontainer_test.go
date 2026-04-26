package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestDevcontainer_Registered(t *testing.T) {
	r := setup.Lookup("devcontainer")
	if r == nil {
		t.Fatal("devcontainer should self-register")
	}
	if r.Meta().Category != setup.CategoryRuntime {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestDevcontainer_DetectAbsent(t *testing.T) {
	r := setup.Lookup("devcontainer")
	dir := t.TempDir()
	status, detail, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
	if !strings.Contains(detail, "no language manifest") {
		t.Errorf("detail should hint at missing manifest: %q", detail)
	}
}

func TestDevcontainer_ApplyDropsLanguageSpecificImage(t *testing.T) {
	r := setup.Lookup("devcontainer")
	cases := []struct {
		manifest string
		seed     string
		want     string
	}{
		{"go.mod", "module x\n", "devcontainers/go"},
		{"package.json", "{}\n", "typescript-node"},
		{"requirements.txt", "x\n", "devcontainers/python"},
		{"Cargo.toml", "[package]\nname = \"x\"\n", "devcontainers/rust"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.manifest), []byte(c.seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := r.Apply(context.Background(), dir, nil); err != nil {
			t.Fatalf("Apply (manifest=%s): %v", c.manifest, err)
		}
		body, err := os.ReadFile(filepath.Join(dir, ".devcontainer/devcontainer.json"))
		if err != nil {
			t.Fatalf("read after Apply: %v", err)
		}
		if !setup.HasMarker(body, setup.ManagedByMarker) {
			t.Errorf("manifest=%s: marker missing", c.manifest)
		}
		if !strings.Contains(string(body), c.want) {
			t.Errorf("manifest=%s: image hint %q not in body:\n%s", c.manifest, c.want, body)
		}
		if err := r.Verify(context.Background(), dir); err != nil {
			t.Errorf("manifest=%s: Verify: %v", c.manifest, err)
		}
	}
}

func TestDevcontainer_ApplyErrorsWithoutManifest(t *testing.T) {
	r := setup.Lookup("devcontainer")
	if err := r.Apply(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("Apply should error on a tempdir with no language manifest")
	}
}

func TestDevcontainer_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("devcontainer")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, ".devcontainer/devcontainer.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"name":"user-authored"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged devcontainer.json")
	}
}

func TestDevcontainer_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("devcontainer")
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

func TestDevcontainer_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("devcontainer")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when devcontainer.json is missing")
	}
}
