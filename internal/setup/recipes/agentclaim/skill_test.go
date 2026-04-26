package agentclaim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/setup"
)

// withFakeClaudeHome redirects skillsRoot() at a tempdir so tests
// don't write under the real ~/.claude/skills.
func withFakeClaudeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := os.Getenv("CLAUDE_HOME")
	os.Setenv("CLAUDE_HOME", dir)
	t.Cleanup(func() { os.Setenv("CLAUDE_HOME", prev) })
	return dir
}

func TestSkill_KarpathyLLMWiki_Registered(t *testing.T) {
	r := setup.Lookup("karpathy-llm-wiki")
	if r == nil {
		t.Fatal("karpathy-llm-wiki should self-register")
	}
	if r.Meta().Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if !strings.Contains(strings.ToLower(r.Meta().Description), "wiki") {
		t.Errorf("description should mention 'wiki': %q", r.Meta().Description)
	}
}

func TestSkill_DetectAbsentInFreshClaudeHome(t *testing.T) {
	withFakeClaudeHome(t)
	r := setup.Lookup("karpathy-llm-wiki")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestSkill_ApplyEmbedsBodyAndStampsMarker(t *testing.T) {
	dir := withFakeClaudeHome(t)
	r := setup.Lookup("karpathy-llm-wiki")
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Reading the file directly verifies the marker prepended to
	// frontmatter survives + the body landed.
	body, err := os.ReadFile(dir + "/skills/karpathy-llm-wiki/SKILL.md")
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	s := string(body)
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written SKILL.md lacks managed-by marker")
	}
	if !strings.Contains(s, "Karpathy LLM Wiki") {
		t.Errorf("body content lost; got first 200 chars: %q", s[:min(200, len(s))])
	}
	// Verify must pass.
	if err := r.Verify(context.Background(), t.TempDir()); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}
}

func TestSkill_ApplyIsIdempotent(t *testing.T) {
	withFakeClaudeHome(t)
	r := setup.Lookup("karpathy-llm-wiki")
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Errorf("re-Apply should succeed; got %v", err)
	}
}

func TestSkill_RefusesUnmanagedFile(t *testing.T) {
	dir := withFakeClaudeHome(t)
	r := setup.Lookup("karpathy-llm-wiki")
	target := dir + "/skills/karpathy-llm-wiki/SKILL.md"
	if err := os.MkdirAll(dir+"/skills/karpathy-llm-wiki", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\nno marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged SKILL.md")
	}
	// Force overrides.
	if err := r.Apply(context.Background(), t.TempDir(), setup.Options{"force": true}); err != nil {
		t.Errorf("Apply with force should succeed: %v", err)
	}
}

// URL-mode skill: serve a fake markdown body via httptest and
// verify the recipe writes it with the marker stamped.
func TestSkill_URLModeFetchesAndStamps(t *testing.T) {
	dir := withFakeClaudeHome(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte("# Hello\nbody from server\n"))
	}))
	defer server.Close()

	prev := skillHTTPClient
	defer func() { skillHTTPClient = prev }()
	skillHTTPClient = &http.Client{Timeout: 3 * time.Second}

	// Build an ad-hoc URL-mode recipe + register it. resetForTest
	// clears the global registry; restore karpathy after.
	url := server.URL
	r := skillRecipe{
		name:        "url-mode-test",
		description: "test-only URL skill",
		upstream:    "https://example.com",
		URL:         url,
	}
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("Apply (URL mode): %v", err)
	}
	body, err := os.ReadFile(dir + "/skills/url-mode-test/SKILL.md")
	if err != nil {
		t.Fatalf("read after URL Apply: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "body from server") {
		t.Errorf("URL body lost: %q", s)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("URL-fetched SKILL.md lacks marker stamp")
	}
}

func TestSkill_URLModeRejectsNonMarkdownContentType(t *testing.T) {
	withFakeClaudeHome(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"oops":true}`))
	}))
	defer server.Close()

	prev := skillHTTPClient
	defer func() { skillHTTPClient = prev }()
	skillHTTPClient = &http.Client{Timeout: 3 * time.Second}

	r := skillRecipe{name: "json-bad", description: "x", upstream: "https://example.com", URL: server.URL}
	if err := r.Apply(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("Apply should refuse a JSON content-type for a SKILL.md fetch")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
