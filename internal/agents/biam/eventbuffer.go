// Package biam — TaskEventBuffer is the per-task ring buffer that
// backs the SSE A2A async-push handler at GET /v1/biam/subscribe.
//
// Why a separate buffer alongside WatchHub:
//   - WatchHub is a process-wide live broadcaster with no replay.
//     A subscriber that connects mid-stream sees only events from
//     that point forward.
//   - SSE clients reconnect with a Last-Event-ID header and expect
//     the server to replay everything that happened between the
//     drop and the resume. WatchHub's design (drop-on-slow-consumer,
//     no history) can't do that.
//   - A2A peers polling /v1/biam/subscribe?task_id=… need both
//     replay (for crash-recovery / resume-after-disconnect) and
//     edge-triggered push (so they don't poll a dead socket).
//
// Design (per ADR-024 §Resolved 2026-05-02):
//
//   - One TaskEventBuffer per process (singleton `Events`).
//   - Per-task circular buffer, capacity 256 events. Oldest events
//     drop when the cap is hit.
//   - Each event carries a u64 monotonic ID (per-task, starts at 1).
//     The SSE handler emits these as `id:` lines so a reconnecting
//     client's Last-Event-ID header lets us replay only the missed
//     suffix.
//   - Append fires a non-blocking notify on every channel registered
//     for that task. SSE handlers select{} on notify + ctx.Done.
//   - Bounded-buffer drops are visible: when the requested
//     Last-Event-ID is older than the oldest retained event, the
//     replay returns from the earliest available + the buffer's
//     dropped-prefix marker so the client can detect the gap.
//
// Lifetime: the daemon constructs the singleton at boot. Tests use
// NewTaskEventBuffer for isolation.
package biam

import (
	"sync"
	"time"
)

// TaskEvent is one entry in a per-task ring buffer. Kind is the SSE
// `event:` line; Data is the SSE `data:` payload (JSON-encoded by the
// publisher). ID is per-task monotonic.
type TaskEvent struct {
	ID   uint64    `json:"id"`
	Kind string    `json:"kind"` // "task" | "frame" | "system" | "terminal"
	Data []byte    `json:"data"` // pre-encoded JSON body
	TS   time.Time `json:"ts"`
}

// taskRing is the per-task circular buffer + notify fan-out.
type taskRing struct {
	mu sync.Mutex
	// buf is the circular storage. cap is fixed at construction.
	buf []TaskEvent
	// head points to the next slot to write; wraps mod cap.
	head int
	// size is the count of valid entries (≤ cap).
	size int
	// nextID is the ID to assign to the next appended event.
	// Per-task monotonic, never reset across appends (so a wrap
	// doesn't reuse IDs — the buffer drops the oldest events but
	// retains its place in the ID space).
	nextID uint64
	// terminal flips true when a terminal-status event has been
	// appended. Subscribers can poll this on connect to skip the
	// stream entirely if the task already finished.
	terminal bool
	// subscribers receive a non-blocking notify on every Append.
	subscribers map[*ringSub]struct{}
}

type ringSub struct {
	ch chan struct{}
}

// TaskEventBuffer is the per-process registry of per-task rings.
type TaskEventBuffer struct {
	mu      sync.RWMutex
	rings   map[string]*taskRing
	ringCap int
}

// DefaultRingCap is the per-task event retention. 256 events covers
// the bursty-then-quiet shape of a typical dispatch (boot + dozens
// of frames + terminal) without making short reconnects walk a
// dense history.
const DefaultRingCap = 256

// Events is the process-wide singleton. The daemon installs publish
// hooks at boot; tests use NewTaskEventBuffer for isolation.
var Events = NewTaskEventBuffer(DefaultRingCap)

// NewTaskEventBuffer constructs a buffer with the given per-task
// capacity. Cap ≤ 0 falls back to DefaultRingCap.
func NewTaskEventBuffer(cap int) *TaskEventBuffer {
	if cap <= 0 {
		cap = DefaultRingCap
	}
	return &TaskEventBuffer{
		rings:   map[string]*taskRing{},
		ringCap: cap,
	}
}

// ResetForTest wipes every per-task ring. Test-only.
func (b *TaskEventBuffer) ResetForTest() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.rings {
		r.mu.Lock()
		for s := range r.subscribers {
			close(s.ch)
		}
		r.subscribers = map[*ringSub]struct{}{}
		r.mu.Unlock()
	}
	b.rings = map[string]*taskRing{}
}

