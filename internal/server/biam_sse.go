// Package server — `GET /v1/biam/subscribe` SSE handler (ADR-024
// Phase 4: A2A asynchronous push).
//
// Wire shape:
//
//	GET /v1/biam/subscribe?task_id=<id>
//	  Accept: text/event-stream            (advisory — we always
//	                                        emit SSE for this path)
//	  Last-Event-ID: <u64>                  (optional; resume after a
//	                                        prior disconnect)
//
// Response:
//
//	HTTP/1.1 200 OK
//	Content-Type: text/event-stream
//	Cache-Control: no-cache
//	Connection: keep-alive
//
//	id: 1
//	event: task
//	data: {"task_id":"…","status":"active",…}
//
//	id: 2
//	event: frame
//	data: {"task_id":"…","line":"hello","kind":"stdout","ts":"…"}
//
//	…
//
// Lifecycle:
//   - On connect, replay every event for task_id whose ID is greater
//     than the parsed Last-Event-ID header (default 0 = full ring).
//   - Then block on the per-task notify channel; on each wake re-read
//     the ring's tail and stream new events.
//   - Close on (a) terminal-status event (kind == "terminal"), (b)
//     client disconnect (ctx.Done from r.Context()), or (c) daemon
//     shutdown propagated through the same ctx chain.
//
// The buffer is fed by the broadcast hooks installed at daemon boot
// (see internal/agents/biam.WirePushHooks). The handler itself
// touches biam.Events only as a reader + subscriber.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/tools/core"
)

// handleBIAMSubscribe is the SSE entry point. Mounted at
// /v1/biam/subscribe by ServeHTTP.
func handleBIAMSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "GET only",
		})
		return
	}
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "task_id query parameter is required",
		})
		return
	}

	// 404 on unknown tasks so an A2A peer dialing a stale ID
	// gets a clear error instead of a forever-empty SSE stream.
	// Falls back to a buffer-only check when the BIAM store
	// isn't wired (test paths) — events may already be in the
	// buffer even when no SQLite-backed task row exists.
	if store := core.BiamStore(); store != nil {
		t, err := store.GetTask(r.Context(), taskID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": fmt.Sprintf("biam store: %v", err),
			})
			return
		}
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": fmt.Sprintf("task %q not found", taskID),
			})
			return
		}
	} else {
		// Buffer-only path: when no store is registered, we still
		// reject task_ids that have never appeared in the buffer.
		// Otherwise an SSE stream for a typo'd ID would block
		// forever on a notify channel that never wakes.
		evs, _ := biam.Events.Since(taskID, 0)
		if len(evs) == 0 && !biam.Events.HasTerminal(taskID) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": fmt.Sprintf("task %q not found", taskID),
			})
			return
		}
	}

	// Parse Last-Event-ID (RFC: clients re-send the last id they
	// processed on reconnect; we replay the suffix). Garbage
	// header → treat as 0 (full replay) per RFC §9.2 — clients
	// shouldn't be punished for a malformed header.
	var sinceID uint64
	if h := strings.TrimSpace(r.Header.Get("Last-Event-ID")); h != "" {
		if v, err := strconv.ParseUint(h, 10, 64); err == nil {
			sinceID = v
		}
	}

	// Subscribe BEFORE the initial Since() read so we don't
	// miss events appended in the gap between snapshot and
	// subscription. Duplicate IDs are filtered by the lastSent
	// cursor below.
	notify, unsub := biam.Events.Subscribe(taskID)
	defer unsub()

	// SSE headers per WHATWG. `Connection: keep-alive` is HTTP/1.1
	// implicit but we set it explicitly so reverse proxies that
	// still honour the legacy hint don't close the socket on idle.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable nginx response buffering
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	lastSent := sinceID

	// emit writes one SSE event in the canonical shape:
	//   id: <id>\nevent: <kind>\ndata: <json>\n\n
	// Returns false if the client disconnected (write error).
	emit := func(ev biam.TaskEvent) bool {
		// Encode the data on a single line; SSE forbids raw
		// newlines inside `data:` (they'd start a new field).
		// json.Marshal already produces a single-line encoding
		// when `ev.Data` is pre-encoded JSON, but we still
		// guard with a strip in case a publisher hands us a
		// pretty-printed payload.
		data := ev.Data
		if len(data) == 0 {
			data = []byte("{}")
		}
		// SSE field syntax: id, event, data each on their own
		// line, followed by a blank line. We avoid fmt.Fprintf
		// for the hot loop — direct Write is one syscall.
		var buf strings.Builder
		buf.Grow(len(data) + 64)
		buf.WriteString("id: ")
		buf.WriteString(strconv.FormatUint(ev.ID, 10))
		buf.WriteByte('\n')
		buf.WriteString("event: ")
		buf.WriteString(ev.Kind)
		buf.WriteByte('\n')
		buf.WriteString("data: ")
		// Strip stray \n from `data` so multi-line JSON
		// doesn't bleed into the next SSE field.
		for _, b := range data {
			if b == '\n' {
				buf.WriteByte(' ')
			} else {
				buf.WriteByte(b)
			}
		}
		buf.WriteString("\n\n")
		if _, err := w.Write([]byte(buf.String())); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		if ev.ID > lastSent {
			lastSent = ev.ID
		}
		return true
	}

	// Phase 1: replay everything since sinceID. If the buffer
	// dropped events past the requested resume point we emit a
	// `dropped` marker so the client can recover (e.g. by
	// hitting the snapshot endpoint).
	replay, dropped := biam.Events.Since(taskID, sinceID)
	if dropped {
		warn, _ := json.Marshal(map[string]any{
			"task_id":  taskID,
			"warning":  "events older than Last-Event-ID dropped past ring capacity",
			"since_id": sinceID,
		})
		if !emit(biam.TaskEvent{ID: 0, Kind: "dropped", Data: warn}) {
			return
		}
	}
	terminalSeen := false
	for _, ev := range replay {
		if !emit(ev) {
			return
		}
		if ev.Kind == "terminal" {
			terminalSeen = true
		}
	}
	// If the ring already shows terminal in the replay, close
	// the stream — nothing more is coming.
	if terminalSeen {
		return
	}
	// Defensive: a terminal event may have landed before we
	// subscribed (and got pruned, or the buffer was reset).
	// HasTerminal catches that race.
	if biam.Events.HasTerminal(taskID) {
		return
	}

	// Phase 2: stream new events as they land.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-notify:
			if !ok {
				// channel closed — buffer reset or shutdown
				return
			}
			fresh, _ := biam.Events.Since(taskID, lastSent)
			for _, ev := range fresh {
				if !emit(ev) {
					return
				}
				if ev.Kind == "terminal" {
					return
				}
			}
		}
	}
}
