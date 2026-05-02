// Package autopilot — self-direction backlog primitive that lets a
// Claude Code agent (the operator's clawtool driver) pull "what to
// work on next" without requiring the human to re-prompt every
// turn. The user calls this "devam edebilme yeteneği" — continuation
// ability / non-stalling.
//
// Pattern: classic single-producer-multi-consumer task queue
// persisted as TOML. The operator (or the agent itself) appends
// items via AutopilotAdd; the agent dequeues via AutopilotNext, does
// the work, marks done via AutopilotDone, then loops. When Claim()
// returns ok=false the queue is empty and the agent ends the loop.
//
// Why this is NOT BIAM/SendMessage: BIAM is agent-to-agent dispatch
// (codex / gemini / opencode). Autopilot is self-work — the SAME
// agent picking up its own backlog. Pre-this primitive an agent that
// finished a task either (a) stalled waiting for operator input, or
// (b) re-invented a TODO list in its scratchpad on every session.
//
// Storage: a TOML file at $XDG_CONFIG_HOME/clawtool/autopilot/queue.toml
// (defaults to ~/.config/clawtool/autopilot/queue.toml). Per-host,
// not per-repo, so a backlog the operator wrote at lunch is still
// there when they reopen Claude Code in the evening. Atomic writes
// via internal/atomicfile so a crashed mid-rewrite never corrupts
// the queue.
//
// Concurrency: Claim() takes an exclusive lock on the file (load →
// mutate → save under one rwmu acquisition). Two parallel "next"
// calls from a fanned-out agent will not return the same item —
// the tests pin this invariant.
package autopilot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/pelletier/go-toml/v2"
)

// Status enumerates the lifecycle of one backlog Item. The set is
// closed: anything outside this list is a programmer error.
type Status string

const (
	StatusPending    Status = "pending"     // freshly added, not yet claimed
	StatusInProgress Status = "in_progress" // returned by Claim, not yet completed
	StatusDone       Status = "done"        // Complete() called
	StatusSkipped    Status = "skipped"     // Skip() called — operator told the agent to drop it
)

// Item is one unit of self-work. Field names map directly to TOML
// keys; preserve them when adding fields so existing queues survive
// rolling upgrades.
type Item struct {
	ID        string    `toml:"id" json:"id"`
	Prompt    string    `toml:"prompt" json:"prompt"`
	Priority  int       `toml:"priority" json:"priority"`
	Status    Status    `toml:"status" json:"status"`
	CreatedAt time.Time `toml:"created_at" json:"created_at"`
	ClaimedAt time.Time `toml:"claimed_at,omitempty" json:"claimed_at,omitempty"`
	DoneAt    time.Time `toml:"done_at,omitempty" json:"done_at,omitempty"`
	Note      string    `toml:"note,omitempty" json:"note,omitempty"`
}

// queueFile is the on-disk shape — `[[item]]` array hosts the
// items; future top-level metadata (version, comment) goes here.
type queueFile struct {
	Item []Item `toml:"item"`
}

// Queue is a TOML-backed self-direction backlog. Construct with
// Open or OpenAt. Operations are concurrency-safe via an internal
// mutex; multiple Queue instances pointing at the same path serialize
// through the file system (atomicfile rename + every mutation does a
// fresh load).
type Queue struct {
	path string
	mu   sync.Mutex
}

// DefaultPath returns the canonical autopilot queue file:
// $XDG_CONFIG_HOME/clawtool/autopilot/queue.toml.
func DefaultPath() string {
	return filepath.Join(xdg.ConfigDir(), "autopilot", "queue.toml")
}

// Open returns a Queue rooted at the default path.
func Open() *Queue { return OpenAt(DefaultPath()) }

// OpenAt returns a Queue rooted at the supplied path. Tests use
// this to redirect into a tmpdir.
func OpenAt(path string) *Queue { return &Queue{path: path} }

// Path returns the queue's file path.
func (q *Queue) Path() string { return q.path }

// load reads the queue file. Missing file is fine — a fresh queue.
// Caller MUST hold q.mu.
func (q *Queue) load() ([]Item, error) {
	body, err := os.ReadFile(q.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("autopilot: read %s: %w", q.path, err)
	}
	var f queueFile
	if err := toml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("autopilot: parse %s: %w", q.path, err)
	}
	return f.Item, nil
}

// save writes the queue to disk via atomicfile. Caller MUST hold q.mu.
func (q *Queue) save(items []Item) error {
	out, err := toml.Marshal(queueFile{Item: items})
	if err != nil {
		return fmt.Errorf("autopilot: marshal: %w", err)
	}
	header := []byte(
		"# clawtool autopilot — self-direction backlog. Items the\n" +
			"# agent (or operator) appended via 'clawtool autopilot add'\n" +
			"# / mcp__clawtool__AutopilotAdd. The agent dequeues via\n" +
			"# 'clawtool autopilot next' / mcp__clawtool__AutopilotNext.\n" +
			"# When 'next' returns empty the agent ends the loop.\n\n",
	)
	body := append(header, out...)
	return atomicfile.WriteFileMkdir(q.path, body, 0o644, 0o755)
}

