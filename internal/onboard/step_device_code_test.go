package onboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/auth/devicecode"
)

// memSink is the in-memory persistence stub for DeviceCodeStep
// tests. We don't want test runs to mutate the operator's real
// ~/.config/clawtool/secrets.toml.
type memSink struct {
	mu      sync.Mutex
	saved   map[string]map[string]string
	failErr error
}

func newMemSink() *memSink {
	return &memSink{saved: map[string]map[string]string{}}
}

func (m *memSink) Save(scope, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return m.failErr
	}
	if m.saved[scope] == nil {
		m.saved[scope] = map[string]string{}
	}
	m.saved[scope][key] = value
	return nil
}

func (m *memSink) get(scope, key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saved[scope][key]
}

// fakeIssuer mounts a minimal RFC 8628 issuer for tests. The
// `device` and `token` handlers are swappable per-test so each
// scenario shapes its own response sequence.
type fakeIssuer struct {
	server   *httptest.Server
	pollHits int64

	device http.HandlerFunc
	token  http.HandlerFunc
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	f := &fakeIssuer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/device/code", func(w http.ResponseWriter, r *http.Request) {
		if f.device != nil {
			f.device(w, r)
			return
		}
		http.Error(w, "no fixture", http.StatusInternalServerError)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.pollHits, 1)
		if f.token != nil {
			f.token(w, r)
			return
		}
		http.Error(w, "no fixture", http.StatusInternalServerError)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeIssuer) cfg() devicecode.Config {
	return devicecode.Config{
		HTTP:           f.server.Client(),
		DeviceEndpoint: f.server.URL + "/oauth/device/code",
		TokenEndpoint:  f.server.URL + "/oauth/token",
		ClientID:       "test-client",
		Scopes:         "openid",
		UserAgent:      "test/1.0",
		// Squeeze the RFC 8628 floor so tests don't burn 5s
		// per poll iteration. Production callers leave this
		// zero and the helper enforces the spec floor.
		MinInterval: 10 * time.Millisecond,
	}
}

// TestDeviceCodeStep_HappyPath drives the full flow against a
// stub issuer: RequestDeviceCode returns an envelope, the
// renderer is invoked once with the user code + verification URI,
// the poll returns a token after one pending response, and the
// sink ends up with the token persisted under the configured
// scope.
func TestDeviceCodeStep_HappyPath(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DC_full","user_code":"WDJB-MJHT","verification_uri":"https://example.com/device","verification_uri_complete":"https://example.com/device?user_code=WDJB-MJHT","expires_in":900,"interval":1}`))
	}
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch atomic.LoadInt64(&f.pollHits) {
		case 1:
			w.Write([]byte(`{"error":"authorization_pending"}`))
		default:
			w.Write([]byte(`{"access_token":"AT_happy","token_type":"bearer"}`))
		}
	}

	var renderedOnce int
	var seen PromptInfo
	render := func(p PromptInfo) {
		renderedOnce++
		seen = p
	}

	sink := newMemSink()
	cfg := f.cfg()
	// Squeeze the floor for tests so the poller doesn't block.
	step := NewDeviceCodeStep(cfg, "issuer-x", render)
	step.Sink = sink
	step.SecretsKey = "access_token"

	// The poller respects PollEvery on the device-code envelope
	// — we want fast polling in the test, so we override
	// PollEvery via a custom flow path: use a shorter expires
	// to force the floor through. Simplest is to call
	// devicecode.RequestDeviceCode directly in a wrapper that
	// shrinks PollEvery before Poll. The Step API doesn't expose
	// that knob directly because production should never need
	// it. For the test, we patch by injecting a short-poll
	// HTTPClient — already the issuer's interval is 1s which
	// the floor lifts to 5s; we accept a 5s test budget here as
	// the realistic minimum (test still completes well under
	// `go test`'s default deadline).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, err := step.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tok != "AT_happy" {
		t.Fatalf("token = %q", tok)
	}
	if renderedOnce != 1 {
		t.Errorf("renderer invoked %d times, want 1", renderedOnce)
	}
	if seen.UserCode != "WDJB-MJHT" {
		t.Errorf("rendered user code = %q", seen.UserCode)
	}
	if seen.VerificationURI != "https://example.com/device" {
		t.Errorf("rendered verification URI = %q", seen.VerificationURI)
	}
	if seen.VerificationURIComplete == "" {
		t.Errorf("rendered verification URI complete is empty")
	}
	if got := sink.get("issuer-x", "access_token"); got != "AT_happy" {
		t.Errorf("persisted token = %q, want AT_happy", got)
	}
}