// ringFor returns (and lazily creates) the ring for task_id.
func (b *TaskEventBuffer) ringFor(taskID string) *taskRing {
	b.mu.RLock()
	r, ok := b.rings[taskID]
	b.mu.RUnlock()
	if ok {
		return r
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Re-check under write lock — another goroutine may have
	// created the ring while we were upgrading.
	if r, ok := b.rings[taskID]; ok {
		return r
	}
	r = &taskRing{
		buf:         make([]TaskEvent, b.ringCap),
		nextID:      1,
		subscribers: map[*ringSub]struct{}{},
	}
	b.rings[taskID] = r
	return r
}

// Append records an event for taskID. Assigns the next monotonic ID,
// drops the oldest entry on overflow, fires a non-blocking notify on
// every subscriber. `kind == "terminal"` flips the ring's terminal
// flag so HasTerminal() returns true for late-joiners.
func (b *TaskEventBuffer) Append(taskID, kind string, data []byte) TaskEvent {
	r := b.ringFor(taskID)
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	ev := TaskEvent{
		ID:   id,
		Kind: kind,
		Data: data,
		TS:   time.Now().UTC(),
	}
	cap := len(r.buf)
	r.buf[r.head] = ev
	r.head = (r.head + 1) % cap
	if r.size < cap {
		r.size++
	}
	if kind == "terminal" {
		r.terminal = true
	}
	// Non-blocking notify so a slow subscriber that hasn't drained
	// the previous tick doesn't block the publisher. The
	// signal is edge-only — subscribers always re-read the
	// ring on wake to pick up everything they missed.
	subs := make([]*ringSub, 0, len(r.subscribers))
	for s := range r.subscribers {
		subs = append(subs, s)
	}
	r.mu.Unlock()
	for _, s := range subs {
		select {
		case s.ch <- struct{}{}:
		default:
		}
	}
	return ev
}

// Since returns every event for taskID with ID > sinceID, in
// chronological order. When sinceID is 0 the full retained ring is
// returned. dropped reports whether sinceID points at a position
// older than the oldest retained event (the client missed events
// past the cap; reconnect-with-snapshot is the only recovery).
func (b *TaskEventBuffer) Since(taskID string, sinceID uint64) (events []TaskEvent, dropped bool) {
	b.mu.RLock()
	r, ok := b.rings[taskID]
	b.mu.RUnlock()
	if !ok {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil, false
	}
	cap := len(r.buf)
	// The oldest retained event sits at (head - size) mod cap.
	oldestIdx := (r.head - r.size + cap) % cap
	oldestID := r.buf[oldestIdx].ID
	// Detect a gap: the client wants events after sinceID, but
	// our oldest retained event has ID > sinceID + 1 — the ring
	// wrapped past the requested resume point.
	if sinceID > 0 && oldestID > sinceID+1 {
		dropped = true
	}
	out := make([]TaskEvent, 0, r.size)
	for i := 0; i < r.size; i++ {
		idx := (oldestIdx + i) % cap
		ev := r.buf[idx]
		if ev.ID > sinceID {
			out = append(out, ev)
		}
	}
	return out, dropped
}

// HasTerminal reports whether a terminal event has been appended for
// taskID. Lets the SSE handler short-circuit a connect to a task
// that already finished (replay history + close).
func (b *TaskEventBuffer) HasTerminal(taskID string) bool {
	b.mu.RLock()
	r, ok := b.rings[taskID]
	b.mu.RUnlock()
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminal
}

// Subscribe registers an edge-trigger notify channel for taskID.
// Returns the receive channel + an unsubscribe func. The channel
// receives one signal per Append; receivers MUST re-read the ring
// on wake. Buffer cap = 1 — coalesced; the publisher drops
// duplicate signals so a slow subscriber sees one wake per batch
// rather than N pending wakes.
func (b *TaskEventBuffer) Subscribe(taskID string) (<-chan struct{}, func()) {
	r := b.ringFor(taskID)
	sub := &ringSub{ch: make(chan struct{}, 1)}
	r.mu.Lock()
	r.subscribers[sub] = struct{}{}
	r.mu.Unlock()
	return sub.ch, func() {
		r.mu.Lock()
		if _, ok := r.subscribers[sub]; ok {
			delete(r.subscribers, sub)
			close(sub.ch)
		}
		r.mu.Unlock()
	}
}

// SubscriberCount is test-only.
func (b *TaskEventBuffer) SubscriberCount(taskID string) int {
	b.mu.RLock()
	r, ok := b.rings[taskID]
	b.mu.RUnlock()
	if !ok {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subscribers)
}
