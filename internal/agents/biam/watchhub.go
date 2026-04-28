// Package biam — WatchHub broadcasts task transitions AND live
// stream frames to in-process subscribers. The Unix-socket server
// (watchsocket.go) is the out-of-process consumer that lets
// `clawtool task watch`, `clawtool dashboard`, and
// `clawtool orchestrator` ditch SQLite polling.
//
// Why a second hub alongside Notifier:
//   - Notifier is a one-shot terminal-only push for TaskNotify /
//     `clawtool send --wait`. It clears its subscriber list per task
//     after a single Publish.
//   - WatchHub fans EVERY transition (active, message_count++,
//     terminal) AND every line the upstream agent emits as a
//     StreamFrame to long-lived watchers. The orchestrator pane
//     reconstructs a live stdout view from this.
//
// Subscribers receive on a buffered channel (cap 64 for tasks, cap
// 256 for frames since stream lines are higher cadence). A slow
// subscriber drops events past the buffer rather than blocking the
// publisher — losing a transition is preferable to stalling every
// other watcher.
package biam

import (
	"sync"
	"time"
)

// StreamFrame is one line emitted by an upstream agent. The
// orchestrator pane appends frames to a per-task ringbuffer and
// renders them as live stdout. Frames carry the `kind` so the
// renderer can colour `error` or `meta` lines differently from
// regular output.
type StreamFrame struct {
	TaskID string    `json:"task_id"`
	Agent  string    `json:"agent,omitempty"` // family-only, never instance label
	Line   string    `json:"line"`
	Kind   string    `json:"kind,omitempty"` // "stdout" (default) | "error" | "meta"
	TS     time.Time `json:"ts"`
}

// WatchHub is the multi-subscriber broadcaster. Lifetime = process.
type WatchHub struct {
	mu     sync.Mutex
	subs   map[*watchSub]struct{}
	frames map[*frameSub]struct{}
}

type watchSub struct {
	ch chan Task
}

type frameSub struct {
	ch chan StreamFrame
}

// Watch is the process-wide singleton. Tests use ResetWatchForTest.
var Watch = &WatchHub{
	subs:   map[*watchSub]struct{}{},
	frames: map[*frameSub]struct{}{},
}

// Subscribe registers a buffered channel for every Broadcast. Returns
// the receive channel + an unsubscribe func. Callers MUST call
// unsubscribe to free the slot — usually via defer.
func (h *WatchHub) Subscribe() (<-chan Task, func()) {
	sub := &watchSub{ch: make(chan Task, 32)}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[sub]; ok {
			delete(h.subs, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}
}

// Broadcast fans the task snapshot to every subscriber. Non-blocking:
// a subscriber whose buffer is full drops this event silently. The
// store hook calls this after every state mutation.
func (h *WatchHub) Broadcast(t Task) {
	h.mu.Lock()
	subs := make([]*watchSub, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		select {
		case s.ch <- t:
		default:
			// drop — slow consumer
		}
	}
}

// SubsCount is test-only — exposed so tests assert that unsubscribe
// actually frees the slot.
func (h *WatchHub) SubsCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// ResetWatchForTest wipes every subscriber. Test-only.
func (h *WatchHub) ResetWatchForTest() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		close(s.ch)
	}
	h.subs = map[*watchSub]struct{}{}
	for s := range h.frames {
		close(s.ch)
	}
	h.frames = map[*frameSub]struct{}{}
}

// SubscribeFrames registers a stream-frame subscriber. Higher buffer
// (256) than Subscribe — agents emit dozens of lines/second. Caller
// MUST unsub.
func (h *WatchHub) SubscribeFrames() (<-chan StreamFrame, func()) {
	sub := &frameSub{ch: make(chan StreamFrame, 256)}
	h.mu.Lock()
	if h.frames == nil {
		h.frames = map[*frameSub]struct{}{}
	}
	h.frames[sub] = struct{}{}
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		if _, ok := h.frames[sub]; ok {
			delete(h.frames, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}
}

// BroadcastFrame fans one StreamFrame to every frame subscriber.
// Non-blocking: a subscriber whose 256-cap buffer is full drops the
// event silently. The runner calls this after every line scanned
// from the upstream rc.
func (h *WatchHub) BroadcastFrame(f StreamFrame) {
	h.mu.Lock()
	if h.frames == nil {
		h.mu.Unlock()
		return
	}
	subs := make([]*frameSub, 0, len(h.frames))
	for s := range h.frames {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		select {
		case s.ch <- f:
		default:
			// drop — slow consumer
		}
	}
}

// FrameSubsCount is test-only.
func (h *WatchHub) FrameSubsCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.frames)
}
