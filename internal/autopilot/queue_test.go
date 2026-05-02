package autopilot

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// helper: open a fresh queue under t.TempDir().
func newQueue(t *testing.T) *Queue {
	t.Helper()
	return OpenAt(filepath.Join(t.TempDir(), "queue.toml"))
}

// TestQueue_HappyPath drives the canonical agent loop:
//
//	add → claim → complete → claim returns empty.
//
// This is the primitive's whole reason to exist; if it ever
// regresses every other test in this file is moot.
func TestQueue_HappyPath(t *testing.T) {
	q := newQueue(t)
	added, err := q.Add("ship the autopilot primitive", 0, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" || added.Status != StatusPending {
		t.Fatalf("Add returned bad item: %+v", added)
	}

	got, ok, err := q.Claim()
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	if got.ID != added.ID || got.Status != StatusInProgress {
		t.Fatalf("Claim returned wrong item: %+v", got)
	}
	if got.ClaimedAt.IsZero() {
		t.Fatalf("Claim did not stamp ClaimedAt")
	}

	done, err := q.Complete(got.ID, "merged into main")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if done.Status != StatusDone || done.DoneAt.IsZero() {
		t.Fatalf("Complete left bad state: %+v", done)
	}

	// queue is now empty — Claim returns ok=false.
	if _, ok, err := q.Claim(); err != nil || ok {
		t.Fatalf("Claim on drained queue: ok=%v err=%v (want ok=false)", ok, err)
	}
}

// TestQueue_PriorityAndOrder confirms the picker honors priority
// (higher first) then created_at (earlier first). Equal priority
// + same-tick adds tie-break on ID lexicographically; the generator
// already orders newer adds with a higher counter so the earlier
// add wins.
func TestQueue_PriorityAndOrder(t *testing.T) {
	q := newQueue(t)
	low, _ := q.Add("low-prio", 0, "")
	high, _ := q.Add("high-prio", 5, "")
	mid, _ := q.Add("mid-prio", 1, "")

	got, _, err := q.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.ID != high.ID {
		t.Fatalf("Claim picked %q, want highest-priority %q", got.ID, high.ID)
	}
	got, _, err = q.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.ID != mid.ID {
		t.Fatalf("Claim picked %q, want mid %q", got.ID, mid.ID)
	}
	got, _, err = q.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.ID != low.ID {
		t.Fatalf("Claim picked %q, want low %q", got.ID, low.ID)
	}
}

// TestQueue_ConcurrentClaim is the load-bearing invariant for the
// fan-out story: two parallel Claim calls MUST NOT return the same
// item. Without atomic load → mutate → save under one lock, both
// goroutines could read the same pending row and double-claim.
func TestQueue_ConcurrentClaim(t *testing.T) {
	q := newQueue(t)
	const N = 20
	for i := 0; i < N; i++ {
		if _, err := q.Add("item", 0, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			it, ok, err := q.Claim()
			if err != nil || !ok {
				return
			}
			mu.Lock()
			seen[it.ID]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != N {
		t.Fatalf("claimed %d unique items, want %d", len(seen), N)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("item %q claimed %d times, want 1", id, n)
		}
	}
}

// TestQueue_SkipAndList confirms Skip flips status, List filters
// honor it, and a skipped item is no longer claimable.
func TestQueue_SkipAndList(t *testing.T) {
	q := newQueue(t)
	a, _ := q.Add("first", 0, "")
	b, _ := q.Add("second", 0, "")
	if _, err := q.Skip(a.ID, "operator dropped it"); err != nil {
		t.Fatalf("Skip: %v", err)
	}

	pending, err := q.List(StatusPending)
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatalf("pending list: got %+v, want [%q]", pending, b.ID)
	}
	skipped, err := q.List(StatusSkipped)
	if err != nil {
		t.Fatalf("List skipped: %v", err)
	}
	if len(skipped) != 1 || skipped[0].ID != a.ID {
		t.Fatalf("skipped list: got %+v, want [%q]", skipped, a.ID)
	}

	// Claim returns the surviving pending item.
	got, ok, err := q.Claim()
	if err != nil || !ok || got.ID != b.ID {
		t.Fatalf("Claim after skip: ok=%v err=%v id=%q want id=%q",
			ok, err, got.ID, b.ID)
	}
}

