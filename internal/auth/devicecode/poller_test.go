package devicecode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIssuer stands in for an arbitrary OAuth provider's device +
// token endpoints. The handler closures capture per-test state so
// each subtest can shape the response sequence (pending → success,
// slow_down → success, denied, expired) without test cross-talk.
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

func (f *fakeIssuer) cfg() Config {
	return Config{
		HTTP:           f.server.Client(),
		DeviceEndpoint: f.server.URL + "/oauth/device/code",
		TokenEndpoint:  f.server.URL + "/oauth/token",
		ClientID:       "test-client-id",
		Scopes:         "openid profile",
		UserAgent:      "test-agent/1.0",
	}
}

func TestRequestDeviceCode_HappyPath(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("client_id"); got != "test-client-id" {
			t.Errorf("client_id = %q", got)
		}
		if got := r.FormValue("scope"); got != "openid profile" {
			t.Errorf("scope = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DC123","user_code":"WDJB-MJHT","verification_uri":"https://example.com/device","expires_in":900,"interval":5}`))
	}
	dc, err := RequestDeviceCode(context.Background(), f.cfg())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.DeviceCode != "DC123" || dc.UserCode != "WDJB-MJHT" {
		t.Fatalf("unexpected envelope: %+v", dc)
	}
	if dc.PollEvery != 5*time.Second {
		t.Errorf("PollEvery = %v, want 5s", dc.PollEvery)
	}
	if !dc.Expires.After(time.Now().Add(800 * time.Second)) {
		t.Errorf("Expires not in the future: %v", dc.Expires)
	}
}

func TestRequestDeviceCode_NoClientID(t *testing.T) {
	cfg := Config{
		DeviceEndpoint: "https://example.com/device",
	}
	if _, err := RequestDeviceCode(context.Background(), cfg); !errors.Is(err, ErrNoClientID) {
		t.Fatalf("want ErrNoClientID, got %v", err)
	}
}

func TestRequestDeviceCode_FloorsIntervalToRFC(t *testing.T) {
	f := newFakeIssuer(t)
	f.device = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Issuer reports 1s; RFC 8628 §3.5 says we MUST not
		// poll faster than the recommended floor.
		w.Write([]byte(`{"device_code":"DC","user_code":"U","verification_uri":"https://example.com/device","expires_in":600,"interval":1}`))
	}
	dc, err := RequestDeviceCode(context.Background(), f.cfg())
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.PollEvery < MinPollInterval {
		t.Errorf("PollEvery = %v, want >= %v", dc.PollEvery, MinPollInterval)
	}
}

// TestPollerRetriesOnAuthorizationPending validates RFC 8628 §3.5:
// the issuer responds authorization_pending while the user is
// still in their browser; the poller must keep polling at the
// existing interval until either success or expiry.
func TestPollerRetriesOnAuthorizationPending(t *testing.T) {
	f := newFakeIssuer(t)
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch atomic.LoadInt64(&f.pollHits) {
		case 1, 2:
			w.Write([]byte(`{"error":"authorization_pending","error_description":"hold on"}`))
		default:
			w.Write([]byte(`{"access_token":"AT_realtoken","token_type":"bearer","scope":"openid profile"}`))
		}
	}
	dc := &DeviceCode{
		DeviceCode: "DC123",
		Expires:    time.Now().Add(60 * time.Second),
		PollEvery:  10 * time.Millisecond, // squeeze the test
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := Poll(ctx, f.cfg(), dc)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if tok.AccessToken != "AT_realtoken" {
		t.Fatalf("token = %+v", tok)
	}
	if got := atomic.LoadInt64(&f.pollHits); got < 3 {
		t.Errorf("expected at least 3 polls (pending+pending+ok), got %d", got)
	}
}

// TestPollerSlowDownExtendsInterval validates the slow_down branch:
// when the issuer asks us to back off, the next poll must be at
// least the issuer-suggested interval later.
func TestPollerSlowDownExtendsInterval(t *testing.T) {
	f := newFakeIssuer(t)
	var lastPoll atomic.Int64
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		now := time.Now().UnixMilli()
		prev := lastPoll.Swap(now)
		switch atomic.LoadInt64(&f.pollHits) {
		case 1:
			w.Write([]byte(`{"error":"slow_down","interval":1}`))
		default:
			// On the second poll, ensure the slow_down
			// interval was actually honoured (~1s gap, with
			// 200ms slack).
			if prev > 0 && now-prev < 800 {
				t.Errorf("slow_down ignored: gap=%dms (want >=800ms)", now-prev)
			}
			w.Write([]byte(`{"access_token":"AT_x","token_type":"bearer"}`))
		}
	}
	dc := &DeviceCode{
		DeviceCode: "DC",
		Expires:    time.Now().Add(60 * time.Second),
		PollEvery:  10 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Poll(ctx, f.cfg(), dc); err != nil {
		t.Fatalf("Poll: %v", err)
	}
}

// TestPollerTimesOut validates the expires_in deadline. The poller
// keeps getting authorization_pending; before any token comes back,
// the device code's deadline elapses and the poller surfaces
// ErrDeviceCodeExpired.
func TestPollerTimesOut(t *testing.T) {
	f := newFakeIssuer(t)
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"authorization_pending"}`))
	}
	dc := &DeviceCode{
		DeviceCode: "DC",
		Expires:    time.Now().Add(50 * time.Millisecond),
		PollEvery:  10 * time.Millisecond,
	}
	_, err := Poll(context.Background(), f.cfg(), dc)
	if !errors.Is(err, ErrDeviceCodeExpired) {
		t.Fatalf("want ErrDeviceCodeExpired, got %v", err)
	}
}

func TestPollerAccessDenied(t *testing.T) {
	f := newFakeIssuer(t)
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"access_denied"}`))
	}
	dc := &DeviceCode{
		DeviceCode: "DC",
		Expires:    time.Now().Add(60 * time.Second),
		PollEvery:  10 * time.Millisecond,
	}
	_, err := Poll(context.Background(), f.cfg(), dc)
	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("want ErrAuthorizationDenied, got %v", err)
	}
}

func TestPollerExpiredTokenServerSide(t *testing.T) {
	f := newFakeIssuer(t)
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"expired_token"}`))
	}
	dc := &DeviceCode{
		DeviceCode: "DC",
		Expires:    time.Now().Add(60 * time.Second),
		PollEvery:  10 * time.Millisecond,
	}
	_, err := Poll(context.Background(), f.cfg(), dc)
	if !errors.Is(err, ErrDeviceCodeExpired) {
		t.Fatalf("want ErrDeviceCodeExpired, got %v", err)
	}
}

func TestPollerCtxCancelAborts(t *testing.T) {
	f := newFakeIssuer(t)
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"authorization_pending"}`))
	}
	dc := &DeviceCode{
		DeviceCode: "DC",
		Expires:    time.Now().Add(60 * time.Second),
		PollEvery:  10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := Poll(ctx, f.cfg(), dc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
