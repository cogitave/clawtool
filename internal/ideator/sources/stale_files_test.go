package sources

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStaleFiles_RealRepo creates a tiny git repo with two .go files
// — one committed long ago, one recent — and asserts that only the
// old one surfaces.
func TestStaleFiles_RealRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git plumbing test relies on /bin/sh; skip on Windows runners")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q", "-b", "main")
	run("git", "config", "commit.gpgsign", "false")

	// Old file — backdate the commit using GIT_AUTHOR/COMMITTER_DATE.
	if err := os.WriteFile(filepath.Join(dir, "old.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	oldCmd := exec.Command("git", "commit", "-q", "-am", "old", "--allow-empty")
	oldCmd.Args = []string{"git", "add", "old.go"}
	oldCmd.Dir = dir
	if out, err := oldCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add old: %v\n%s", err, out)
	}
	old := exec.Command("git", "commit", "-q", "-m", "old")
	old.Dir = dir
	old.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE=2025-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2025-01-01T00:00:00Z",
	)
	if out, err := old.CombinedOutput(); err != nil {
		t.Fatalf("git commit old: %v\n%s", err, out)
	}

	// Fresh file.
	if err := os.WriteFile(filepath.Join(dir, "fresh.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	run("git", "add", "fresh.go")
	freshCmd := exec.Command("git", "commit", "-q", "-m", "fresh")
	freshCmd.Dir = dir
	freshCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := freshCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit fresh: %v\n%s", err, out)
	}

	src := NewStaleFiles()
	src.MinAge = 30 * 24 * time.Hour
	// Pin "now" so old.go is reliably ancient regardless of when CI
	// runs — old date is 2025-01-01, "now" is 2026-06-01 → ~17 mo.
	src.Now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 1 (only old.go): %v", len(ideas), titles)
	}
	if !strings.Contains(ideas[0].Title, "old.go") {
		t.Errorf("Title = %q, want contains old.go", ideas[0].Title)
	}
	if ideas[0].SuggestedPriority != 2 {
		t.Errorf("priority = %d, want 2 (low — heuristic, not signal-driven)", ideas[0].SuggestedPriority)
	}
}

// TestStaleFiles_MissingGitIsNoOp confirms a missing git binary
// returns no ideas + no error (cheap-on-fail).
func TestStaleFiles_MissingGitIsNoOp(t *testing.T) {
	src := NewStaleFiles()
	src.GitBinary = "/nonexistent/path/to/git-binary"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestStaleFiles_SkipsPaths verifies the skip-substring filter drops
// _test.go and /testdata/ files.
func TestStaleFiles_SkipsPaths(t *testing.T) {
	cases := map[string]bool{
		"internal/foo/foo.go":         false,
		"internal/foo/foo_test.go":    true,
		"internal/foo/testdata/x.go":  true,
		"internal/foo/x.pb.go":        true,
		"internal/foo/x_generated.go": true,
		"vendor/github.com/x/y/y.go":  true,
		"internal/foo/bar.go":         false,
	}
	src := NewStaleFiles()
	for path, wantSkip := range cases {
		got := shouldSkipPath(path, src.SkipPaths)
		if got != wantSkip {
			t.Errorf("shouldSkipPath(%q) = %v, want %v", path, got, wantSkip)
		}
	}
}
