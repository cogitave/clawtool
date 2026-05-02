// Package biam — bridge between WatchHub broadcasts and the per-task
// TaskEventBuffer that backs /v1/biam/subscribe SSE replay.
//
// Why a bridge: WatchHub is the live broadcaster every existing
// in-process consumer (orchestrator, dashboard, watchsocket) reads
// from; the SSE handler needs a *replayable* per-task history. The
// bridge subscribes to WatchHub and rewrites every event into the
// per-task ring so SSE clients get both the snapshot+replay and the
// edge-trigger fan-out.
//
// Wired once at daemon boot from internal/server.buildMCPServer.
package biam

import (
	"context"
	"encoding/json"
)

// WirePushHooks bridges hub's broadcasts into events. Returns a stop
// func that unsubscribes both bridges. Idempotent at the call-site
// (the daemon only calls it once); calling twice creates two bridges
// and produces duplicate events.
//
// Bridges:
//   - hub.Subscribe()       → events.Append(taskID, "task" | "terminal", ...)
//   - hub.SubscribeFrames() → events.Append(taskID, "frame", ...)
//
// System notifications are NOT bridged — they're not task-scoped
// (no task_id), so they don't fit the SSE ?task_id=… contract.
// SSE clients that want daemon-level events poll a future
// /v1/biam/system endpoint instead.
func WirePushHooks(ctx context.Context, hub *WatchHub, events *TaskEventBuffer) (stop func()) {
	taskCh, unsubTask := hub.Subscribe()
	frameCh, unsubFrame := hub.SubscribeFrames()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case t, ok := <-taskCh:
				if !ok {
					return
				}
				kind := "task"
				if t.Status.IsTerminal() {
					// Terminal events are tagged distinctly so
					// the SSE handler can close the stream as
					// soon as the kind hits the wire — without
					// a second SQLite round-trip on every event.
					kind = "terminal"
				}
				if data, err := json.Marshal(t); err == nil {
					events.Append(t.TaskID, kind, data)
				}
			case f, ok := <-frameCh:
				if !ok {
					return
				}
				if data, err := json.Marshal(f); err == nil {
					events.Append(f.TaskID, "frame", data)
				}
			}
		}
	}()

	return func() {
		unsubTask()
		unsubFrame()
		<-done
	}
}
