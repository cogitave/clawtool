// Package biam — Unix-socket task-watch server. The daemon runs
// ServeWatchSocket alongside its HTTP gateway; `clawtool task watch`
// dials the same socket and reads NDJSON Task events as they happen,
// eliminating the 250ms SQLite poll.
//
// Wire format: one Task JSON per line, newline-terminated. The
// server emits a snapshot of every existing task on connect (so
// late joiners catch up without polling), then streams the live
// hub feed until the client disconnects or the daemon exits.
//
// Permissions: socket file is mode 0600 — same security posture as
// the listener-token. The XDG_STATE_HOME path keeps it off the
// user's $HOME root.
package biam

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultWatchSocketPath honours XDG_STATE_HOME, falls back to
// ~/.local/state. Keeps the runtime socket out of $XDG_CONFIG_HOME
// (config = static) and $XDG_DATA_HOME (data = durable).
func DefaultWatchSocketPath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "task-watch.sock")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "clawtool", "task-watch.sock")
	}
	return "task-watch.sock"
}

// ServeWatchSocket binds the Unix socket at `path`, accepting clients
// until ctx cancels. Each accepted connection gets:
//
//  1. A backlog snapshot — every current task as a JSONL line, so a
//     late watcher catches up without re-polling SQLite.
//  2. A live tail subscribed to `hub` — every Broadcast becomes
//     another JSONL line.
//
// Returns when ctx is done OR the listener accept errors fatally.
// A nil hub falls back to the package singleton.
func ServeWatchSocket(ctx context.Context, store *Store, hub *WatchHub, path string) error {
	if hub == nil {
		hub = Watch
	}
	if path == "" {
		path = DefaultWatchSocketPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("biam watchsocket: mkdir parent: %w", err)
	}
	// Stale socket from a prior crash — best-effort remove. Net.Listen
	// will fail with "address already in use" otherwise.
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("biam watchsocket: listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return fmt.Errorf("biam watchsocket: chmod %s: %w", path, err)
	}

	// Wire the store hook to broadcast every mutation. We re-read
	// the row so the broadcast carries the merged snapshot
	// (status + message_count + last_message). When GetTask fails
	// transiently we drop the event rather than emitting a
	// half-populated row — the next mutation will broadcast cleanly.
	store.SetTaskHook(func(taskID string) {
		t, err := store.GetTask(context.Background(), taskID)
		if err != nil || t == nil {
			return
		}
		hub.Broadcast(*t)
	})

	// Close the listener when ctx cancels so Accept unblocks.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				_ = os.Remove(path)
				return nil
			}
			// Transient accept error — log via stderr and retry
			// after a short pause so a flaky FS doesn't kill the
			// whole server.
			fmt.Fprintf(os.Stderr, "biam watchsocket: accept: %v\n", err)
			select {
			case <-ctx.Done():
				wg.Wait()
				_ = os.Remove(path)
				return nil
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer c.Close()
			handleWatchClient(ctx, c, store, hub)
		}(conn)
	}
}

// WatchEnvelope is the JSONL wire-format wrapping every event the
// watch socket emits. `Kind` distinguishes "task" snapshots from
// "frame" stream lines so a single connection can multiplex both.
// CLI / TUI consumers branch on Kind. Older clients that pre-date
// the wrapping detect the new shape (top-level `kind` key) and
// upgrade their parser; nothing breaks if a Task lands in `Task`
// and `Frame` stays nil.
type WatchEnvelope struct {
	Kind  string       `json:"kind"`            // "task" | "frame"
	Task  *Task        `json:"task,omitempty"`  // populated when Kind=="task"
	Frame *StreamFrame `json:"frame,omitempty"` // populated when Kind=="frame"
}

// handleWatchClient streams snapshot + live events to one connected
// reader. Returns when the client disconnects, the connection errors
// out, or ctx cancels. Wraps every payload in a WatchEnvelope so
// task transitions and stream frames share one socket.
func handleWatchClient(ctx context.Context, c net.Conn, store *Store, hub *WatchHub) {
	w := bufio.NewWriter(c)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	// Subscribe FIRST so events that fire during the snapshot
	// don't slip through the gap. Buffered cap-32 channel +
	// drop-on-full means slow clients lose events but never block
	// the publisher.
	taskCh, unsubTask := hub.Subscribe()
	defer unsubTask()
	frameCh, unsubFrame := hub.SubscribeFrames()
	defer unsubFrame()

	emit := func(env WatchEnvelope) bool {
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := enc.Encode(env); err != nil {
			return false
		}
		if err := w.Flush(); err != nil {
			return false
		}
		_ = c.SetWriteDeadline(time.Time{})
		return true
	}

	// Snapshot pass — give the watcher every task we know about
	// before tailing the live feed.
	if tasks, err := store.ListTasks(ctx, 1000); err == nil {
		for i := range tasks {
			t := tasks[i]
			if !emit(WatchEnvelope{Kind: "task", Task: &t}) {
				return
			}
		}
	}

	// Detect client disconnect via a non-blocking read goroutine.
	// We don't expect any client→server traffic; reading just
	// signals EOF when the watcher process exits.
	disc := make(chan struct{}, 1)
	go func() {
		_, _ = c.Read(make([]byte, 1))
		disc <- struct{}{}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-disc:
			return
		case t, ok := <-taskCh:
			if !ok {
				return
			}
			if !emit(WatchEnvelope{Kind: "task", Task: &t}) {
				return
			}
		case f, ok := <-frameCh:
			if !ok {
				return
			}
			if !emit(WatchEnvelope{Kind: "frame", Frame: &f}) {
				return
			}
		}
	}
}

// DialWatchSocket returns an open net.Conn to the daemon's task-
// watch socket. CLI-side helper. Empty path uses the default.
// Caller closes.
func DialWatchSocket(path string) (net.Conn, error) {
	if path == "" {
		path = DefaultWatchSocketPath()
	}
	c, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// DecodeWatchEvent reads one JSONL Task line from r. EOF returns
// io.EOF without wrapping so callers can detect clean disconnect.
func DecodeWatchEvent(dec *json.Decoder) (*Task, error) {
	var t Task
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Errors exposed for caller branching.
var (
	ErrNoWatchSocket = errors.New("biam watchsocket: socket not reachable")
)
