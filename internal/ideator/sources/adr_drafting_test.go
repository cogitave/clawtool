package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedClock pins time.Now-equivalent at a deterministic instant
// so the staleness math in the source under test is reproducible
// regardless of when CI runs. Restored via the returned cleanup.
func fixedClock(t *testing.T, at time.Time) func() {
	t.Helper()
	prev := nowFn
	nowFn = func() time.Time { return at }
	return func() { nowFn = prev }
}

// writeADR is a small helper that lays down `wiki/decisions/<name>`
// inside dir with the supplied body. Returns the relative path.
func writeADR(t *testing.T, dir, name, body string) string {
	t.Helper()
	adrDir := filepath.Join(dir, "wiki", "decisions")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	full := filepath.Join(adrDir, name)
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write adr: %v", err)
	}
	rel, _ := filepath.Rel(dir, full)
	return rel
}

// TestADRDrafting_SurfacesStale30PlusDayDraftingADRs is the
// happy path: an ADR with `status: drafting` and an `updated:`
// date >30 days behind the clock must produce one Idea with
// priority=5, evidence pointing at the ADR, and a SuggestedPrompt
// that mentions the staleness.
func TestADRDrafting_SurfacesStale30PlusDayDraftingADRs(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cleanup := fixedClock(t, now)
	defer cleanup()

	dir := t.TempDir()
	body := strings.Join([]string{
		"---",
		"type: decision",
		"title: \"099 Stale draft\"",
		"status: drafting",
		"created: 2026-01-15",
		"updated: 2026-01-15", // 106 days behind the clock
		"---",
		"",
		"# 099 Stale draft",
		"",
		"Body.",
	}, "\n")
	rel := writeADR(t, dir, "099-stale-draft.md", body)

	src := NewADRDrafting()
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		t.Fatalf("Scan returned %d ideas, want 1", len(ideas))
	}
	idea := ideas[0]
	if idea.SuggestedPriority != 5 {
		t.Errorf("priority = %d, want 5", idea.SuggestedPriority)
	}
	if idea.Evidence != rel {
		t.Errorf("evidence = %q, want %q", idea.Evidence, rel)
	}
	if !strings.Contains(idea.SuggestedPrompt, "drafting") {
		t.Errorf("prompt missing 'drafting': %q", idea.SuggestedPrompt)
	}
	if !strings.Contains(idea.SuggestedPrompt, "Promote to 'accepted'") {
		t.Errorf("prompt missing canonical recovery phrasing: %q", idea.SuggestedPrompt)
	}
	if !strings.Contains(idea.SuggestedPrompt, "days ago") {
		t.Errorf("prompt missing 'days ago': %q", idea.SuggestedPrompt)
	}
	if idea.DedupeKey == "" {
		t.Errorf("DedupeKey is empty")
	}
}

// TestADRDrafting_SkipsAcceptedADRs guards against the source
// emitting Ideas for ADRs whose status is anything other than
// `drafting` (mature, accepted, superseded, …). Stale or fresh,
// non-drafting status is out of scope.
func TestADRDrafting_SkipsAcceptedADRs(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cleanup := fixedClock(t, now)
	defer cleanup()

	dir := t.TempDir()
	statuses := []string{"accepted", "mature", "superseded", "rejected"}
	for i, s := range statuses {
		body := strings.Join([]string{
			"---",
			"type: decision",
			"status: " + s,
			"updated: 2026-01-01", // very stale, must still be skipped
			"---",
			"",
			"# Test",
		}, "\n")
		writeADR(t, dir, "0"+string(rune('0'+i))+"0-"+s+".md", body)
	}

	src := NewADRDrafting()
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0 (statuses %v should never surface)", len(ideas), statuses)
	}
}

// TestADRDrafting_SkipsRecentDraftingADRs covers the < 30 day
// boundary: a drafting ADR updated within the last month is
// active work, not decision-debt — leave it alone.
func TestADRDrafting_SkipsRecentDraftingADRs(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cleanup := fixedClock(t, now)
	defer cleanup()

	dir := t.TempDir()
	// 10 days behind — comfortably under 30.
	body := strings.Join([]string{
		"---",
		"status: drafting",
		"updated: 2026-04-21",
		"---",
		"# Test",
	}, "\n")
	writeADR(t, dir, "100-fresh-draft.md", body)
	// Exactly 30 days behind — boundary case, must NOT surface
	// (the source uses `age > 30d`, so age ≤ 30d stays quiet).
	// `updated:` is parsed as midnight UTC of that date, so to
	// land exactly on the threshold relative to a noon clock we
	// need the date to be 30d behind the noon instant.
	bodyEdge := strings.Join([]string{
		"---",
		"status: drafting",
		"updated: 2026-04-01T12:00:00Z",
		"---",
		"# Test",
	}, "\n")
	writeADR(t, dir, "101-edge-draft.md", bodyEdge)

	src := NewADRDrafting()
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0 (recent + boundary drafts should not surface)", len(ideas))
	}
}

// TestADRDrafting_MissingWikiIsNoOp is the cheap-on-fail contract:
// no wiki/ directory → empty result + nil error, no warning that
// counts as a "real" failure.
func TestADRDrafting_MissingWikiIsNoOp(t *testing.T) {
	src := NewADRDrafting()
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}
