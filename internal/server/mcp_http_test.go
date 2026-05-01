package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeMCPHandler emulates the mark3labs/mcp-go StreamableHTTPServer
// for a single-response /mcp POST: it writes the canonical
// `Content-Type: application/json` reply with a JSON-RPC body. The
// real upstream library does the same in streamable_http.go:546 (we
// don't depend on the live MCP server in these tests; the shim's
// contract is purely about HTTP framing).
func fakeMCPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "test-session")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"protocolVersion": "2025-06-18",
				"serverInfo":      map[string]any{"name": "clawtool", "version": "test"},
			},
		})
	})
}

func newShimServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(mcpAcceptShim(fakeMCPHandler()))
}

func postInit(t *testing.T, url, accept string) *http.Response {
	t.Helper()
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	req, _ := http.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestMCP_AcceptJSON_ReturnsJSON(t *testing.T) {
	srv := newShimServer(t)
	defer srv.Close()
	resp := postInit(t, srv.URL, "application/json")
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	b, _ := io.ReadAll(resp.Body)
	var msg map[string]any
	if err := json.Unmarshal(b, &msg); err != nil {
		t.Fatalf("body is not JSON-RPC: %v\nbody=%s", err, b)
	}
	if msg["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", msg["jsonrpc"])
	}
}

func TestMCP_AcceptEventStream_ReturnsSSE(t *testing.T) {
	srv := newShimServer(t)
	defer srv.Close()
	resp := postInit(t, srv.URL, "text/event-stream")
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	// Mcp-Session-Id and other inner-set headers must survive.
	if got := resp.Header.Get("Mcp-Session-Id"); got != "test-session" {
		t.Errorf("Mcp-Session-Id = %q, want test-session", got)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.HasPrefix(body, "data: ") {
		t.Fatalf("body does not start with `data: `:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("body does not end with `\\n\\n`: %q", body)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(body, "data: "), "\n\n")
	var msg map[string]any
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("SSE data payload is not JSON-RPC: %v\npayload=%s", err, payload)
	}
	if msg["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", msg["jsonrpc"])
	}
}

func TestMCP_AcceptBoth_PrefersSSE(t *testing.T) {
	srv := newShimServer(t)
	defer srv.Close()
	// Order doesn't matter; spec says prefer SSE when both are listed.
	resp := postInit(t, srv.URL, "application/json, text/event-stream")
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream (Accept lists both, SSE wins)", got)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(b), "data: ") {
		t.Errorf("body does not start with `data: `:\n%s", b)
	}
}

func TestMCP_AcceptMissing_DefaultsJSON(t *testing.T) {
	srv := newShimServer(t)
	defer srv.Close()
	resp := postInit(t, srv.URL, "")
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (no Accept = legacy default)", got)
	}
	b, _ := io.ReadAll(resp.Body)
	var msg map[string]any
	if err := json.Unmarshal(b, &msg); err != nil {
		t.Fatalf("body is not JSON-RPC: %v\nbody=%s", err, b)
	}
}
