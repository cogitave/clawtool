package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/catalog"
)

// fakeRegistryHTTP returns an httptest server that mimics the
// MCP Registry's /v0/servers endpoint with the given response
// body. Tests use --url to point the verb at this server.
func fakeRegistryHTTP(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSourceRegistry_HumanOutput exercises the default banner
// path: count + per-server `name [version] — description`. Uses
// httptest to inject a deterministic response so the test stays
// network-free in CI.
func TestSourceRegistry_HumanOutput(t *testing.T) {
	srv := fakeRegistryHTTP(t, `{
		"servers": [
			{"server": {"name": "io.github.acme/example", "description": "Example MCP server", "version": "1.0.0"}},
			{"server": {"name": "ac.foo/bar", "description": "Another server", "version": "0.2.0"}}
		]
	}`)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "--url", srv.URL, "--limit", "5"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{
		"MCP Registry:",
		srv.URL,
		"2 server(s) returned",
		"limit 5",
		"io.github.acme/example",
		"[1.0.0]",
		"Example MCP server",
		"ac.foo/bar",
		"[0.2.0]",
		"Another server",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestSourceRegistry_JSONOutput emits the parsed RegistryResult
// as JSON. Pipelines like `clawtool source registry --json | jq
// '.servers[].name'` rely on this shape.
func TestSourceRegistry_JSONOutput(t *testing.T) {
	srv := fakeRegistryHTTP(t, `{
		"servers": [
			{"server": {"name": "io.github.acme/example", "description": "X", "version": "1.0.0"}}
		]
	}`)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "--url", srv.URL, "--limit", "1", "--json"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	var got struct {
		BaseURL string `json:"base_url"`
		Count   int    `json:"count"`
		Servers []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     string `json:"version"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, out.String())
	}
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}
	if got.BaseURL != srv.URL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, srv.URL)
	}
	if len(got.Servers) != 1 || got.Servers[0].Name != "io.github.acme/example" {
		t.Errorf("Servers = %+v", got.Servers)
	}
}

// TestSourceRegistry_HTTPErrorHumanPath confirms a 5xx surfaces
// through stderr on the human path with the wrapped error from
// ProbeRegistry. Exit code is 1.
func TestSourceRegistry_HTTPErrorHumanPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"upstream exploded"}`))
	}))
	t.Cleanup(srv.Close)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "--url", srv.URL})
	if rc != 1 {
		t.Errorf("rc=%d, want 1 on 5xx", rc)
	}
	if !strings.Contains(errb.String(), "500") {
		t.Errorf("stderr should mention status; got %q", errb.String())
	}
	if !strings.Contains(errb.String(), "upstream exploded") {
		t.Errorf("stderr should include upstream body; got %q", errb.String())
	}
	// Human-mode error must NOT leak into stdout.
	if out.Len() > 0 {
		t.Errorf("human-mode stdout should be empty on error; got %q", out.String())
	}
}

