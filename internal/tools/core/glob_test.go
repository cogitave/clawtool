package core

import (
	"os"
	"os/exec"
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
	res := executeGlob(globArgs{Pattern: "**/*.go", Cwd: dir, Limit: globDefaultLimit})

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
	res := executeGlob(globArgs{Pattern: "*.go", Cwd: dir, Limit: globDefaultLimit})
	if res.MatchesCount != 2 {
		t.Errorf("matches = %d, want 2 (a.go, b.go) for non-recursive *.go; got: %v",
			res.MatchesCount, res.Matches)
	}
}

func TestGlob_LimitCap(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob(globArgs{Pattern: "**/*.go", Cwd: dir, Limit: 2})
	if res.MatchesCount != 2 {
		t.Errorf("matches = %d, want 2 (cap)", res.MatchesCount)
	}
	if !res.Truncated {
		t.Error("truncated should be true when cap hits")
	}
}

func TestGlob_NoMatch(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob(globArgs{Pattern: "**/*.zzz", Cwd: dir, Limit: globDefaultLimit})
	if res.MatchesCount != 0 {
		t.Errorf("matches = %d, want 0 for unmatched pattern", res.MatchesCount)
	}
	if res.Truncated {
		t.Error("truncated should be false when no matches")
	}
}

func TestGlob_NonRecursiveByExtension(t *testing.T) {
	dir := globFixture(t)
	res := executeGlob(globArgs{Pattern: "**/*.md", Cwd: dir, Limit: globDefaultLimit})
	if res.MatchesCount != 1 {
		t.Errorf("matches = %d, want 1 (README.md only)", res.MatchesCount)
	}
}

func TestGlob_GitignoreSkipsIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("tracked.txt", "x")
	mustWrite("ignored.log", "y")
	mustWrite("vendor/lib.go", "z")
	mustWrite(".gitignore", "*.log\nvendor/\n")

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// respect_gitignore=true (default) → ignored paths excluded.
	res := executeGlob(globArgs{
		Pattern: "**/*", Cwd: dir, Limit: globDefaultLimit,
		RespectGitignore: true,
	})
	for _, m := range res.Matches {
		if strings.Contains(m, "ignored.log") || strings.HasPrefix(m, "vendor/") {
			t.Errorf("git-ls-files should have excluded %q: %v", m, res.Matches)
		}
	}
	if res.Engine != "doublestar+git-ls-files" {
		t.Errorf("expected git-aware engine label, got %q", res.Engine)
	}

	// respect_gitignore=false → legacy walker sees everything.
	res2 := executeGlob(globArgs{
		Pattern: "**/*", Cwd: dir, Limit: globDefaultLimit,
		RespectGitignore: false,
	})
	hasIgnored := false
	for _, m := range res2.Matches {
		if strings.Contains(m, "ignored.log") {
			hasIgnored = true
		}
	}
	if !hasIgnored {
		t.Errorf("respect_gitignore=false should surface ignored.log; got %v", res2.Matches)
	}
}

func TestGlob_HiddenFilesDefaultExcluded(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"visible.txt", ".secret"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := executeGlob(globArgs{
		Pattern: "*", Cwd: dir, Limit: globDefaultLimit,
		RespectGitignore: false, IncludeHidden: false,
	})
	for _, m := range res.Matches {
		if m == ".secret" {
			t.Error("dotfile should be hidden by default")
		}
	}

	// include_hidden=true surfaces it.
	res2 := executeGlob(globArgs{
		Pattern: "*", Cwd: dir, Limit: globDefaultLimit,
		RespectGitignore: false, IncludeHidden: true,
	})
	if !containsString(res2.Matches, ".secret") {
		t.Errorf("include_hidden=true should surface .secret: %v", res2.Matches)
	}

	// Explicit dot pattern overrides include_hidden=false.
	res3 := executeGlob(globArgs{
		Pattern: ".secret", Cwd: dir, Limit: globDefaultLimit,
		RespectGitignore: false, IncludeHidden: false,
	})
	if !containsString(res3.Matches, ".secret") {
		t.Errorf("explicit dot pattern should match dotfile: %v", res3.Matches)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
