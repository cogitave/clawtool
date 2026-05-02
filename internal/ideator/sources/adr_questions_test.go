package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/ideator"
)

// TestADRQuestions_ParsesOpenQuestionsBlock writes a fake ADR with
// an "## Open questions" section, then confirms one Idea per
// numbered/bulleted line is emitted.
func TestADRQuestions_ParsesOpenQuestionsBlock(t *testing.T) {
	dir := t.TempDir()
	adrDir := filepath.Join(dir, "wiki", "decisions")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "# 035 Self-direction\n\nStatus: accepted\n\n" +
		"## Decision\n\nThree-layer stack.\n\n" +
		"## Open questions\n\n" +
		"- Cron-driven ideate?\n" +
		"- Per-source rate limits?\n" +
		"1. Ideator kill switch?\n" +
		"\n## Cross-links\n\n" +
		"- Should not surface as an open question.\n"
	if err := os.WriteFile(filepath.Join(adrDir, "035-self-direction.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write adr: %v", err)
	}

	src := NewADRQuestions()
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 3 {
		t.Fatalf("Scan returned %d ideas, want 3 (got: %v)", len(ideas), ideaTitles(ideas))
	}
	for _, want := range []string{"Cron-driven ideate", "Per-source rate limits", "Ideator kill switch"} {
		found := false
		for _, idea := range ideas {
			if strings.Contains(idea.Title, want) || strings.Contains(idea.SuggestedPrompt, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing idea for %q in %v", want, ideaTitles(ideas))
		}
	}
	// Each idea must carry a stable DedupeKey.
	seen := map[string]struct{}{}
	for _, idea := range ideas {
		if idea.DedupeKey == "" {
			t.Fatalf("idea %q has empty DedupeKey", idea.Title)
		}
		if _, dup := seen[idea.DedupeKey]; dup {
			t.Fatalf("duplicate DedupeKey across ideas: %s", idea.DedupeKey)
		}
		seen[idea.DedupeKey] = struct{}{}
	}
}

// TestADRQuestions_MissingDirIsNoOp confirms a repo without a wiki/
// directory returns empty + nil (cheap-on-fail).
func TestADRQuestions_MissingDirIsNoOp(t *testing.T) {
	src := NewADRQuestions()
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

func ideaTitles(ideas []ideator.Idea) []string {
	out := make([]string, len(ideas))
	for i, idea := range ideas {
		out[i] = idea.Title
	}
	return out
}
