package ci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestGHActionsTest_Registered(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
	if r == nil {
		t.Fatal("gh-actions-test should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryCI {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestDetectLanguage_PicksFirstMatch(t *testing.T) {
	cases := []struct {
		seed []string
		want language
	}{
		{[]string{"go.mod"}, langGo},
		{[]string{"Cargo.toml"}, langRust},
		{[]string{"package.json"}, langNode},
		{[]string{"requirements.txt"}, langPython},
		{[]string{"pyproject.toml"}, langPython},
		// Order priority: go beats node when both exist.
		{[]string{"go.mod", "package.json"}, langGo},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, f := range c.seed {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got, ok := detectLanguage(dir)
		if !ok {
			t.Errorf("seed=%v: detectLanguage returned ok=false", c.seed)
			continue
		}
		if got != c.want {
			t.Errorf("seed=%v: got %q, want %q", c.seed, got, c.want)
		}
	}
}

func TestDetectLanguage_NoMatchOnEmptyDir(t *testing.T) {
	if _, ok := detectLanguage(t.TempDir()); ok {
		t.Error("empty dir should not match any language")
	}
}

func TestGHActionsTest_DetectAbsentEmptyDir(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
	dir := t.TempDir()
	status, detail, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
	if !strings.Contains(detail, "no language manifest") {
		t.Errorf("detail should hint at the missing manifest, got %q", detail)
	}
}

func TestGHActionsTest_ApplyDropsLanguageSpecificWorkflow(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
	cases := []struct {
		manifest    string
		seedContent string
		expectMatch string
	}{
		{"go.mod", "module x\n", "go test -race -count=1 ./..."},
		{"package.json", "{}\n", "npm test"},
		{"requirements.txt", "pytest\n", "pytest"},
		{"Cargo.toml", "[package]\nname = \"x\"\n", "cargo test --all"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.manifest), []byte(c.seedContent), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := r.Apply(context.Background(), dir, nil); err != nil {
			t.Fatalf("Apply (manifest=%s): %v", c.manifest, err)
		}
		body, err := os.ReadFile(filepath.Join(dir, ".github/workflows/test.yml"))
		if err != nil {
			t.Fatalf("read after Apply: %v", err)
		}
		if !setup.HasMarker(body, setup.ManagedByMarker) {
			t.Errorf("manifest=%s: marker missing", c.manifest)
		}
		if !strings.Contains(string(body), c.expectMatch) {
			t.Errorf("manifest=%s: workflow missing %q; got:\n%s", c.manifest, c.expectMatch, string(body))
		}
		if err := r.Verify(context.Background(), dir); err != nil {
			t.Errorf("manifest=%s: Verify: %v", c.manifest, err)
		}
	}
}

func TestGHActionsTest_ApplyErrorsWhenNoManifest(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
	err := r.Apply(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Apply should error on a tempdir with no language manifest")
	}
	if !strings.Contains(err.Error(), "no language manifest") {
		t.Errorf("error should mention 'no language manifest': %v", err)
	}
}

func TestGHActionsTest_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, ".github/workflows/test.yml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("name: user-authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged test.yml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestGHActionsTest_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
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

func TestGHActionsTest_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("gh-actions-test")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when test.yml is missing")
	}
}