// Add appends a new pending item. id is auto-generated when empty.
// Returns the persisted Item (with id + created_at filled).
func (q *Queue) Add(prompt string, priority int, note string) (Item, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Item{}, errors.New("autopilot: prompt is empty")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.load()
	if err != nil {
		return Item{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	id := generateID(items, now)
	it := Item{
		ID:        id,
		Prompt:    prompt,
		Priority:  priority,
		Status:    StatusPending,
		CreatedAt: now,
		Note:      strings.TrimSpace(note),
	}
	items = append(items, it)
	if err := q.save(items); err != nil {
		return Item{}, err
	}
	return it, nil
}

// Claim returns the highest-priority pending item, marks it
// in_progress, and persists. ok=false (zero Item, nil err) when
// the queue has no pending items — the agent's signal to stop the
// loop.
//
// Selection order: highest Priority first, then earliest CreatedAt.
// Ties broken by lexicographic ID for determinism in tests.
func (q *Queue) Claim() (Item, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.load()
	if err != nil {
		return Item{}, false, err
	}
	idx := pickPending(items)
	if idx < 0 {
		return Item{}, false, nil
	}
	items[idx].Status = StatusInProgress
	items[idx].ClaimedAt = time.Now().UTC().Truncate(time.Second)
	if err := q.save(items); err != nil {
		return Item{}, false, err
	}
	return items[idx], true, nil
}

// Complete marks the named item done. Idempotent: completing an
// already-done item returns a typed error so the caller can decide
// whether to ignore it. Unknown id returns ErrNotFound.
func (q *Queue) Complete(id, note string) (Item, error) {
	return q.transition(id, StatusDone, note)
}

// Skip marks the named item skipped. Same shape as Complete.
func (q *Queue) Skip(id, note string) (Item, error) {
	return q.transition(id, StatusSkipped, note)
}

// transition is the shared mutator for Complete / Skip.
func (q *Queue) transition(id string, target Status, note string) (Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.load()
	if err != nil {
		return Item{}, err
	}
	idx := -1
	for i := range items {
		if items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Item{}, ErrNotFound
	}
	if items[idx].Status == StatusDone || items[idx].Status == StatusSkipped {
		return items[idx], ErrAlreadyTerminal
	}
	items[idx].Status = target
	items[idx].DoneAt = time.Now().UTC().Truncate(time.Second)
	if note = strings.TrimSpace(note); note != "" {
		items[idx].Note = note
	}
	if err := q.save(items); err != nil {
		return Item{}, err
	}
	return items[idx], nil
}

// List returns every item filtered by status. Pass empty string to
// return all. Order: pending first (priority/created), then
// in_progress, then done/skipped.
func (q *Queue) List(filter Status) ([]Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.load()
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if filter != "" && it.Status != filter {
			continue
		}
		out = append(out, it)
	}
	sortItems(out)
	return out, nil
}

// Counts represents the queue's status histogram. Returned by Status.
type Counts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
	Skipped    int `json:"skipped"`
	Total      int `json:"total"`
}

// Status returns the histogram of items by status.
func (q *Queue) Status() (Counts, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.load()
	if err != nil {
		return Counts{}, err
	}
	var c Counts
	for _, it := range items {
		c.Total++
		switch it.Status {
		case StatusPending:
			c.Pending++
		case StatusInProgress:
			c.InProgress++
		case StatusDone:
			c.Done++
		case StatusSkipped:
			c.Skipped++
		}
	}
	return c, nil
}

// ErrNotFound is returned by Complete / Skip when no item matches
// the given id.
var ErrNotFound = errors.New("autopilot: item not found")

// ErrAlreadyTerminal is returned by Complete / Skip when the item
// is already done or skipped. Callers can ignore for idempotency.
var ErrAlreadyTerminal = errors.New("autopilot: item already in terminal state")

// pickPending returns the index of the next item to claim, or -1
// when no pending item exists. Highest priority wins, ties broken
// by earliest CreatedAt, then lexicographic ID.
func pickPending(items []Item) int {
	best := -1
	for i, it := range items {
		if it.Status != StatusPending {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		b := items[best]
		switch {
		case it.Priority > b.Priority:
			best = i
		case it.Priority < b.Priority:
			// keep best
		case it.CreatedAt.Before(b.CreatedAt):
			best = i
		case it.CreatedAt.After(b.CreatedAt):
			// keep best
		case it.ID < b.ID:
			best = i
		}
	}
	return best
}

// sortItems orders for List. Active states first (pending, then
// in_progress) by priority desc + created asc. Terminal states
// (done, skipped) last by done_at desc.
func sortItems(items []Item) {
	rank := func(s Status) int {
		switch s {
		case StatusPending:
			return 0
		case StatusInProgress:
			return 1
		case StatusDone:
			return 2
		case StatusSkipped:
			return 3
		}
		return 4
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		ra, rb := rank(a.Status), rank(b.Status)
		if ra != rb {
			return ra < rb
		}
		if ra <= 1 { // active
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.ID < b.ID
		}
		// terminal: most recent first
		if !a.DoneAt.Equal(b.DoneAt) {
			return a.DoneAt.After(b.DoneAt)
		}
		return a.ID < b.ID
	})
}

// generateID builds a stable, sortable id of the form
// "ap-YYYYMMDD-HHMMSS-N" where N disambiguates same-second adds.
// Falls back on a counter probe of existing IDs to keep collisions
// deterministic in tests.
func generateID(existing []Item, now time.Time) string {
	prefix := "ap-" + now.Format("20060102-150405")
	max := 0
	for _, it := range existing {
		if !strings.HasPrefix(it.ID, prefix) {
			continue
		}
		// Suffix is "-N"; parse trailing int.
		tail := strings.TrimPrefix(it.ID, prefix)
		tail = strings.TrimPrefix(tail, "-")
		var n int
		if _, err := fmt.Sscanf(tail, "%d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("%s-%d", prefix, max+1)
}
