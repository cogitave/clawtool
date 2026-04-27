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

func TestGrep_ContextLines(t *testing.T) {
	if LookupEngine("rg").Bin == "" {
		t.Skip("ripgrep not on PATH; context lines need rg --json")
	}
	dir := t.TempDir()
	body := "line one\nline two\nMATCH here\nline four\nline five\n"
	if err := os.WriteFile(filepath.Join(dir, "ctx.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res := executeGrep(context.Background(), grepArgs{
		Pattern:       "MATCH",
		Patterns:      []string{"MATCH"},
		Cwd:           dir,
		Path:          ".",
		MaxMatches:    10,
		ContextBefore: 2,
		ContextAfter:  2,
	})
	if res.MatchesCount != 1 {
		t.Fatalf("matches=%d, want 1", res.MatchesCount)
	}
	m := res.Matches[0]
	if len(m.Before) != 2 {
		t.Errorf("Before=%v, want 2 lines", m.Before)
	}
	if len(m.After) != 2 {
		t.Errorf("After=%v, want 2 lines", m.After)
	}
	if !strings.Contains(strings.Join(m.Before, "\n"), "line two") {
		t.Errorf("Before missing 'line two': %v", m.Before)
	}
	if !strings.Contains(strings.Join(m.After, "\n"), "line four") {
		t.Errorf("After missing 'line four': %v", m.After)
	}
}

func TestGrep_MultiPattern(t *testing.T) {
	if LookupEngine("rg").Bin == "" {
		t.Skip("ripgrep not on PATH")
	}
	dir := t.TempDir()
	body := "alpha\nbeta\ngamma\ndelta\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res := executeGrep(context.Background(), grepArgs{
		Pattern:    "alpha",
		Patterns:   []string{"alpha", "gamma"},
		Cwd:        dir,
		Path:       ".",
		MaxMatches: 10,
	})
	if res.MatchesCount != 2 {
		t.Fatalf("multi-pattern should match 2 lines, got %d: %+v", res.MatchesCount, res.Matches)
	}
}

func TestGrep_TruncationMessageMentionsHardCap(t *testing.T) {
	res := GrepResult{
		BaseResult:   BaseResult{Operation: "Grep", Engine: "ripgrep"},
		Pattern:      "x",
		Matches:      []GrepMatch{{Path: "f", Line: 1, Column: 1, Text: "x"}},
		MatchesCount: 1,
		Truncated:    true,
	}
	out := res.Render()
	if !strings.Contains(out, "raise max_matches") {
		t.Errorf("truncation footer should hint at the cap: %s", out)
	}
}
