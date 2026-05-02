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

// TestADRQuestions_ParsesNumberedAndBulletedLists is the operator-
// reported regression: an ADR with a real-world `## Open questions`
// section that mixes bullet items, numbered items, sub-headers, and
// bold/italic formatting must still emit one Idea per line. Prior
// to the v0.22.120 fix, the parser bailed out on the first `###`
// sub-header and emitted zero Ideas from the entire section.
func TestADRQuestions_ParsesNumberedAndBulletedLists(t *testing.T) {
	dir := t.TempDir()
	adrDir := filepath.Join(dir, "wiki", "decisions")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		"# 035 Self-direction",
		"",
		"## Decision",
		"",
		"Three-layer stack.",
		"",
		"## Open questions",
		"",
		"- Bullet open question one?",
		"- **Bold bullet** — second bullet question?",
		"1. Numbered open question alpha?",
		"2. Numbered open question beta?",
		"3) Paren-numbered open question gamma?",
		"",
		"### Sub-section inside open questions",
		"",
		"- Nested bullet question?",
		"1. Nested numbered question?",
		"",
		"## Cross-links",
		"",
		"- Should NOT surface as an open question.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(adrDir, "035-self-direction-autonomy-stack.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write adr: %v", err)
	}

	src := NewADRQuestions()
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	wantTexts := []string{
		"Bullet open question one",
		"Bold bullet",
		"Numbered open question alpha",
		"Numbered open question beta",
		"Paren-numbered open question gamma",
		"Nested bullet question",
		"Nested numbered question",
	}
	if len(ideas) != len(wantTexts) {
		t.Fatalf("Scan returned %d ideas, want %d (got: %v)", len(ideas), len(wantTexts), ideaTitles(ideas))
	}
	for _, want := range wantTexts {
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
	// Cross-links bullet must NOT surface.
	for _, idea := range ideas {
		if strings.Contains(idea.Title, "Should NOT surface") || strings.Contains(idea.SuggestedPrompt, "Should NOT surface") {
			t.Fatalf("Cross-links bullet leaked into open questions: %+v", idea)
		}
	}
}

// TestADRQuestions_PermissiveHeaderMatch covers headers that include
// trailing qualifiers — operator's wiki has both "Open questions /
// risks" and "Open questions (deferred)" in real ADRs.
func TestADRQuestions_PermissiveHeaderMatch(t *testing.T) {
	cases := []string{
		"## Open questions",
		"## open questions",
		"### Open Questions",
		"## Open questions / risks",
		"## Open questions (deferred)",
		"## Open Question",
		"## Open Questions — 2026-04",
	}
	for _, h := range cases {
		if !isOpenQuestionsHeader(h) {
			t.Errorf("isOpenQuestionsHeader(%q) = false, want true", h)
		}
	}
	// Negative cases — must NOT match.
	for _, h := range []string{
		"## Decision",
		"## Open observations", // distinct section, intentional miss
		"## Closed questions",
	} {
		if isOpenQuestionsHeader(h) {
			t.Errorf("isOpenQuestionsHeader(%q) = true, want false", h)
		}
	}
}

func ideaTitles(ideas []ideator.Idea) []string {
	out := make([]string, len(ideas))
	for i, idea := range ideas {
		out[i] = idea.Title
	}
	return out
}
