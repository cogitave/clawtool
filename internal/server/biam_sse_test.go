package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/tools/core"
)

// newSSETestServer mounts /v1/biam/subscribe with a fresh BIAM
// store + reset event buffer. Returns the httptest server and a
// cleanup func that drains globals.
func newSSETestServer(t *testing.T, token string) (*httptest.Server, *biam.Store, func()) {
	t.Helper()
	prevStore := core.BiamStore()
	store, err := biam.OpenStore(filepath.Join(t.TempDir(), "biam.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	core.SetBiamStore(store)
	biam.Events.ResetForTest()

	mux := http.NewServeMux()
	authed := authMiddleware(token)
	mux.Handle("/v1/biam/subscribe", authed(http.HandlerFunc(handleBIAMSubscribe)))
	srv := httptest.NewServer(mux)

	cleanup := func() {
		srv.Close()
		core.SetBiamStore(prevStore)
		_ = store.Close()
		biam.Events.ResetForTest()
	}
	return srv, store, cleanup
}

// sseRead pulls SSE frames off body until ctx cancels or the
// stream ends. Returns parsed events.
type sseEvent struct {
	id    string
	event string
	data  string
}

func sseRead(t *testing.T, body io.ReadCloser, want int, deadline time.Duration) []sseEvent {
	t.Helper()
	defer body.Close()
	out := []sseEvent{}
	done := make(chan struct{})

	go func() {
		defer close(done)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var cur sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if cur.event != "" || cur.data != "" || cur.id != "" {
					out = append(out, cur)
					cur = sseEvent{}
					if len(out) >= want {
						return
					}
				}
				continue
			}
			switch {
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(deadline):
		_ = body.Close() // unblock the scanner
		<-done
	}
	return out
}

func TestSSESubscribe_StreamsEvents(t *testing.T) {
	srv, store, cleanup := newSSETestServer(t, "tok")
	defer cleanup()

	ctx := context.Background()
	if err := store.CreateTask(ctx, "task-A", "test", "claude/local"); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Pre-seed 3 events so they're already buffered when we connect.
	biam.Events.Append("task-A", "task", []byte(`{"task_id":"task-A","status":"pending"}`))
	biam.Events.Append("task-A", "frame", []byte(`{"task_id":"task-A","line":"hello"}`))
	biam.Events.Append("task-A", "frame", []byte(`{"task_id":"task-A","line":"world"}`))

	req, _ := http.NewRequest("GET", srv.URL+"/v1/biam/subscribe?task_id=task-A", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("want Content-Type text/event-stream, got %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("want Cache-Control no-cache, got %q", got)
	}

	events := sseRead(t, resp.Body, 3, 2*time.Second)
	if len(events) < 3 {
		t.Fatalf("want >=3 events, got %d (%+v)", len(events), events)
	}
	if events[0].id != "1" || events[0].event != "task" {
		t.Errorf("event[0]: want id=1 event=task, got id=%s event=%s", events[0].id, events[0].event)
	}
	if events[1].id != "2" || events[1].event != "frame" {
		t.Errorf("event[1]: want id=2 event=frame, got id=%s event=%s", events[1].id, events[1].event)
	}
	if events[2].id != "3" || events[2].event != "frame" {
		t.Errorf("event[2]: want id=3 event=frame, got id=%s event=%s", events[2].id, events[2].event)
	}
}

func TestSSESubscribe_RespectsLastEventID(t *testing.T) {
	srv, store, cleanup := newSSETestServer(t, "tok")
	defer cleanup()

	ctx := context.Background()
	if err := store.CreateTask(ctx, "task-B", "test", "codex/local"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	biam.Events.Append("task-B", "task", []byte(`{"i":1}`))
	biam.Events.Append("task-B", "frame", []byte(`{"i":2}`))
	biam.Events.Append("task-B", "frame", []byte(`{"i":3}`))
	biam.Events.Append("task-B", "frame", []byte(`{"i":4}`))

	req, _ := http.NewRequest("GET", srv.URL+"/v1/biam/subscribe?task_id=task-B", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Last-Event-ID", "2")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	events := sseRead(t, resp.Body, 2, 2*time.Second)
	if len(events) < 2 {
		t.Fatalf("want >=2 events (3,4), got %d", len(events))
	}
	if events[0].id != "3" {
		t.Errorf("first event after Last-Event-ID=2 should be id=3, got %s", events[0].id)
	}
	if events[1].id != "4" {
		t.Errorf("second event should be id=4, got %s", events[1].id)
	}
}

func TestSSESubscribe_ClosesOnTerminalStatus(t *testing.T) {
	srv, store, cleanup := newSSETestServer(t, "tok")
	defer cleanup()

	ctx := context.Background()
	if err := store.CreateTask(ctx, "task-C", "test", "gemini/local"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	biam.Events.Append("task-C", "task", []byte(`{"status":"active"}`))

	req, _ := http.NewRequest("GET", srv.URL+"/v1/biam/subscribe?task_id=task-C", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Append a terminal event after we connect; the server should
	// close the connection right after streaming it.
	go func() {
		time.Sleep(50 * time.Millisecond)
		biam.Events.Append("task-C", "terminal", []byte(`{"status":"done"}`))
	}()

	// Read until EOF — when the server closes the body, io.ReadAll
	// returns without error.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "event: terminal") {
		t.Fatalf("expected terminal event in stream; got:\n%s", string(body))
	}
	if !strings.Contains(string(body), "event: task") {
		t.Errorf("expected initial task event in stream too")
	}
}

func TestSSESubscribe_TaskNotFound(t *testing.T) {
	srv, _, cleanup := newSSETestServer(t, "tok")
	defer cleanup()

	req, _ := http.NewRequest("GET", srv.URL+"/v1/biam/subscribe?task_id=nope", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "not found") {
		t.Errorf("want 'not found' in body, got %s", string(body))
	}
}

func TestSSESubscribe_RejectsMissingTaskID(t *testing.T) {
	srv, _, cleanup := newSSETestServer(t, "tok")
	defer cleanup()

	req, _ := http.NewRequest("GET", srv.URL+"/v1/biam/subscribe", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestSSESubscribe_StreamsLiveEventsAfterReplay(t *testing.T) {
	srv, store, cleanup := newSSETestServer(t, "tok")
	defer cleanup()

	ctx := context.Background()
	if err := store.CreateTask(ctx, "task-D", "test", "opencode/local"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	biam.Events.Append("task-D", "task", []byte(`{"i":1}`))

	req, _ := http.NewRequest("GET", srv.URL+"/v1/biam/subscribe?task_id=task-D", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	go func() {
		time.Sleep(40 * time.Millisecond)
		biam.Events.Append("task-D", "frame", []byte(`{"i":2}`))
		time.Sleep(20 * time.Millisecond)
		biam.Events.Append("task-D", "terminal", []byte(`{"i":3}`))
	}()

	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "id: 1") || !strings.Contains(s, "id: 2") || !strings.Contains(s, "id: 3") {
		t.Fatalf("expected ids 1,2,3 in stream; got:\n%s", s)
	}
	if !strings.Contains(s, "event: terminal") {
		t.Errorf("expected terminal event")
	}
}
