package ideator

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/cogitave/clawtool/internal/autopilot"
)

// stubSource lets tests inject deterministic Idea slices without
// touching disk or external CLIs.
type stubSource struct {
	name  string
	ideas []Idea
	err   error
	calls atomic.Int32
}

func (s *stubSource) Name() string { return s.name }
func (s *stubSource) Scan(ctx context.Context, repoRoot string) ([]Idea, error) {
	s.calls.Add(1)
	return append([]Idea(nil), s.ideas...), s.err
}

// TestRun_DedupesAndCaps drives the orchestrator's three load-bearing
// behaviours: (1) run every source in parallel, (2) drop second-and-later
// occurrences of the same DedupeKey, (3) cap output at TopK after
// sorting by SuggestedPriority desc.
func TestRun_DedupesAndCaps(t *testing.T) {
	s1 := &stubSource{
		name: "alpha",
		ideas: []Idea{
			{SuggestedPrompt: "P-low", SuggestedPriority: 1, DedupeKey: "k1"},
			{SuggestedPrompt: "P-high", SuggestedPriority: 9, DedupeKey: "k2"},
		},
	}
	s2 := &stubSource{
		name: "beta",
		ideas: []Idea{
			// Duplicate of k1 — should be deduped.
			{SuggestedPrompt: "duplicate", SuggestedPriority: 5, DedupeKey: "k1"},
			{SuggestedPrompt: "P-mid", SuggestedPriority: 3, DedupeKey: "k3"},
			{SuggestedPrompt: "P-bottom", SuggestedPriority: 0, DedupeKey: "k4"},
		},
	}

	res, err := Run(context.Background(), Options{
		RepoRoot: t.TempDir(),
		TopK:     3,
		Sources:  []IdeaSource{s1, s2},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := s1.calls.Load(); got != 1 {
		t.Fatalf("s1 calls: got %d want 1", got)
	}
	if got := s2.calls.Load(); got != 1 {
		t.Fatalf("s2 calls: got %d want 1", got)
	}
	if res.Deduped != 1 {
		t.Fatalf("Deduped: got %d want 1", res.Deduped)
	}
	if len(res.Ideas) != 3 {
		t.Fatalf("Ideas len: got %d want 3", len(res.Ideas))
	}
	// Order: priority desc — 9, 3, 1 (k4 with prio=0 falls off the cap).
	wantPrios := []int{9, 3, 1}
	for i, w := range wantPrios {
		if res.Ideas[i].SuggestedPriority != w {
			t.Fatalf("Ideas[%d] priority: got %d want %d", i, res.Ideas[i].SuggestedPriority, w)
		}
	}
	// Per-source counts must reflect raw emission, not post-dedupe.
	if res.PerSource["alpha"] != 2 || res.PerSource["beta"] != 3 {
		t.Fatalf("PerSource: %+v", res.PerSource)
	}
}

// TestRun_SourceFilter restricts execution to a named source.
func TestRun_SourceFilter(t *testing.T) {
	yes := &stubSource{name: "yes", ideas: []Idea{{SuggestedPrompt: "yes-1", DedupeKey: "y"}}}
	no := &stubSource{name: "no", ideas: []Idea{{SuggestedPrompt: "no-1", DedupeKey: "n"}}}
	res, err := Run(context.Background(), Options{
		RepoRoot:     t.TempDir(),
		SourceFilter: "yes",
		Sources:      []IdeaSource{yes, no},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if no.calls.Load() != 0 {
		t.Fatalf("filtered-out source ran: calls=%d", no.calls.Load())
	}
	if yes.calls.Load() != 1 {
		t.Fatalf("filtered-in source: calls=%d want 1", yes.calls.Load())
	}
	if len(res.Ideas) != 1 || res.Ideas[0].SourceName != "yes" {
		t.Fatalf("Ideas: %+v", res.Ideas)
	}
}

// TestRun_SourceErrorIsCheap proves a single source returning an
// error doesn't poison the orchestrator: surviving sources still
// emit, and the error surfaces in SourceErrors without short-circuiting.
func TestRun_SourceErrorIsCheap(t *testing.T) {
	bad := &stubSource{name: "bad", err: errors.New("boom")}
	good := &stubSource{name: "good", ideas: []Idea{{SuggestedPrompt: "still here", DedupeKey: "g"}}}
	res, err := Run(context.Background(), Options{
		RepoRoot: t.TempDir(),
		Sources:  []IdeaSource{bad, good},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.SourceErrors["bad"]; got == "" {
		t.Fatalf("SourceErrors missing bad: %+v", res.SourceErrors)
	}
	if len(res.Ideas) != 1 {
		t.Fatalf("Ideas len: %d want 1", len(res.Ideas))
	}
}

// TestRun_NoSources fails loud rather than silently producing
// nothing — a misconfigured caller deserves to know.
func TestRun_NoSources(t *testing.T) {
	if _, err := Run(context.Background(), Options{RepoRoot: t.TempDir()}); err == nil {
		t.Fatalf("Run with no sources accepted; want error")
	}
}

// TestRunAndQueue_Pushes_AsProposed proves RunAndQueue writes each
// surviving Idea into the autopilot store at status=proposed and
// returns Added/Skipped counts.
func TestRunAndQueue_Pushes_AsProposed(t *testing.T) {
	q := autopilot.OpenAt(filepath.Join(t.TempDir(), "queue.toml"))
	src := &stubSource{
		name: "alpha",
		ideas: []Idea{
			{SuggestedPrompt: "first", DedupeKey: "k1", SuggestedPriority: 5, Evidence: "foo.go:10"},
			{SuggestedPrompt: "second", DedupeKey: "k2", SuggestedPriority: 1},
		},
	}
	res, err := RunAndQueue(context.Background(), Options{
		RepoRoot: t.TempDir(),
		Sources:  []IdeaSource{src},
	}, q)
	if err != nil {
		t.Fatalf("RunAndQueue: %v", err)
	}
	if res.Added != 2 || res.Skipped != 0 {
		t.Fatalf("Added/Skipped: %+v want added=2 skipped=0", res)
	}
	items, err := q.List(autopilot.StatusProposed)
	if err != nil {
		t.Fatalf("List proposed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("proposed items: got %d want 2", len(items))
	}
	for _, it := range items {
		if it.Source != "alpha" {
			t.Fatalf("Item.Source: got %q want %q", it.Source, "alpha")
		}
		if it.DedupeKey == "" {
			t.Fatalf("Item.DedupeKey empty: %+v", it)
		}
	}

	// Re-running Mongolia should dedupe — no items added, all skipped.
	res2, err := RunAndQueue(context.Background(), Options{
		RepoRoot: t.TempDir(),
		Sources:  []IdeaSource{src},
	}, q)
	if err != nil {
		t.Fatalf("RunAndQueue 2: %v", err)
	}
	if res2.Added != 0 || res2.Skipped != 2 {
		t.Fatalf("re-run added/skipped: %+v want added=0 skipped=2", res2)
	}

	// Confirm Claim still doesn't see proposed items — operator gate intact.
	if _, ok, err := q.Claim(); err != nil || ok {
		t.Fatalf("Claim with only-proposed queue: ok=%v err=%v", ok, err)
	}
}

// TestRun_TitleDefaultedFromPrompt proves the orchestrator backfills
// Title from the first line of SuggestedPrompt when the source left
// it empty — keeps source authors honest without losing readable
// CLI output.
func TestRun_TitleDefaultedFromPrompt(t *testing.T) {
	src := &stubSource{
		name: "alpha",
		ideas: []Idea{
			{SuggestedPrompt: "Investigate the foo TODO\n\n  - body", DedupeKey: "k"},
		},
	}
	res, err := Run(context.Background(), Options{RepoRoot: t.TempDir(), Sources: []IdeaSource{src}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Ideas) != 1 {
		t.Fatalf("Ideas len: %d", len(res.Ideas))
	}
	if res.Ideas[0].Title != "Investigate the foo TODO" {
		t.Fatalf("Title: got %q", res.Ideas[0].Title)
	}
	if res.Ideas[0].SourceName != "alpha" {
		t.Fatalf("SourceName: got %q", res.Ideas[0].SourceName)
	}
}

// TestRun_DryLoopDiagnostic confirms the framework emits one
// synthetic meta-Idea when every source returns 0 — the
// architectural fix for "the autonomous loop went silent for 3h
// because Ideator had no signal."
func TestRun_DryLoopDiagnostic(t *testing.T) {
	dry := &stubSource{name: "dry-a", ideas: nil}
	dry2 := &stubSource{name: "dry-b", ideas: nil}
	res, err := Run(context.Background(), Options{
		RepoRoot: t.TempDir(),
		Sources:  []IdeaSource{dry, dry2},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Ideas) != 1 {
		t.Fatalf("Ideas len: %d, want 1 (synthetic dry-loop)", len(res.Ideas))
	}
	if res.Ideas[0].DedupeKey != "ideator:dry-loop" {
		t.Errorf("DedupeKey: got %q, want ideator:dry-loop", res.Ideas[0].DedupeKey)
	}
	if res.Ideas[0].SuggestedPriority != 1 {
		t.Errorf("Priority: got %d, want 1", res.Ideas[0].SuggestedPriority)
	}
	if res.Ideas[0].Title != "Ideator dry — all configured sources returned 0" {
		t.Errorf("Title: got %q", res.Ideas[0].Title)
	}
}

// TestRun_DryLoopSuppressed confirms SuppressDryDiagnostic=true
// keeps the legacy "no ideas" honest path for operator-driven
// `clawtool ideate` invocations.
func TestRun_DryLoopSuppressed(t *testing.T) {
	dry := &stubSource{name: "dry", ideas: nil}
	res, err := Run(context.Background(), Options{
		RepoRoot:              t.TempDir(),
		Sources:               []IdeaSource{dry},
		SuppressDryDiagnostic: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Ideas) != 0 {
		t.Fatalf("Ideas len: %d, want 0 (suppressed)", len(res.Ideas))
	}
}

// TestRun_DryLoopSkippedWhenSourceProduces confirms the diagnostic
// stays out of the way once any source has real signal — the goal
// is "never silent", not "always synthetic."
func TestRun_DryLoopSkippedWhenSourceProduces(t *testing.T) {
	loud := &stubSource{
		name: "loud",
		ideas: []Idea{
			{SuggestedPrompt: "real work", DedupeKey: "k1"},
		},
	}
	dry := &stubSource{name: "dry", ideas: nil}
	res, err := Run(context.Background(), Options{
		RepoRoot: t.TempDir(),
		Sources:  []IdeaSource{loud, dry},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Ideas) != 1 {
		t.Fatalf("Ideas len: %d, want 1 (loud's idea, no diagnostic)", len(res.Ideas))
	}
	if res.Ideas[0].DedupeKey == "ideator:dry-loop" {
		t.Errorf("dry-loop diagnostic leaked when real signal exists")
	}
}
