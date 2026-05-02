package sources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPRReviewPending_StubGH writes a stub `gh` that emits two PRs
// — one fresh (12h old, dropped by MinAge), one stale (5 days old,
// kept). Asserts the stale PR surfaces with the right priority.
func TestPRReviewPending_StubGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub gh test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	body := `#!/bin/sh
cat <<'JSON'
[
  {"number": 7, "title": "Fresh PR", "createdAt": "2026-05-02T00:00:00Z", "author": {"login": "alice"}, "reviewDecision": ""},
  {"number": 12, "title": "Add SafeSkill security badge", "createdAt": "2026-04-28T00:00:00Z", "author": {"login": "bob"}, "reviewDecision": "REVIEW_REQUIRED"}
]
JSON
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	src := NewPRReviewPending()
	src.GHBinary = stub
	// Pin "now" so the 5-day vs 12h cutoff is deterministic.
	src.Now = func() time.Time { return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC) }

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 1 (fresh dropped, stale kept): %v", len(ideas), titles)
	}
	if !strings.Contains(ideas[0].Title, "#12") {
		t.Errorf("surviving Title = %q, want contains #12", ideas[0].Title)
	}
	if ideas[0].SuggestedPriority != 5 {
		t.Errorf("priority = %d, want 5 (3-7 day band)", ideas[0].SuggestedPriority)
	}
	if !strings.HasPrefix(ideas[0].DedupeKey, "pr_review_pending:") {
		t.Errorf("DedupeKey = %q, want prefix pr_review_pending:", ideas[0].DedupeKey)
	}
}

// TestPRReviewPending_MissingBinaryIsNoOp confirms a missing gh
// binary returns no ideas + no error (cheap-on-fail).
func TestPRReviewPending_MissingBinaryIsNoOp(t *testing.T) {
	src := NewPRReviewPending()
	src.GHBinary = "/nonexistent/path/to/gh"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestPriorityForPR covers the age-banded priority helper.
func TestPriorityForPR(t *testing.T) {
	cases := []struct {
		days int
		want int
	}{
		{0, 4}, {1, 4}, {2, 4},
		{3, 5}, {4, 5}, {6, 5},
		{7, 6}, {30, 6},
	}
	for _, tc := range cases {
		if got := priorityForPR(tc.days); got != tc.want {
			t.Errorf("priorityForPR(%d) = %d, want %d", tc.days, got, tc.want)
		}
	}
}