// TestSourceRegistry_HTTPErrorJSONPath confirms --json error
// path emits a structured `{"error":"..."}` object on stdout
// (so pipelines can branch on it) instead of the plaintext
// stderr message.
func TestSourceRegistry_HTTPErrorJSONPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"down for maintenance"}`))
	}))
	t.Cleanup(srv.Close)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "--url", srv.URL, "--json"})
	if rc != 1 {
		t.Errorf("rc=%d, want 1", rc)
	}
	body := strings.TrimSpace(out.String())
	if !strings.HasPrefix(body, "{") || !strings.Contains(body, `"error"`) {
		t.Errorf("expected error object on stdout; got %q", body)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if got["error"] == "" {
		t.Error("error field empty")
	}
}

// TestSourceRegistry_RejectsExtraArgs keeps the usage contract
// strict — `source registry foo` is a typo, surface it.
func TestSourceRegistry_RejectsExtraArgs(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "extra-positional"})
	if rc != 2 {
		t.Errorf("rc=%d, want 2 on extra arg", rc)
	}
	if !strings.Contains(errb.String(), "usage:") {
		t.Errorf("expected usage hint, got: %q", errb.String())
	}
}

// TestSourceRegistry_BackendDispatch confirms that --backend
// routes to the right probe(s): smithery hits ProbeSmitheryRegistry
// only, both runs ProbeRegistry AND ProbeSmitheryRegistry and
// merges the results (deduping by Name). We swap the package-level
// probeMCP / probeSmithery vars with counting stubs so the test
// stays network-free and the assertion is on call shape, not
// HTTP wire-bytes (those are already covered in registry_test.go).
func TestSourceRegistry_BackendDispatch(t *testing.T) {
	mcpCalls, smCalls := 0, 0
	stubMCP := func(_ context.Context, _ string, _ int) (*catalog.RegistryResult, error) {
		mcpCalls++
		return &catalog.RegistryResult{
			BaseURL: "https://registry.modelcontextprotocol.io",
			Count:   2,
			Servers: []catalog.RegistryServer{
				{Name: "io.github.acme/example", Version: "1.0.0"},
				{Name: "shared", Version: "2.0.0"},
			},
		}, nil
	}
	stubSmithery := func(_ context.Context, _ string, _ int) (*catalog.RegistryResult, error) {
		smCalls++
		return &catalog.RegistryResult{
			BaseURL: "https://registry.smithery.ai",
			Count:   2,
			Servers: []catalog.RegistryServer{
				{Name: "exa", Description: "Web search"},
				{Name: "shared", Description: "Dup name; should dedupe"},
			},
		}, nil
	}

	origMCP, origSm := probeMCP, probeSmithery
	probeMCP, probeSmithery = stubMCP, stubSmithery
	t.Cleanup(func() { probeMCP, probeSmithery = origMCP, origSm })

	// --backend smithery: only the smithery probe runs.
	mcpCalls, smCalls = 0, 0
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "--backend", "smithery", "--json"})
	if rc != 0 {
		t.Fatalf("smithery rc=%d, stderr=%s", rc, errb.String())
	}
	if mcpCalls != 0 {
		t.Errorf("--backend smithery: ProbeRegistry calls = %d, want 0", mcpCalls)
	}
	if smCalls != 1 {
		t.Errorf("--backend smithery: ProbeSmitheryRegistry calls = %d, want 1", smCalls)
	}
	var smResp catalog.RegistryResult
	if err := json.Unmarshal(out.Bytes(), &smResp); err != nil {
		t.Fatalf("smithery JSON: %v\n%s", err, out.String())
	}
	if smResp.Count != 2 || smResp.Servers[0].Name != "exa" {
		t.Errorf("smithery payload not surfaced; got %+v", smResp)
	}

	// --backend both: both probes run, results merge & dedupe by Name.
	mcpCalls, smCalls = 0, 0
	out, errb = &bytes.Buffer{}, &bytes.Buffer{}
	app = &App{Stdout: out, Stderr: errb}
	rc = app.Run([]string{"source", "registry", "--backend", "both", "--json"})
	if rc != 0 {
		t.Fatalf("both rc=%d, stderr=%s", rc, errb.String())
	}
	if mcpCalls != 1 {
		t.Errorf("--backend both: ProbeRegistry calls = %d, want 1", mcpCalls)
	}
	if smCalls != 1 {
		t.Errorf("--backend both: ProbeSmitheryRegistry calls = %d, want 1", smCalls)
	}
	var bothResp catalog.RegistryResult
	if err := json.Unmarshal(out.Bytes(), &bothResp); err != nil {
		t.Fatalf("both JSON: %v\n%s", err, out.String())
	}
	// 2 mcp + 2 smithery, "shared" collides → 3 distinct names.
	if bothResp.Count != 3 {
		t.Errorf("merged Count = %d, want 3 (dedupe by Name)", bothResp.Count)
	}
	names := make(map[string]int)
	for _, s := range bothResp.Servers {
		names[s.Name]++
	}
	if names["shared"] != 1 {
		t.Errorf("expected 'shared' to appear exactly once, got %d", names["shared"])
	}
	if names["exa"] != 1 || names["io.github.acme/example"] != 1 {
		t.Errorf("merged set missing entries: %+v", names)
	}
}

// TestSourceRegistry_BackendInvalid rejects unknown --backend
// values with rc=2 (usage error) so typos like
// `--backend smithry` surface immediately rather than silently
// falling back to the default backend.
func TestSourceRegistry_BackendInvalid(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"source", "registry", "--backend", "smithry"})
	if rc != 2 {
		t.Errorf("rc=%d, want 2 on bad backend", rc)
	}
	if !strings.Contains(errb.String(), "--backend") {
		t.Errorf("stderr should mention --backend; got %q", errb.String())
	}
}
