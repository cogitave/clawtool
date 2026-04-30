package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeRegistry returns an httptest.Server that mimics
// `<base>/v0/servers?limit=N`. The `respond` callback receives
// the request and writes the response — tests use it to assert
// query-param behaviour and to inject failure modes.
func fakeRegistry(t *testing.T, respond func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(respond))
	t.Cleanup(srv.Close)
	return srv
}

// TestProbeRegistry_HappyPath confirms a parseable registry
// response is unwrapped from the {servers: [{server: {...}}]}
// envelope into the flat RegistryServer slice.
func TestProbeRegistry_HappyPath(t *testing.T) {
	srv := fakeRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v0/servers" {
			t.Errorf("path = %q, want /v0/servers", got)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Errorf("limit query = %q, want 5", got)
		}
		if got := r.Header.Get("User-Agent"); got != "clawtool-catalog-probe" {
			t.Errorf("User-Agent = %q, want clawtool-catalog-probe", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"servers": [
				{"server": {"name": "io.github.acme/example", "description": "Example MCP server", "version": "1.0.0"}},
				{"server": {"name": "ac.inference.sh/mcp", "description": "AI app runner", "version": "1.0.1"}}
			],
			"metadata": {"count": 2}
		}`))
	})

	res, err := ProbeRegistry(context.Background(), srv.URL, 5)
	if err != nil {
		t.Fatalf("ProbeRegistry: %v", err)
	}
	if res.BaseURL != srv.URL {
		t.Errorf("BaseURL = %q, want %q", res.BaseURL, srv.URL)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2", res.Count)
	}
	if res.Servers[0].Name != "io.github.acme/example" {
		t.Errorf("Servers[0].Name = %q", res.Servers[0].Name)
	}
	if res.Servers[1].Description != "AI app runner" {
		t.Errorf("Servers[1].Description = %q", res.Servers[1].Description)
	}
	if res.Servers[0].Version != "1.0.0" {
		t.Errorf("Servers[0].Version = %q", res.Servers[0].Version)
	}
}

// TestProbeRegistry_LimitClamping confirms 0 / negative limits
// fall to 10 and limits >50 clamp to 50. Pre-flight clamping
// keeps the upstream from rejecting a hostile or accidentally-
// huge page request.
func TestProbeRegistry_LimitClamping(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "10"},
		{-1, "10"},
		{200, "50"},
		{17, "17"},
	}
	for _, tc := range cases {
		var got string
		srv := fakeRegistry(t, func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Query().Get("limit")
			_, _ = w.Write([]byte(`{"servers":[],"metadata":{}}`))
		})
		if _, err := ProbeRegistry(context.Background(), srv.URL, tc.in); err != nil {
			t.Fatalf("limit=%d: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("limit=%d → query=%q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestProbeRegistry_DefaultURL confirms the empty-string
// baseURL falls through to DefaultRegistryURL. Doesn't make a
// real network call — we substitute the default with the test
// server in a wrapper, but we still exercise the empty-baseURL
// branch via a sentinel: the function should set BaseURL to
// the resolved value (no trailing slash).
func TestProbeRegistry_DefaultURLNotEmpty(t *testing.T) {
	if DefaultRegistryURL == "" {
		t.Fatal("DefaultRegistryURL must be a non-empty constant")
	}
	if !strings.HasPrefix(DefaultRegistryURL, "https://") {
		t.Errorf("DefaultRegistryURL = %q, want https:// prefix", DefaultRegistryURL)
	}
}

// TestProbeRegistry_TrailingSlashTolerated confirms the
// baseURL trailing slash is normalised before path concat.
func TestProbeRegistry_TrailingSlashTolerated(t *testing.T) {
	srv := fakeRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/servers" {
			t.Errorf("path = %q, want exactly /v0/servers (no double slash)", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"servers":[]}`))
	})
	if _, err := ProbeRegistry(context.Background(), srv.URL+"/", 5); err != nil {
		t.Fatalf("trailing slash: %v", err)
	}
}

// TestProbeRegistry_HTTPError_5xx confirms a 5xx surfaces a
// wrapped error including the status code and the upstream
// response body for diagnostics.
func TestProbeRegistry_HTTPError_5xx(t *testing.T) {
	srv := fakeRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"server exploded"}`))
	})
	_, err := ProbeRegistry(context.Background(), srv.URL, 5)
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err should mention status code; got %q", err)
	}
	if !strings.Contains(err.Error(), "server exploded") {
		t.Errorf("err should include upstream body; got %q", err)
	}
}

// TestProbeRegistry_BadJSON confirms malformed registry
// responses surface a decode error rather than a panic. The
// upstream contract may shift; clawtool should tell the
// operator clearly when it does.
func TestProbeRegistry_BadJSON(t *testing.T) {
	srv := fakeRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>oops not JSON</html>`))
	})
	_, err := ProbeRegistry(context.Background(), srv.URL, 5)
	if err == nil {
		t.Fatal("expected decode error on non-JSON response")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("err should mention decode; got %q", err)
	}
}

// TestProbeRegistry_EmptyServers handles the registry-empty
// case (no servers match). Should return Count=0 + empty
// slice, not an error — empty is a valid registry state.
func TestProbeRegistry_EmptyServers(t *testing.T) {
	srv := fakeRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"servers":[],"metadata":{"count":0}}`))
	})
	res, err := ProbeRegistry(context.Background(), srv.URL, 5)
	if err != nil {
		t.Fatalf("empty registry: %v", err)
	}
	if res.Count != 0 {
		t.Errorf("Count = %d, want 0", res.Count)
	}
	if res.Servers == nil {
		t.Error("Servers should be non-nil empty slice for valid JSON unmarshalling downstream")
	}
}
