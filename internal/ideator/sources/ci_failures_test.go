package sources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCIFailures_StubGH writes a tiny shell script that mimics
// `gh run list` and confirms the source converts the JSON into
// ideas.
func TestCIFailures_StubGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub gh test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stubPath := filepath.Join(dir, "gh")
	stub := `#!/bin/sh
cat <<'JSON'
[
  {"databaseId": 9001, "name": "ci.yml", "headSha": "abcdef0123456789abcdef0123456789abcdef01", "conclusion": "failure", "event": "push", "createdAt": "2026-04-30T12:00:00Z"},
  {"databaseId": 9002, "name": "release.yml", "headSha": "1234567890abcdef1234567890abcdef12345678", "conclusion": "failure", "event": "push", "createdAt": "2026-04-30T13:00:00Z"}
]
JSON
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	src := NewCIFailures()
	src.GHBinary = stubPath
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 2 {
		t.Fatalf("Scan returned %d ideas, want 2", len(ideas))
	}
	for _, idea := range ideas {
		if !strings.HasPrefix(idea.Title, "CI failure:") {
			t.Fatalf("Title: %q", idea.Title)
		}
		if idea.SuggestedPriority < 5 {
			t.Fatalf("priority too low: %d", idea.SuggestedPriority)
		}
		if idea.DedupeKey == "" {
			t.Fatalf("DedupeKey empty")
		}
	}
}

// TestCIFailures_MissingBinaryIsNoOp confirms a missing gh binary
// returns no ideas + no error (cheap-on-fail).
func TestCIFailures_MissingBinaryIsNoOp(t *testing.T) {
	src := NewCIFailures()
	src.GHBinary = "/nonexistent/path/to/gh-binary-that-cannot-exist"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}
