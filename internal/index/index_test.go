package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkByLines(t *testing.T) {
	body := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n")
	got := chunkByLines(body, 2)
	if len(got) != 3 {
		t.Fatalf("expected 3 chunks; got %d", len(got))
	}
	if got[0].text != "a\nb" || got[0].start != 1 || got[0].end != 2 {
		t.Errorf("chunk 0: %+v", got[0])
	}
	if got[2].text != "e" || got[2].start != 5 || got[2].end != 5 {
		t.Errorf("chunk 2: %+v", got[2])
	}
}

func TestShouldIgnore(t *testing.T) {
	repo := "/repo"
	cases := []struct {
		path  string
		want  bool
		label string
	}{
		{"/repo/.git/HEAD", true, "dotgit"},
		{"/repo/node_modules/foo/index.js", true, "node_modules"},
		{"/repo/vendor/x/y.go", true, "vendor"},
		{"/repo/internal/x.go", false, "ordinary source"},
		{"/repo/dist/bundle.js", true, "dist"},
		{"/repo/cmd/main.go", false, "cmd"},
	}
	patterns := []string{".git/**", "node_modules/**", "vendor/**", "dist/**"}
	for _, c := range cases {
		got := shouldIgnore(repo, c.path, patterns)
		if got != c.want {
			t.Errorf("%s: shouldIgnore(%q) = %v, want %v", c.label, c.path, got, c.want)
		}
	}
}

func TestContainsNUL(t *testing.T) {
	if !containsNUL([]byte{1, 2, 0, 3}) {
		t.Error("should detect NUL")
	}
	if containsNUL([]byte("hello world")) {
		t.Error("plain text should not flag NUL")
	}
}

func TestCollectionTag_Stable(t *testing.T) {
	a := collectionTag("/some/repo")
	b := collectionTag("/some/repo")
	if a != b {
		t.Errorf("collectionTag should be deterministic; got %q vs %q", a, b)
	}
	if a == collectionTag("/different/path") {
		t.Errorf("collectionTag should differ across paths")
	}
}

func TestParseInt(t *testing.T) {
	if parseInt("42") != 42 {
		t.Error("parseInt 42")
	}
	if parseInt("0") != 0 {
		t.Error("parseInt 0")
	}
	if parseInt("12abc") != 12 {
		t.Error("parseInt should stop on non-digit")
	}
	if parseInt("") != 0 {
		t.Error("parseInt empty should be 0")
	}
}

func TestSearch_BeforeBuildErrors(t *testing.T) {
	s := New(t.TempDir(), Options{})
	_, err := s.Search(context.Background(), "anything", 10)
	if err == nil {
		t.Error("Search before Build should error")
	}
}

func TestBuild_RequiresEmbeddingKey(t *testing.T) {
	// Without OPENAI_API_KEY, the openai provider should refuse Init.
	t.Setenv("OPENAI_API_KEY", "")
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hello"), 0o644)
	s := New(repo, Options{Provider: "openai"})
	err := s.Build(context.Background())
	if err == nil {
		t.Error("Build without OPENAI_API_KEY should error on openai provider")
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	s := New("/tmp/repo", Options{})
	if s.opts.MaxFileBytes <= 0 {
		t.Error("default MaxFileBytes should be set")
	}
	if len(s.opts.Ignore) == 0 {
		t.Error("default Ignore patterns should be set")
	}
	if s.opts.Provider != "openai" {
		t.Errorf("default Provider: got %q, want openai", s.opts.Provider)
	}
}