// TestQueue_StatusCounts confirms the histogram reflects every
// state transition.
func TestQueue_StatusCounts(t *testing.T) {
	q := newQueue(t)
	for i := 0; i < 4; i++ {
		if _, err := q.Add("item", 0, ""); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	a, _, _ := q.Claim()
	b, _, _ := q.Claim()
	if _, err := q.Complete(a.ID, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := q.Skip(b.ID, ""); err != nil {
		t.Fatalf("Skip: %v", err)
	}

	c, err := q.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if c.Total != 4 || c.Pending != 2 || c.InProgress != 0 || c.Done != 1 || c.Skipped != 1 {
		t.Fatalf("Counts mismatch: %+v", c)
	}
}

// TestQueue_NotFoundAndTerminal confirms the typed errors fire on
// the obvious bad-input paths.
func TestQueue_NotFoundAndTerminal(t *testing.T) {
	q := newQueue(t)
	if _, err := q.Complete("does-not-exist", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Complete unknown: err=%v want ErrNotFound", err)
	}
	a, _ := q.Add("once", 0, "")
	if _, _, err := q.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := q.Complete(a.ID, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := q.Complete(a.ID, ""); !errors.Is(err, ErrAlreadyTerminal) {
		t.Fatalf("Complete terminal: err=%v want ErrAlreadyTerminal", err)
	}
}

// TestQueue_MissingFileIsEmpty confirms a brand-new queue with no
// file on disk reads as zero items rather than erroring out.
// Operators expect a fresh repo to behave like an empty queue.
func TestQueue_MissingFileIsEmpty(t *testing.T) {
	q := newQueue(t)
	c, err := q.Status()
	if err != nil {
		t.Fatalf("Status on missing file: %v", err)
	}
	if c.Total != 0 {
		t.Fatalf("Status on missing file: total=%d, want 0", c.Total)
	}
	if _, ok, err := q.Claim(); err != nil || ok {
		t.Fatalf("Claim on missing file: ok=%v err=%v", ok, err)
	}
}

// TestQueue_AddRequiresPrompt rejects empty-string adds. The CLI
// also guards this; the package-level guard catches programmatic
// misuse from MCP handlers.
func TestQueue_AddRequiresPrompt(t *testing.T) {
	q := newQueue(t)
	if _, err := q.Add("   ", 0, ""); err == nil {
		t.Fatalf("Add accepted whitespace prompt")
	}
}

// TestQueue_ProposedNotClaimed pins the operator-gate invariant:
// items the Ideator emits with Propose() land as StatusProposed and
// MUST NOT be returned by Claim. Without this gate the Ideator could
// silently drive autonomous execution past the human's review.
func TestQueue_ProposedNotClaimed(t *testing.T) {
	q := newQueue(t)
	prop, err := q.Propose(ProposeInput{
		Prompt:    "wire BM25 baseline diff",
		Source:    "bench_regression",
		Evidence:  "/tmp/clawtool-toolsearch-bench.tsv",
		DedupeKey: "bench_regression:bm25-baseline",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if prop.Status != StatusProposed {
		t.Fatalf("Propose status: got %q, want %q", prop.Status, StatusProposed)
	}
	if _, ok, err := q.Claim(); err != nil || ok {
		t.Fatalf("Claim of proposed-only queue: ok=%v err=%v (want ok=false)", ok, err)
	}

	// Counts must surface the proposed slot independently of pending.
	c, err := q.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if c.Proposed != 1 || c.Pending != 0 || c.Total != 1 {
		t.Fatalf("Counts: %+v (want proposed=1 pending=0 total=1)", c)
	}
}

// TestQueue_AcceptFlipsToPending exercises the gate: an operator
// running `autopilot accept <id>` flips the item to pending, after
// which Claim picks it up normally. Accept on a non-proposed item
// returns the appropriate typed error.
func TestQueue_AcceptFlipsToPending(t *testing.T) {
	q := newQueue(t)
	prop, err := q.Propose(ProposeInput{Prompt: "raise wiki:* coverage"})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	got, err := q.Accept(prop.ID, "operator approved")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("Accept status: got %q, want %q", got.Status, StatusPending)
	}
	if got.AcceptedAt.IsZero() {
		t.Fatalf("Accept did not stamp AcceptedAt")
	}
	if got.Note != "operator approved" {
		t.Fatalf("Accept note: got %q, want %q", got.Note, "operator approved")
	}

	// Claim now finds it.
	claimed, ok, err := q.Claim()
	if err != nil || !ok {
		t.Fatalf("Claim post-accept: ok=%v err=%v", ok, err)
	}
	if claimed.ID != prop.ID {
		t.Fatalf("Claim id: got %q, want %q", claimed.ID, prop.ID)
	}

	// Accepting an in_progress item is rejected with ErrAlreadyAccepted.
	if _, err := q.Accept(prop.ID, ""); !errors.Is(err, ErrAlreadyAccepted) {
		t.Fatalf("Accept on in_progress: err=%v want ErrAlreadyAccepted", err)
	}

	if _, err := q.Complete(prop.ID, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Accepting a terminal item is rejected with ErrAlreadyTerminal.
	if _, err := q.Accept(prop.ID, ""); !errors.Is(err, ErrAlreadyTerminal) {
		t.Fatalf("Accept on terminal: err=%v want ErrAlreadyTerminal", err)
	}

	if _, err := q.Accept("does-not-exist", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Accept unknown: err=%v want ErrNotFound", err)
	}
}

// TestQueue_ProposeDedupes confirms that re-proposing on the same
// DedupeKey while the original is still active is a no-op and surfaces
// ErrDuplicateProposal so the Ideator can tally the dedup.
func TestQueue_ProposeDedupes(t *testing.T) {
	q := newQueue(t)
	first, err := q.Propose(ProposeInput{Prompt: "fix flaky test", DedupeKey: "todos:foo.go:42"})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := q.Propose(ProposeInput{Prompt: "fix flaky test", DedupeKey: "todos:foo.go:42"}); !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("Propose dup: err=%v want ErrDuplicateProposal", err)
	}
	// Counts: the second Propose must not have appended another row.
	c, err := q.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if c.Total != 1 || c.Proposed != 1 {
		t.Fatalf("Counts: %+v (want total=1 proposed=1)", c)
	}
	// Once the original is skipped, re-proposing succeeds (operator
	// said no, the source still sees the signal — give them another
	// chance after they fix the underlying issue).
	if _, err := q.Skip(first.ID, ""); err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if _, err := q.Propose(ProposeInput{Prompt: "fix flaky test", DedupeKey: "todos:foo.go:42"}); err != nil {
		t.Fatalf("Propose after skip: %v", err)
	}
}
