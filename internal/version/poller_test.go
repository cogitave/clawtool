package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubGitHub returns a 200 + tag_name body. Reuses the package
// updateHTTPClient + UpdateCheckURL seam by swapping the singleton
// for the duration of the test.
func stubGitHub(t *testing.T, tag string) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	prevClient := updateHTTPClient
	prevURL := updateCheckURLOverride
	updateHTTPClient = srv.Client()
	updateCheckURLOverride = srv.URL
	return func() {
		updateHTTPClient = prevClient
		updateCheckURLOverride = prevURL
		srv.Close()
	}
}

// recorder collects every publish call so the test can inspect the
// payload + count.
type recorder struct {
	mu     sync.Mutex
	events []recorderEvent
}

type recorderEvent struct {
	kind, severity, title, body, action string
}

func (r *recorder) publish(kind, severity, title, body, actionHint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recorderEvent{kind, severity, title, body, actionHint})
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// TestPoller_PublishesOnceOnUpdateAvailable confirms the poller
// fires exactly one SystemNotification when GitHub returns a newer
// tag than the local Version. Subsequent ticks with the same tag
// are silent — operator sees the banner once per release, not per
// tick.
func TestPoller_PublishesOnceOnUpdateAvailable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cleanup := stubGitHub(t, "v9.9.9")
	defer cleanup()

	rec := &recorder{}
	var checkCount atomic.Int32
	track := func(_ string) { checkCount.Add(1) }
	p := NewPoller(rec.publish, PollerConfig{Interval: 30 * time.Millisecond, Timeout: 200 * time.Millisecond}, track)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	go p.Run(ctx)

	// Wait for ctx to expire so the poller has time for ~6 ticks.
	<-ctx.Done()

	if rec.count() != 1 {
		t.Errorf("expected exactly 1 publish, got %d (ticks: %d)", rec.count(), checkCount.Load())
	}
	if checkCount.Load() < 2 {
		t.Errorf("expected at least 2 ticks in 200ms with 30ms interval, got %d", checkCount.Load())
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) > 0 {
		ev := rec.events[0]
		if ev.kind != "update_available" {
			t.Errorf("kind = %q, want update_available", ev.kind)
		}
		if ev.action != "clawtool upgrade" {
			t.Errorf("action = %q, want 'clawtool upgrade'", ev.action)
		}
	}
}

// TestPoller_NoPublishWhenUpToDate confirms the poller stays silent
// when GitHub's latest tag is ≤ local Version.
func TestPoller_NoPublishWhenUpToDate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Stub returns the SAME tag as our local Version → no update.
	cleanup := stubGitHub(t, "v"+Version)
	defer cleanup()

	rec := &recorder{}
	p := NewPoller(rec.publish, PollerConfig{Interval: 20 * time.Millisecond, Timeout: 200 * time.Millisecond}, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	go p.Run(ctx)
	<-ctx.Done()

	if rec.count() != 0 {
		t.Errorf("expected zero publishes when up-to-date, got %d", rec.count())
	}
}

// TestPoller_TelemetryFiresOnEveryTick confirms every check emits
// a `clawtool.update_check` event, regardless of whether it
// triggered a publish. Operators can chart check volume even when
// no transitions occur.
func TestPoller_TelemetryFiresOnEveryTick(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cleanup := stubGitHub(t, "v"+Version)
	defer cleanup()

	var ticks atomic.Int32
	track := func(outcome string) {
		ticks.Add(1)
		if outcome != "up_to_date" {
			t.Errorf("unexpected outcome %q in up-to-date scenario", outcome)
		}
	}
	p := NewPoller(nil, PollerConfig{Interval: 20 * time.Millisecond, Timeout: 200 * time.Millisecond}, track)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()
	// Wait for ctx to expire AND the goroutine to fully exit, so
	// any in-flight track() call completes before the test returns.
	// Without the second receive, the runtime panics with
	// "Fail in goroutine after test has completed" when track()
	// fires after the test stack has unwound.
	<-ctx.Done()
	<-done

	if got := ticks.Load(); got < 3 {
		t.Errorf("expected ≥3 ticks in 100ms, got %d", got)
	}
}
