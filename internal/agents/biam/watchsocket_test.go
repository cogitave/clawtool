package biam

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// TestWatchSocket_EnvelopeMultiplex confirms one connected client
// receives both Task snapshots/transitions and StreamFrames over
// the same socket, each wrapped in a WatchEnvelope with the right
// Kind discriminator.
//
// Why this matters: the orchestrator and `task watch` consumers
// branch on Kind. If the server ever skipped the wrap (e.g. raw
// Task fell through), the dashboard's envelope decoder would barf
// and the orchestrator's frame ringbuffer would stay empty. This
// test guards the wire contract.
func TestWatchSocket_EnvelopeMultiplex(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := t.Context()
	if err := store.CreateTask(ctx, "snap-1", "tester", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, "snap-1", TaskActive, ""); err != nil {
		t.Fatal(err)
	}

	hub := &WatchHub{
		subs:   map[*watchSub]struct{}{},
		frames: map[*frameSub]struct{}{},
	}

	sockPath := filepath.Join(dir, "watch.sock")
	srvCtx, cancelSrv := context.WithCancel(ctx)
	defer cancelSrv()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- ServeWatchSocket(srvCtx, store, hub, sockPath)
	}()

	// Wait for the socket to bind. ServeWatchSocket sets up the
	// listener synchronously, but we still need to give net.Listen
	// + chmod a moment before dialling.
	deadline := time.Now().Add(time.Second)
	var conn interface {
		Close() error
	}
	for {
		c, derr := DialWatchSocket(sockPath)
		if derr == nil {
			conn = c
			defer c.Close()
			dec := json.NewDecoder(c)

			// Snapshot phase — one envelope, Kind=task.
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			var snap WatchEnvelope
			if err := dec.Decode(&snap); err != nil {
				t.Fatalf("snapshot decode: %v", err)
			}
			if snap.Kind != "task" || snap.Task == nil || snap.Task.TaskID != "snap-1" {
				t.Fatalf("expected snapshot task=snap-1, got %+v", snap)
			}

			// Now broadcast a frame and a follow-up task
			// transition, assert each arrives with the right
			// Kind. Sleep briefly so the snapshot pump has
			// drained before the live tail starts.
			time.Sleep(20 * time.Millisecond)
			hub.BroadcastFrame(StreamFrame{
				TaskID: "snap-1",
				Agent:  "claude",
				Line:   "hello from agent",
				Kind:   "stdout",
				TS:     time.Now().UTC(),
			})
			hub.Broadcast(Task{TaskID: "snap-1", Status: TaskDone})

			// Drain up to 2 envelopes; order between frame
			// and task isn't guaranteed (separate channels +
			// select) so accumulate and assert both kinds
			// landed.
			seenFrame := false
			seenTask := false
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			for i := 0; i < 2; i++ {
				var env WatchEnvelope
				if err := dec.Decode(&env); err != nil {
					t.Fatalf("event %d decode: %v", i, err)
				}
				switch env.Kind {
				case "frame":
					if env.Frame == nil || env.Frame.Line != "hello from agent" {
						t.Errorf("bad frame envelope: %+v", env)
					}
					seenFrame = true
				case "task":
					if env.Task == nil || env.Task.Status != TaskDone {
						t.Errorf("bad task envelope: %+v", env)
					}
					seenTask = true
				default:
					t.Errorf("unknown envelope kind %q", env.Kind)
				}
			}
			if !seenFrame || !seenTask {
				t.Errorf("expected both kinds, got frame=%v task=%v", seenFrame, seenTask)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial socket: %v", derr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = conn

	cancelSrv()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeWatchSocket did not return after cancel")
	}
}
