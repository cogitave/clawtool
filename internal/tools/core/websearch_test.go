package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/secrets"
)

// braveCannedJSON is a minimal but realistic shape of Brave's
// /res/v1/web/search response — enough for our parser to chew through.
const braveCannedJSON = `{
  "web": {
    "results": [
      {"title": "Go Programming Language", "url": "https://go.dev", "description": "The <strong>Go</strong> language home page."},
      {"title": "Go (Wikipedia)",            "url": "https://en.wikipedia.org/wiki/Go_(programming_language)", "description": "Statically typed compiled language."},
      {"title": "A Tour of Go",              "url": "https://tour.golang.org",   "description": "Interactive intro."}
    ]
  }
}`

// withBraveServer spins up a httptest.Server returning the canned response,
// rewires braveBaseURL to point at it, and returns a cleanup func.
func withBraveServer(t *testing.T, handler http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := braveBaseURL
	braveBaseURL = srv.URL
	return func() {
		braveBaseURL = prev
		srv.Close()
	}
}

func TestBraveBackend_HappyPath(t *testing.T) {
	cleanup := withBraveServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Subscription-Token"); got != "test-key" {
			t.Errorf("X-Subscription-Token = %q, want test-key", got)
		}
		if r.URL.Query().Get("q") != "go language" {
			t.Errorf("q = %q, want 'go language'", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("count") != "5" {
			t.Errorf("count = %q, want 5", r.URL.Query().Get("count"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(braveCannedJSON))
	})
	defer cleanup()

	store := &secrets.Store{Scopes: map[string]map[string]string{}}
	store.Set("websearch", "BRAVE_API_KEY", "test-key")

	b, err := newBraveBackend(store)
	if err != nil {
		t.Fatalf("newBraveBackend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hits, err := b.Search(ctx, "go language", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3", len(hits))
	}
	if hits[0].Title != "Go Programming Language" || hits[0].URL != "https://go.dev" {
		t.Errorf("hit[0] = %+v, want Go.dev top result", hits[0])
	}
	if strings.Contains(hits[0].Snippet, "<strong>") {
		t.Errorf("snippet still has HTML markup: %q", hits[0].Snippet)
	}
}

func TestBraveBackend_MissingKeyReturnsSentinel(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{}}
	t.Setenv("BRAVE_API_KEY", "")

	_, err := newBraveBackend(store)
	if err == nil {
		t.Fatal("expected error when BRAVE_API_KEY missing")
	}
	if !strings.Contains(err.Error(), "BRAVE_API_KEY") {
		t.Errorf("error must mention BRAVE_API_KEY: %v", err)
	}
}

func TestBraveBackend_NonOKResponse(t *testing.T) {
	cleanup := withBraveServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	})
	defer cleanup()

	store := &secrets.Store{Scopes: map[string]map[string]string{}}
	store.Set("websearch", "BRAVE_API_KEY", "anything")
	b, _ := newBraveBackend(store)

	_, err := b.Search(context.Background(), "x", 5)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention status 403: %v", err)
	}
}

func TestResolveBackend_UnknownBackendErrors(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{}}
	store.Set("websearch", "backend", "not-a-real-backend")
	_, err := resolveBackend(store)
	if err == nil || !strings.Contains(err.Error(), "not-a-real-backend") {
		t.Errorf("expected 'unknown backend' error, got: %v", err)
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"plain":                   "plain",
		"a <b>b</b> c":            "a b c",
		"<strong>X</strong> Y":    "X Y",
		"no closing <a href='x'>": "no closing ",
	}
	for in, want := range cases {
		if got := stripHTML(in); got != want {
			t.Errorf("stripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
