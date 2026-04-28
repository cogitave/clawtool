// Package biam — WatchHub broadcasts every task transition to
// in-process subscribers, fanning out the same Task snapshot to N
// goroutines. The Unix-socket server (watchsocket.go) is the
// out-of-process consumer that lets `clawtool task watch` ditch
// SQLite polling.
//
// Why a second hub alongside Notifier:
//   - Notifier is a one-shot terminal-only push for TaskNotify /
//     `clawtool send --wait`. It clears its subscriber list per task
//     after a single Publish.
//   - WatchHub fans EVERY transition (active, message_count++,
//     terminal) to long-lived watchers so a `task watch --all`
//     sees the full progress timeline.
//
// Subscribers receive on a buffered channel (cap 32). A slow
// subscriber drops events past the buffer rather than blocking the
// publisher — losing a transition is preferable to stalling every
// other watcher.
package biam

import "sync"

// WatchHub is the multi-subscriber broadcaster. Lifetime = process.
type WatchHub struct {
	mu   sync.Mutex
	subs map[*watchSub]struct{}
}

type watchSub struct {
	ch chan Task
}

// Watch is the process-wide singleton. Tests use ResetWatchForTest.
var Watch = &WatchHub{subs: map[*watchSub]struct{}{}}

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
}
