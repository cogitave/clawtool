package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func globFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(p, body string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a.go", "package a\n")
	must("b.go", "package b\n")
	must("sub/c.go", "package c\n")
	must("sub/deep/d.go", "package d\n")
	must("README.md", "# readme\n")
	must("data.json", "{}\n")
	return dir
}

func TestGlob_DoubleStar(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob("**/*.go", dir, globDefaultLimit)

	if res.Engine != "doublestar" {
		t.Errorf("engine = %q, want doublestar", res.Engine)
	}
	if res.MatchesCount != 4 {
		t.Errorf("matches_count = %d, want 4 (a.go, b.go, sub/c.go, sub/deep/d.go); got: %v",
			res.MatchesCount, res.Matches)
	}
	for _, m := range res.Matches {
		if !strings.HasSuffix(m, ".go") {
			t.Errorf("non-go match leaked through: %q", m)
		}
		if strings.Contains(m, "\\") {
			t.Errorf("backslash in path %q — expected forward-slash on every OS", m)
		}
	}
}

func TestGlob_TopLevelOnly(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob("*.go", dir, globDefaultLimit)
	if res.MatchesCount != 2 {
		t.Errorf("matches = %d, want 2 (a.go, b.go) for non-recursive *.go; got: %v",
			res.MatchesCount, res.Matches)
	}
}

func TestGlob_LimitCap(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob("**/*.go", dir, 2)
	if res.MatchesCount != 2 {
		t.Errorf("matches = %d, want 2 (cap)", res.MatchesCount)
	}
	if !res.Truncated {
		t.Error("truncated should be true when cap hits")
	}
}

func TestGlob_NoMatch(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob("**/*.zzz", dir, globDefaultLimit)
	if res.MatchesCount != 0 {
		t.Errorf("matches = %d, want 0 for unmatched pattern", res.MatchesCount)
	}
	if res.Truncated {
		t.Error("truncated should be false when no matches")
	}
}

func TestGlob_NonRecursiveByExtension(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob("**/*.md", dir, globDefaultLimit)
	if res.MatchesCount != 1 {
		t.Errorf("matches = %d, want 1 (README.md only)", res.MatchesCount)
	}
}
