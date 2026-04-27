// Package biam — process-internal completion notifier (ADR-024
// preview / TaskNotify support). The SQLite-backed task store is
// the durable record; this is the *edge-triggered* fast path so a
// TaskNotify caller doesn't have to poll. Lifetime = clawtool
// serve process. Subscriptions evaporate on restart — completed
// tasks remain queryable via TaskGet.
package biam

import (
	"sync"
)

// notifier broadcasts terminal-status transitions to in-process
// subscribers. Each subscriber gets a one-shot channel that fires
// when its task_id reaches a terminal state.
type notifier struct {
	mu   sync.Mutex
	subs map[string][]chan Task
}

// Notifier is the process-wide singleton. Tests use ResetForTest.
var Notifier = &notifier{subs: map[string][]chan Task{}}

// Sub is a handle to one subscription. Cancel removes the channel
// from the subscriber list so a goroutine that bails out doesn't
// leak its slot until the next Publish.
type Sub struct {
	Ch     <-chan Task
	cancel func()
}

// Cancel detaches this subscription. Safe to call after Publish
// has fired (no-op).
func (s *Sub) Cancel() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

// Subscribe registers a one-shot channel for terminal-status events
// on task_id. The channel is buffered (cap 1) so Publish never
// blocks. Caller MUST either drain the channel or call Cancel —
// otherwise the slot lingers in the registry until Publish or
// process exit.
func (n *notifier) Subscribe(taskID string) *Sub {
	ch := make(chan Task, 1)
	n.mu.Lock()
	n.subs[taskID] = append(n.subs[taskID], ch)
	n.mu.Unlock()

	return &Sub{
		Ch: ch,
		cancel: func() {
			n.mu.Lock()
			defer n.mu.Unlock()
			list := n.subs[taskID]
			for i, c := range list {
				if c == ch {
					n.subs[taskID] = append(list[:i], list[i+1:]...)
					break
				}
			}
			if len(n.subs[taskID]) == 0 {
				delete(n.subs, taskID)
			}
		},
	}
}

// Publish snapshots `task` to every subscriber waiting on its
// task_id and clears the subscriber list. Non-blocking — channels
// are cap-1 and we only fire once per task per subscription.
func (n *notifier) Publish(task Task) {
	n.mu.Lock()
	subs := n.subs[task.TaskID]
	delete(n.subs, task.TaskID)
	n.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- task:
		default:
			// Defensive: cap-1 buffer + single publish per
			// subscription means this should never trigger.
		}
	}
}

// SubsCount returns the number of subscribers waiting on task_id.
// Test-only — exposed so the test suite can assert that Cancel
// actually removes the slot.
func (n *notifier) SubsCount(taskID string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.subs[taskID])
}

// ResetForTest wipes every subscriber. Test-only.
func (n *notifier) ResetForTest() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.subs = map[string][]chan Task{}
}
