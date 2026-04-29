package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub stands in for github.com / api.github.com. Each test
// sets the routes it cares about; the helper records what was
// asked so assertions can verify the wire shape (form fields,
// headers, paths) — that's where the wire-protocol contract
// actually lives.
type fakeGitHub struct {
	server   *httptest.Server
	pollHits int64

	// route handlers
	deviceCode http.HandlerFunc
	token      http.HandlerFunc
	star       http.HandlerFunc
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		if f.deviceCode != nil {
			f.deviceCode(w, r)
			return
		}
		http.Error(w, "no fixture", http.StatusInternalServerError)
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&f.pollHits, 1)
		if f.token != nil {
			f.token(w, r)
			return
		}
		http.Error(w, "no fixture", http.StatusInternalServerError)
	})
	mux.HandleFunc("/user/starred/", func(w http.ResponseWriter, r *http.Request) {
		if f.star != nil {
			f.star(w, r)
			return
		}
		http.Error(w, "no fixture", http.StatusInternalServerError)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGitHub) client() *Client {
	return &Client{
		HTTP:        f.server.Client(),
		BaseURL:     f.server.URL,
		APIBaseURL:  f.server.URL,
		UserAgent:   "test-agent/1.0",
		ClientIDStr: "test-client-id",
	}
}

func TestRequestDeviceCode_HappyPath(t *testing.T) {
	f := newFakeGitHub(t)
	f.deviceCode = func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("client_id"); got != "test-client-id" {
			t.Errorf("client_id = %q, want test-client-id", got)
		}
		if got := r.FormValue("scope"); got != "public_repo" {
			t.Errorf("scope = %q, want public_repo", got)
		}
		if got := r.Header.Get("User-Agent"); got != "test-agent/1.0" {
			t.Errorf("User-Agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DC123","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
	}
	c := f.client()
	dc, err := c.RequestDeviceCode(context.Background(), "public_repo")
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.UserCode != "ABCD-1234" || dc.DeviceCodeStr != "DC123" {
		t.Fatalf("unexpected device code: %+v", dc)
	}
	if dc.PollEvery != 5*time.Second {
		t.Errorf("PollEvery = %v, want 5s", dc.PollEvery)
	}
	if !dc.Expires.After(time.Now().Add(800 * time.Second)) {
		t.Errorf("Expires not in the future: %v", dc.Expires)
	}
}

func TestRequestDeviceCode_NoClientID(t *testing.T) {
	c := NewClient()
	c.ClientIDStr = ""
	saved := ClientID
	ClientID = ""
	defer func() { ClientID = saved }()
	if _, err := c.RequestDeviceCode(context.Background(), "public_repo"); !errors.Is(err, ErrNoClientID) {
		t.Fatalf("want ErrNoClientID, got %v", err)
	}
}

func TestPollAccessToken_PendingThenSuccess(t *testing.T) {
	f := newFakeGitHub(t)
	f.token = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch atomic.LoadInt64(&f.pollHits) {
		case 1:
			w.Write([]byte(`{"error":"authorization_pending","error_description":"hold tight"}`))
		default:
			w.Write([]byte(`{"access_token":"gho_realtoken12345","token_type":"bearer","scope":"public_repo"}`))
		}
	}
	c := f.client()
	dc := &DeviceCode{
		DeviceCodeStr: "DC123",
		Expires:       time.Now().Add(60 * time.Second),
		PollEvery:     20 * time.Millisecond, // fast for test
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := c.PollAccessToken(ctx, dc)
	if err != nil {
		t.Fatalf("PollAccessToken: %v", err)
	}
	if tok != "gho_realtoken12345" {
		t.Fatalf("token = %q", tok)
	}
	if got := atomic.LoadInt64(&f.pollHits); got < 2 {
		t.Errorf("expected at least 2 polls, got %d", got)
	}
}

func TestPollAccessToken_DeniedAndExpired(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		f := newFakeGitHub(t)
		f.token = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"access_denied"}`))
		}
		c := f.client()
		dc := &DeviceCode{Expires: time.Now().Add(60 * time.Second), PollEvery: 10 * time.Millisecond}
		_, err := c.PollAccessToken(context.Background(), dc)
		if !errors.Is(err, ErrAuthorizationDenied) {
			t.Fatalf("want ErrAuthorizationDenied, got %v", err)
		}
	})
	t.Run("expired-server-side", func(t *testing.T) {
		f := newFakeGitHub(t)
		f.token = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"expired_token"}`))
		}
		c := f.client()
		dc := &DeviceCode{Expires: time.Now().Add(60 * time.Second), PollEvery: 10 * time.Millisecond}
		_, err := c.PollAccessToken(context.Background(), dc)
		if !errors.Is(err, ErrDeviceCodeExpired) {
			t.Fatalf("want ErrDeviceCodeExpired, got %v", err)
		}
	})
	t.Run("expired-client-side", func(t *testing.T) {
		f := newFakeGitHub(t)
		f.token = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"authorization_pending"}`))
		}
		c := f.client()
		dc := &DeviceCode{Expires: time.Now().Add(50 * time.Millisecond), PollEvery: 10 * time.Millisecond}
		_, err := c.PollAccessToken(context.Background(), dc)
		if !errors.Is(err, ErrDeviceCodeExpired) {
			t.Fatalf("want ErrDeviceCodeExpired, got %v", err)
		}
	})
}

func TestStarRepo_HappyPath(t *testing.T) {
	f := newFakeGitHub(t)
	f.star = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if got := r.URL.Path; got != "/user/starred/cogitave/clawtool" {
			t.Errorf("path = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gho_x" {
			t.Errorf("Authorization header = %q", got)
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "github+json") {
			t.Errorf("Accept = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}
	c := f.client()
	if err := c.StarRepo(context.Background(), "gho_x", "cogitave", "clawtool"); err != nil {
		t.Fatalf("StarRepo: %v", err)
	}
}

func TestStarRepo_PropagatesAuthErrors(t *testing.T) {
	cases := []struct {
		status   int
		wantSubs string
	}{
		{http.StatusUnauthorized, "401"},
		{http.StatusForbidden, "403"},
		{http.StatusNotFound, "404"},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			f := newFakeGitHub(t)
			f.star = func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}
			c := f.client()
			err := c.StarRepo(context.Background(), "gho_x", "cogitave", "clawtool")
			if err == nil || !strings.Contains(err.Error(), tc.wantSubs) {
				t.Fatalf("status %d: want error containing %q, got %v", tc.status, tc.wantSubs, err)
			}
		})
	}
}

func TestStarRepo_RejectsEmptyOwnerOrRepo(t *testing.T) {
	c := NewClient()
	if err := c.StarRepo(context.Background(), "tok", "", "clawtool"); err == nil {
		t.Errorf("empty owner: want error")
	}
	if err := c.StarRepo(context.Background(), "tok", "cogitave", ""); err == nil {
		t.Errorf("empty repo: want error")
	}
}

func TestStarPageURL(t *testing.T) {
	got := StarPageURL("cogitave", "clawtool")
	want := "https://github.com/cogitave/clawtool"
	if got != want {
		t.Errorf("StarPageURL = %q, want %q", got, want)
	}
}
