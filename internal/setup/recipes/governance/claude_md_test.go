package governance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

// ── claude-md ──────────────────────────────────────────────────────

func TestClaudeMD_Registered(t *testing.T) {
	r := setup.Lookup("claude-md")
	if r == nil {
		t.Fatal("claude-md should self-register")
	}
	if r.Meta().Category != setup.CategoryGovernance {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestClaudeMD_DetectAbsent(t *testing.T) {
	r := setup.Lookup("claude-md")
	dir := t.TempDir()
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestClaudeMD_ApplyDropsTemplateWithSubstitutions(t *testing.T) {
	r := setup.Lookup("claude-md")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, setup.Options{
		"project": "neat-thing",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(body)
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("CLAUDE.md lacks managed-by marker")
	}
	if !strings.Contains(s, "neat-thing") {
		t.Errorf("project name not substituted: %s", s)
	}
	if !strings.Contains(s, "Go") {
		t.Errorf("language hint (Go) should be substituted from go.mod presence: %s", s)
	}
}

func TestClaudeMD_ApplyDefaultsLanguageToPolyglotWhenNoManifest(t *testing.T) {
	r := setup.Lookup("claude-md")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(body), "polyglot") {
		t.Errorf("expected 'polyglot' fallback in language line; got:\n%s", body)
	}
}

func TestClaudeMD_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("claude-md")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# user wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged CLAUDE.md")
	}
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("Apply with force should succeed: %v", err)
	}
}

func TestClaudeMD_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("claude-md")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply should succeed; got %v", err)
	}
}

// ── agents-md ──────────────────────────────────────────────────────

func TestAgentsMD_Registered(t *testing.T) {
	r := setup.Lookup("agents-md")
	if r == nil {
		t.Fatal("agents-md should self-register")
	}
	if r.Meta().Category != setup.CategoryGovernance {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestAgentsMD_ApplyThenVerify(t *testing.T) {
	r := setup.Lookup("agents-md")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	s := string(body)
	if !strings.Contains(s, "AGENTS.md") {
		t.Error("body missing self-title")
	}
	if !strings.Contains(s, "agents.md") {
		t.Error("body should mention the spec source")
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// detectLanguageHint is a pure helper; quick coverage to lock it.
func TestDetectLanguageHint(t *testing.T) {
	cases := []struct {
		seed string
		want string
	}{
		{"go.mod", "Go"},
		{"Cargo.toml", "Rust"},
		{"package.json", "Node.js / TypeScript"},
		{"requirements.txt", "Python"},
		{"pyproject.toml", "Python"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.seed), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := detectLanguageHint(dir); got != c.want {
			t.Errorf("detectLanguageHint(%s): got %q, want %q", c.seed, got, c.want)
		}
	}
	if got := detectLanguageHint(t.TempDir()); got != "" {
		t.Errorf("empty dir should return empty string; got %q", got)
	}
}
