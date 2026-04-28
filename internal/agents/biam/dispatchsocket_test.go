package biam

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// stubSubmitter satisfies dispatchSubmitter for tests. Records every
// Submit call so the assertions can inspect what the socket layer
// forwarded. Returns a deterministic taskID per call so the wire
// path is observable.
type stubSubmitter struct {
	mu       sync.Mutex
	calls    []stubCall
	nextID   int
	failNext error
}

type stubCall struct {
	instance string
	prompt   string
	opts     map[string]any
}

func (s *stubSubmitter) Submit(_ context.Context, instance, prompt string, opts map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return "", err
	}
	s.calls = append(s.calls, stubCall{instance: instance, prompt: prompt, opts: opts})
	s.nextID++
	return "stub-task-" + itoa(s.nextID), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+(n%10))) + out
		n /= 10
	}
	return out
}

// TestDispatchSocket_RoundTripsSubmit confirms a full Dial → Submit
// → response cycle hits the runner with the right args and returns
// the runner's task ID to the client. This is the load-bearing
// contract — every other test depends on it working.
func TestDispatchSocket_RoundTripsSubmit(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "dispatch.sock")

	submitter := &stubSubmitter{}
	srvCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- ServeDispatchSocket(srvCtx, submitter, sockPath)
	}()

	// Wait for the socket to bind. ServeDispatchSocket sets up the
	// listener synchronously, but chmod + accept loop start asynchronously.
	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err := DialDispatchSocket(sockPath)
		if err == nil {
			ctx, cctx := context.WithTimeout(t.Context(), 2*time.Second)
			taskID, serr := client.Submit(ctx, "codex", "hello world", map[string]any{"format": "json"})
			cctx()
			if serr != nil {
				t.Fatalf("Submit: %v", serr)
			}
			if taskID != "stub-task-1" {
				t.Errorf("taskID = %q, want stub-task-1", taskID)
			}
			submitter.mu.Lock()
			if len(submitter.calls) != 1 {
				submitter.mu.Unlock()
				t.Fatalf("expected 1 Submit call, got %d", len(submitter.calls))
			}
			c := submitter.calls[0]
			submitter.mu.Unlock()
			if c.instance != "codex" || c.prompt != "hello world" {
				t.Errorf("call args mismatch: %+v", c)
			}
			if c.opts["format"] != "json" {
				t.Errorf("opts didn't transit: %+v", c.opts)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeDispatchSocket did not return after cancel")
	}
}

// TestDispatchSocket_MissingSocketReturnsTypedError confirms callers
// can detect the "no daemon running" case and fall back gracefully
// — this is the load-bearing branch in `clawtool send --async`.
func TestDispatchSocket_MissingSocketReturnsTypedError(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "missing.sock")

	_, err := DialDispatchSocket(sockPath)
	if err == nil {
		t.Fatal("expected error dialling absent socket")
	}
	if !errors.Is(err, ErrNoDispatchSocket) {
		t.Errorf("expected ErrNoDispatchSocket, got %v", err)
	}
}

// TestDispatchSocket_RunnerErrorPropagates confirms a runner-side
// error reaches the client as the response.Error string.
func TestDispatchSocket_RunnerErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "dispatch.sock")

	submitter := &stubSubmitter{failNext: errors.New("simulated runner failure")}
	srvCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = ServeDispatchSocket(srvCtx, submitter, sockPath) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err := DialDispatchSocket(sockPath)
		if err == nil {
			ctx, cctx := context.WithTimeout(t.Context(), 2*time.Second)
			_, serr := client.Submit(ctx, "codex", "hi", nil)
			cctx()
			if serr == nil || serr.Error() != "simulated runner failure" {
				t.Errorf("expected propagated error, got %v", serr)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestDispatchSocket_EmptyPromptRejected confirms the server-side
// guard refuses an empty submit before forwarding to the runner.
// Without this guard a malformed CLI invocation would create a
// no-op task in the BIAM store.
func TestDispatchSocket_EmptyPromptRejected(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "dispatch.sock")

	submitter := &stubSubmitter{}
	srvCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = ServeDispatchSocket(srvCtx, submitter, sockPath) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		client, err := DialDispatchSocket(sockPath)
		if err == nil {
			ctx, cctx := context.WithTimeout(t.Context(), 2*time.Second)
			_, serr := client.Submit(ctx, "codex", "   ", nil)
			cctx()
			if serr == nil {
				t.Error("expected rejection of empty prompt")
			}
			submitter.mu.Lock()
			calls := len(submitter.calls)
			submitter.mu.Unlock()
			if calls != 0 {
				t.Errorf("runner should not have been called for empty prompt, got %d calls", calls)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
