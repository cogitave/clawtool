package biam

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunner_Submit_HonoursFromInstance confirms the cross-host
// BIAM bidi path: when codex / gemini / opencode dispatches through
// the shared daemon, the resulting envelope's `from` reflects the
// caller's family, not the daemon's own identity. Without this the
// BIAM thread audit trail and reply routing collapse onto the
// initiator.
func TestRunner_Submit_HonoursFromInstance(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := LoadOrCreateIdentity(filepath.Join(dir, "identity.ed25519"))
	if err != nil {
		t.Fatal(err)
	}

	send := func(_ context.Context, _ string, _ string, _ map[string]any) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("ok")), nil
	}
	r := NewRunner(store, id, send)

	tests := []struct {
		name       string
		opts       map[string]any
		wantSender string
	}{
		{
			name:       "default identity when from_instance absent",
			opts:       map[string]any{},
			wantSender: id.InstanceID,
		},
		{
			name:       "explicit from_instance overrides",
			opts:       map[string]any{"from_instance": "codex"},
			wantSender: "codex",
		},
		{
			name:       "whitespace-only from_instance falls back to default",
			opts:       map[string]any{"from_instance": "   "},
			wantSender: id.InstanceID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			// Submit synchronously; THEN spawn the polling
			// goroutine with the captured ID. Avoids the
			// race-detector hit on a shared taskID variable
			// (CI's `go test -race` caught it).
			taskID, err := r.Submit(ctx, "claude", "ping", tc.opts)
			if err != nil {
				t.Fatalf("submit: %v", err)
			}

			done := make(chan struct{})
			go func() {
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					tk, err := store.GetTask(ctx, taskID)
					if err == nil && tk != nil && tk.Status.IsTerminal() {
						close(done)
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				close(done)
			}()
			<-done

			msgs, err := store.MessagesFor(ctx, taskID)
			if err != nil {
				t.Fatalf("messages: %v", err)
			}
			if len(msgs) == 0 {
				t.Fatalf("expected at least one envelope, got 0")
			}
			// First envelope is always the prompt — that's the one
			// whose `from` we assert. Result envelope (if it lands
			// before MessagesFor returns) reverses the addresses
			// and would muddy the assertion.
			if got := msgs[0].From.InstanceID; got != tc.wantSender {
				t.Errorf("envelope.from.instance_id = %q, want %q",
					got, tc.wantSender)
			}
		})
	}
}
