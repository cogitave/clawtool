package sources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestStaleBranches_StubGit writes a stub `git` that mimics
// `git branch -r --merged origin/main` output and asserts the
// source filters by prefix + caps the result set.
func TestStaleBranches_StubGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub git test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "git")
	body := `#!/bin/sh
# Mimic ` + "`git branch -r --merged origin/main`" + ` output.
cat <<'OUT'
  origin/HEAD -> origin/main
  origin/main
  origin/autodev/feat-a
  origin/autodev/feat-b
  origin/autodev/feat-c
  origin/feature/non-autodev
OUT
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	src := NewStaleBranches()
	src.GitBinary = stub

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 3 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 3 (autodev/* only): %v", len(ideas), titles)
	}
	for _, idea := range ideas {
		if !strings.Contains(idea.Title, "autodev/") {
			t.Errorf("non-autodev branch leaked: %q", idea.Title)
		}
		if idea.SuggestedPriority != 3 {
			t.Errorf("priority = %d, want 3", idea.SuggestedPriority)
		}
		if !strings.HasPrefix(idea.DedupeKey, "stale_branches:") {
			t.Errorf("DedupeKey = %q, want prefix stale_branches:", idea.DedupeKey)
		}
	}
}

// TestStaleBranches_HonorsMaxIdeas confirms the cap drops branches
// past the limit so a long-running autonomous loop doesn't drown
// the autopilot queue.
func TestStaleBranches_HonorsMaxIdeas(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub git test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "git")
	body := `#!/bin/sh
cat <<'OUT'
  origin/autodev/01
  origin/autodev/02
  origin/autodev/03
  origin/autodev/04
  origin/autodev/05
  origin/autodev/06
  origin/autodev/07
OUT
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	src := NewStaleBranches()
	src.GitBinary = stub
	src.MaxIdeas = 3

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 3 {
		t.Fatalf("Scan returned %d ideas, want 3 (capped)", len(ideas))
	}
}

// TestStaleBranches_MissingGitIsNoOp confirms a missing git binary
// returns no ideas + no error.
func TestStaleBranches_MissingGitIsNoOp(t *testing.T) {
	src := NewStaleBranches()
	src.GitBinary = "/nonexistent/path/to/git"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}
