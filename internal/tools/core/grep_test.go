package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture writes two files into a fresh tmp dir for grep tests.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(p, body string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a.go", "package a\n\nfunc Hello() string {\n\treturn \"world\"\n}\n")
	must("b.go", "package a\n\nfunc Bye() string {\n\treturn \"world\"\n}\n")
	must("c.txt", "this contains world too\n")
	return dir
}

func TestGrep_FindsMatches(t *testing.T) {
	dir := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := executeGrep(ctx, grepArgs{
		Pattern:    "world",
		Cwd:        dir,
		Path:       ".",
		MaxMatches: 100,
	})

	if res.Engine == "none" {
		t.Skip("no grep engine on this system")
	}
	if res.MatchesCount < 3 {
		t.Errorf("matches_count = %d, want >= 3 (a.go, b.go, c.txt all contain 'world')", res.MatchesCount)
	}
	if res.Truncated {
		t.Errorf("truncated = true with cap 100 and only 3 matches")
	}

	// Sanity check: at least one match cites a.go on a real line.
	found := false
	for _, m := range res.Matches {
		if strings.HasSuffix(m.Path, "a.go") && m.Line >= 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("no match cited a.go: %+v", res.Matches)
	}
}

func TestGrep_FilterByGlob(t *testing.T) {
	dir := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := executeGrep(ctx, grepArgs{
		Pattern:    "world",
		Cwd:        dir,
		Path:       ".",
		Glob:       "*.go",
		MaxMatches: 100,
	})
	if res.Engine == "none" {
		t.Skip("no grep engine on this system")
	}

	for _, m := range res.Matches {
		if !strings.HasSuffix(m.Path, ".go") {
			t.Errorf("glob *.go matched non-go file: %s", m.Path)
		}
	}
	if res.MatchesCount == 0 {
		t.Errorf("expected at least one .go match")
	}
}

func TestGrep_TruncatesAtMaxMatches(t *testing.T) {
	dir := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := executeGrep(ctx, grepArgs{
		Pattern:    "world",
		Cwd:        dir,
		Path:       ".",
		MaxMatches: 1,
	})
	if res.Engine == "none" {
		t.Skip("no grep engine on this system")
	}
	if res.MatchesCount != 1 {
		t.Errorf("matches_count = %d, want 1 with cap 1", res.MatchesCount)
	}
	// ripgrep applies max-count per file so total may equal cap; either way
	// the result must reflect the cap correctly. Truncation flag is set
	// when we stop reading; check that we did not exceed the cap.
	if len(res.Matches) > 1 {
		t.Errorf("matches = %d, want <= 1", len(res.Matches))
	}
}

func TestGrep_PatternNotFound(t *testing.T) {
	dir := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res := executeGrep(ctx, grepArgs{
		Pattern:    "ZZZ-no-such-pattern-ZZZ",
		Cwd:        dir,
		Path:       ".",
		MaxMatches: 100,
	})
	if res.Engine == "none" {
		t.Skip("no grep engine on this system")
	}
	if res.MatchesCount != 0 {
		t.Errorf("matches_count = %d, want 0 for absent pattern", res.MatchesCount)
	}
	if res.Truncated {
		t.Errorf("truncated should be false when no matches")
	}
}

func TestGrep_CaseInsensitive(t *testing.T) {
	dir := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resLower := executeGrep(ctx, grepArgs{
		Pattern: "WORLD", Cwd: dir, Path: ".", MaxMatches: 100,
		IgnoreCase: false,
	})
	resI := executeGrep(ctx, grepArgs{
		Pattern: "WORLD", Cwd: dir, Path: ".", MaxMatches: 100,
		IgnoreCase: true,
	})
	if resI.Engine == "none" {
		t.Skip("no grep engine on this system")
	}
	if resI.MatchesCount <= resLower.MatchesCount {
		t.Errorf("case-insensitive should match more: i=%d, ci=%d",
			resLower.MatchesCount, resI.MatchesCount)
	}
}
