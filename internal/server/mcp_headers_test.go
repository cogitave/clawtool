package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// captureHandler is a tiny http.Handler used by the server-side
// tests. It records the resolved Mcp-Method / Mcp-Name from
// context (set by mcpHeaderMiddleware) so a test can assert on
// them after the request completes. The body it returns is
// deliberately minimal — these tests are about header /
// context plumbing, not JSON-RPC semantics.
type captureHandler struct {
	method atomic.Value // string
	name   atomic.Value // string
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.method.Store(MCPMethodFromContext(r.Context()))
	c.name.Store(MCPNameFromContext(r.Context()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
}

func (c *captureHandler) Method() string {
	v, _ := c.method.Load().(string)
	return v
}

func (c *captureHandler) Name() string {
	v, _ := c.name.Load().(string)
	return v
}

// TestMcpMethodHeader_ServerReadsIncoming — POST with `Mcp-Method:
// tools/list` reaches the inner handler with the method exposed
// via context.
func TestMcpMethodHeader_ServerReadsIncoming(t *testing.T) {
	cap := &captureHandler{}
	srv := httptest.NewServer(mcpHeaderMiddleware(cap))
	defer srv.Close()

	// Body deliberately uses the SAME method as the header so this
	// test asserts the "header path" without triggering the
	// mismatch branch.
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderMcpMethod, "tools/list")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if got := cap.Method(); got != "tools/list" {
		t.Errorf("ctx Mcp-Method = %q, want %q", got, "tools/list")
	}
	if got := resp.Header.Get(HeaderMcpMethod); got != "tools/list" {
		t.Errorf("response Mcp-Method header = %q, want %q", got, "tools/list")
	}
	// tools/list has no sub-target → no Mcp-Name on response.
	if got := resp.Header.Get(HeaderMcpName); got != "" {
		t.Errorf("response Mcp-Name = %q, want empty (tools/list has no sub-target)", got)
	}
}

// TestMcpMethodHeader_ClientSetsOutgoing — BuildMCPRequest carries
// both headers with correct values for a tools/call body.
func TestMcpMethodHeader_ClientSetsOutgoing(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"mcp__clawtool__SendMessage","arguments":{"prompt":"hi"}}}`)
	req, err := BuildMCPRequest(context.Background(), "http://example.invalid/mcp", body)
	if err != nil {
		t.Fatal(err)
	}

	if got := req.Method; got != http.MethodPost {
		t.Errorf("HTTP method = %q, want POST", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get(HeaderMcpMethod); got != "tools/call" {
		t.Errorf("%s = %q, want %q", HeaderMcpMethod, got, "tools/call")
	}
	if got := req.Header.Get(HeaderMcpName); got != "mcp__clawtool__SendMessage" {
		t.Errorf("%s = %q, want %q", HeaderMcpName, got, "mcp__clawtool__SendMessage")
	}

	// Body must remain readable by Do — BuildMCPRequest gives us
	// a fresh reader, so this is just a sanity check that the
	// payload bytes survived round-tripping.
	got, _ := io.ReadAll(req.Body)
	if !bytes.Contains(got, []byte(`"method":"tools/call"`)) {
		t.Errorf("body missing method field: %s", got)
	}
}

// TestMcpMethodHeader_BodyMismatchPrefersBody — request with
// `Mcp-Method: foo` but body method `bar` → handler observes
// `bar` and a stderr warning is emitted.
func TestMcpMethodHeader_BodyMismatchPrefersBody(t *testing.T) {
	// Capture stderr so we can assert the warning was emitted.
	// The middleware writes via fmt.Fprintf(os.Stderr, …) — same
	// pattern other server-package code uses for transient
	// warnings.
	origStderr := os.Stderr
	rPipe, wPipe, _ := os.Pipe()
	os.Stderr = wPipe
	defer func() { os.Stderr = origStderr }()

	cap := &captureHandler{}
	srv := httptest.NewServer(mcpHeaderMiddleware(cap))
	defer srv.Close()

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"bar"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderMcpMethod, "foo")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	// Close the write end + restore stderr so we can read what
	// was buffered. (Read from rPipe in a goroutine would race
	// the deferred restore — close-then-read is safer.)
	_ = wPipe.Close()
	os.Stderr = origStderr
	stderrBytes, _ := io.ReadAll(rPipe)
	stderr := string(stderrBytes)

	if got := cap.Method(); got != "bar" {
		t.Errorf("ctx Mcp-Method = %q, want %q (body should win on mismatch)", got, "bar")
	}
	if got := resp.Header.Get(HeaderMcpMethod); got != "bar" {
		t.Errorf("response Mcp-Method = %q, want %q", got, "bar")
	}
	if !strings.Contains(stderr, "SEP-2243") || !strings.Contains(stderr, "preferring body") {
		t.Errorf("expected warning on stderr, got: %q", stderr)
	}
}

// TestMcpMethodHeader_NoSubTargetOmitsName — methods like
// `notifications/initialized` produce NO Mcp-Name header (the
// header must be absent, not present with empty value).
func TestMcpMethodHeader_NoSubTargetOmitsName(t *testing.T) {
	cap := &captureHandler{}
	srv := httptest.NewServer(mcpHeaderMiddleware(cap))
	defer srv.Close()

	body := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, body)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if got := cap.Method(); got != "notifications/initialized" {
		t.Errorf("ctx Mcp-Method = %q, want notifications/initialized", got)
	}
	if got := cap.Name(); got != "" {
		t.Errorf("ctx Mcp-Name = %q, want empty (no sub-target)", got)
	}
	// Critical check: header must be ABSENT, not empty-string.
	// http.Header.Values returns nil for absent keys. Get returns
	// "" for both absent and empty, so we use Values to
	// distinguish.
	if vs := resp.Header.Values(HeaderMcpName); len(vs) != 0 {
		t.Errorf("response %s = %v, want absent (len 0)", HeaderMcpName, vs)
	}

	// Likewise, BuildMCPRequest must omit Mcp-Name for the same
	// method on the outbound side.
	out := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	creq, err := BuildMCPRequest(context.Background(), "http://example.invalid/mcp", out)
	if err != nil {
		t.Fatal(err)
	}
	if vs := creq.Header.Values(HeaderMcpName); len(vs) != 0 {
		t.Errorf("outbound request %s = %v, want absent", HeaderMcpName, vs)
	}
	if got := creq.Header.Get(HeaderMcpMethod); got != "notifications/initialized" {
		t.Errorf("outbound %s = %q, want notifications/initialized", HeaderMcpMethod, got)
	}
}

// TestMcpMethodHeader_BodyOnlyResolvesFromBody — header absent,
// body carries the method → handler still sees the method via
// context. Covers the "no Mcp-Method header sent" path the spec
// requires us to handle (legacy clients).
func TestMcpMethodHeader_BodyOnlyResolvesFromBody(t *testing.T) {
	cap := &captureHandler{}
	srv := httptest.NewServer(mcpHeaderMiddleware(cap))
	defer srv.Close()

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"weekly-report"}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, body)
	req.Header.Set("Content-Type", "application/json")
	// Note: NO Mcp-Method header set.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if got := cap.Method(); got != "prompts/get" {
		t.Errorf("ctx Mcp-Method = %q, want prompts/get", got)
	}
	if got := cap.Name(); got != "weekly-report" {
		t.Errorf("ctx Mcp-Name = %q, want weekly-report", got)
	}
	if got := resp.Header.Get(HeaderMcpName); got != "weekly-report" {
		t.Errorf("response Mcp-Name = %q, want weekly-report", got)
	}
}

// Sanity check the JSON-RPC envelope parser doesn't choke on
// arbitrary-shaped params (an array, a number, etc.) — the
// envelope only cares about params.name as a string and should
// fall back to no-name when params isn't an object. Regression
// guard: a panic here would crash every /mcp request.
func TestPeekJSONRPC_NonObjectParams(t *testing.T) {
	cases := []string{
		`{"jsonrpc":"2.0","method":"foo","params":[1,2,3]}`,
		`{"jsonrpc":"2.0","method":"foo","params":42}`,
		`{"jsonrpc":"2.0","method":"foo"}`,
	}
	for _, body := range cases {
		req, _ := http.NewRequest(http.MethodPost, "http://x.invalid", strings.NewReader(body))
		// peekJSONRPC may set ok=false for the array/number cases
		// because the envelope's Params struct can't decode
		// non-object shapes — that's fine. The test just asserts
		// no panic + the body is restored either way.
		_, _ = peekJSONRPC(req)
		got, _ := io.ReadAll(req.Body)
		if string(got) != body {
			t.Errorf("body not restored: got %q, want %q", got, body)
		}
	}
}

// Integration check: when the streamable handler chain sets a
// response header (e.g. Mcp-Session-Id), the middleware's own
// header writes shouldn't clobber it. Tests the shared
// ResponseWriter contract.
func TestMcpMethodHeader_DoesNotClobberInnerHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "sess-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	})
	srv := httptest.NewServer(mcpHeaderMiddleware(inner))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "Echo"},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if got := resp.Header.Get("Mcp-Session-Id"); got != "sess-1" {
		t.Errorf("Mcp-Session-Id = %q, want sess-1 (middleware must not clobber)", got)
	}
	if got := resp.Header.Get(HeaderMcpMethod); got != "tools/call" {
		t.Errorf("Mcp-Method = %q, want tools/call", got)
	}
	if got := resp.Header.Get(HeaderMcpName); got != "Echo" {
		t.Errorf("Mcp-Name = %q, want Echo", got)
	}
}