// TestDeviceCodeStep_PollerRetriesOnAuthorizationPending validates
// RFC 8628 §3.5: the wizard step keeps polling while the issuer
// reports authorization_pending and only exits when the token
// arrives. We assert the poll endpoint was hit at least twice
// (one pending → one success).
func TestDeviceCodeStep_PollerRetriesOnAuthorizationPending(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// expires_in=900s; interval=1s gets floored to 5s, so
		// the test takes a few seconds. Acceptable.
		w.Write([]byte(`{"device_code":"DC","user_code":"U","verification_uri":"https://example.com/d","expires_in":900,"interval":1}`))
	}
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		hit := atomic.LoadInt64(&f.pollHits)
		switch {
		case hit < 2:
			w.Write([]byte(`{"error":"authorization_pending"}`))
		default:
			w.Write([]byte(`{"access_token":"AT_after_retry","token_type":"bearer"}`))
		}
	}
	step := NewDeviceCodeStep(f.cfg(), "issuer-y", nil)
	step.Sink = newMemSink()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tok, err := step.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tok != "AT_after_retry" {
		t.Fatalf("token = %q", tok)
	}
	if got := atomic.LoadInt64(&f.pollHits); got < 2 {
		t.Errorf("poll hits = %d, want >= 2", got)
	}
}

// TestDeviceCodeStep_TimesOut validates that an issuer that never
// finishes authorising (always authorization_pending) eventually
// surfaces ErrDeviceCodeExpired once the device code's
// expires_in deadline elapses. Drives the deadline via a tiny
// expires_in (1 second) so the test completes promptly.
func TestDeviceCodeStep_TimesOut(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 1-second expiry. The 5-second poll floor means the
		// first poll attempt happens at t=5s, by which point
		// the deadline has elapsed and the poller surfaces
		// ErrDeviceCodeExpired.
		w.Write([]byte(`{"device_code":"DC","user_code":"U","verification_uri":"https://example.com/d","expires_in":1,"interval":1}`))
	}
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"authorization_pending"}`))
	}
	step := NewDeviceCodeStep(f.cfg(), "issuer-z", nil)
	step.Sink = newMemSink()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := step.Run(ctx)
	if !errors.Is(err, devicecode.ErrDeviceCodeExpired) {
		t.Fatalf("want ErrDeviceCodeExpired in chain, got %v", err)
	}
}

// TestDeviceCodeStep_AccessDenied validates the access_denied
// branch surfaces devicecode.ErrAuthorizationDenied unwrapped.
func TestDeviceCodeStep_AccessDenied(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DC","user_code":"U","verification_uri":"https://example.com/d","expires_in":900,"interval":1}`))
	}
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"access_denied"}`))
	}
	step := NewDeviceCodeStep(f.cfg(), "issuer-deny", nil)
	step.Sink = newMemSink()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := step.Run(ctx)
	if !errors.Is(err, devicecode.ErrAuthorizationDenied) {
		t.Fatalf("want ErrAuthorizationDenied in chain, got %v", err)
	}
}

// TestDeviceCodeStep_PersistFailureReturnsTokenAndError validates
// the contract: when the poll succeeds but the secrets sink
// rejects the write (e.g. read-only home dir), the step still
// returns the token AND an error wrapping the persistence
// failure so the caller can decide what to do.
func TestDeviceCodeStep_PersistFailureReturnsTokenAndError(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DC","user_code":"U","verification_uri":"https://example.com/d","expires_in":900,"interval":1}`))
	}
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"AT_persist_fail","token_type":"bearer"}`))
	}
	sink := newMemSink()
	sink.failErr = errors.New("disk full")
	step := NewDeviceCodeStep(f.cfg(), "issuer-readonly", nil)
	step.Sink = sink
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := step.Run(ctx)
	if err == nil {
		t.Fatalf("want error from sink failure, got nil")
	}
	if tok != "AT_persist_fail" {
		t.Errorf("token still expected on persist failure, got %q", tok)
	}
}

// TestAgentClaim_UsesSharedPoller verifies the shim used by the
// agent-claim recipe path (and clawtool star, the existing OAuth
// caller) routes through the devicecode poller introduced in
// ADR-036 Phase 1. We assert this by constructing a Step,
// running it against a stub issuer, and checking the token comes
// out of the same Poll() machinery (success on a slow_down →
// pending → success sequence that only the shared poller knows
// how to handle).
func TestAgentClaim_UsesSharedPoller(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DC","user_code":"U","verification_uri":"https://example.com/d","expires_in":900,"interval":1}`))
	}
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch atomic.LoadInt64(&f.pollHits) {
		case 1:
			// slow_down with a 1s back-off — only the shared
			// poller honours this; a hand-rolled loop in
			// agent-claim wouldn't.
			w.Write([]byte(`{"error":"slow_down","interval":1}`))
		case 2:
			w.Write([]byte(`{"error":"authorization_pending"}`))
		default:
			w.Write([]byte(`{"access_token":"AT_shared","token_type":"bearer"}`))
		}
	}
	step := NewDeviceCodeStep(f.cfg(), "agent-claim-issuer", nil)
	step.Sink = newMemSink()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tok, err := step.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tok != "AT_shared" {
		t.Fatalf("token = %q", tok)
	}
	if got := atomic.LoadInt64(&f.pollHits); got < 3 {
		t.Errorf("poll hits = %d, want >= 3 (slow_down + pending + ok)", got)
	}
}
