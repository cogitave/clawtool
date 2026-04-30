package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// mkSourceRegistryReq fabricates a CallToolRequest with optional
// limit + url overrides. Mirrors the agent_detect / source_check
// test helper pattern.
func mkSourceRegistryReq(url string, limit int) mcp.CallToolRequest {
	args := map[string]any{}
	if url != "" {
		args["url"] = url
	}
	if limit > 0 {
		args["limit"] = float64(limit)
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "SourceRegistry",
			Arguments: args,
		},
	}
}

// fakeRegistryServer mimics the MCP Registry's /v0/servers
// endpoint with the given response body.
func fakeRegistryServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status > 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSourceRegistry_HappyPath exercises the envelope unwrap +
// projection. Tool returns RegistryResult shape with snake_case
// JSON tags + per-server entries.
func TestSourceRegistry_HappyPath(t *testing.T) {
	srv := fakeRegistryServer(t, `{
		"servers": [
			{"server": {"name": "io.github.acme/example", "description": "Example MCP server", "version": "1.0.0"}},
			{"server": {"name": "ac.foo/bar", "description": "Another", "version": "0.2.0"}}
		]
	}`, 0)

	res, err := runSourceRegistry(context.Background(), mkSourceRegistryReq(srv.URL, 5))
	if err != nil {
		t.Fatalf("runSourceRegistry: %v", err)
	}
	got, ok := res.StructuredContent.(sourceRegistryResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sourceRegistryResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Errorf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if got.URL != srv.URL {
		t.Errorf("URL = %q, want %q", got.URL, srv.URL)
	}
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2", got.Count)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("Servers len = %d, want 2", len(got.Servers))
	}
	if got.Servers[0].Name != "io.github.acme/example" || got.Servers[0].Version != "1.0.0" {
		t.Errorf("Servers[0] = %+v", got.Servers[0])
	}

	out := mustRenderText(t, res)
	if !strings.Contains(out, "2 server(s)") {
		t.Errorf("render missing count: %q", out)
	}
	if !strings.Contains(out, "io.github.acme/example") || !strings.Contains(out, "[1.0.0]") {
		t.Errorf("render missing server name/version: %q", out)
	}
}

// TestSourceRegistry_DefaultLimit confirms missing `limit` arg
// uses the canonical default. Catalog probe clamps limit
// internally so this doubles as a regression guard.
func TestSourceRegistry_DefaultLimit(t *testing.T) {
	var queryLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`{"servers":[]}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := runSourceRegistry(context.Background(), mkSourceRegistryReq(srv.URL, 0)); err != nil {
		t.Fatalf("runSourceRegistry: %v", err)
	}
	if queryLimit != "10" {
		t.Errorf("default limit query = %q, want 10", queryLimit)
	}
}

// TestSourceRegistry_HTTPError surfaces upstream 5xx as
// IsError() = true with a wrapped reason; banner uses the
// shared ErrorLine helper.
func TestSourceRegistry_HTTPError(t *testing.T) {
	srv := fakeRegistryServer(t, `{"detail":"down"}`, http.StatusInternalServerError)

	res, err := runSourceRegistry(context.Background(), mkSourceRegistryReq(srv.URL, 5))
	if err != nil {
		t.Fatalf("runSourceRegistry: %v", err)
	}
	got, ok := res.StructuredContent.(sourceRegistryResult)
	if !ok {
		t.Fatalf("StructuredContent = %T", res.StructuredContent)
	}
	if !got.IsError() {
		t.Error("expected IsError() = true on 5xx")
	}
	if !strings.Contains(got.ErrorReason, "500") {
		t.Errorf("ErrorReason should mention status; got %q", got.ErrorReason)
	}
}

// TestSourceRegistry_RegisteredInManifest pins the surface-drift
// guard: SourceRegistry MUST appear in the manifest with a non-
// nil Register hook + non-empty description / keywords. Sister
// of TestAgentDetect_RegisteredInManifest +
// TestSourceCheck_RegisteredInManifest.
func TestSourceRegistry_RegisteredInManifest(t *testing.T) {
	for _, s := range BuildManifest().Specs() {
		if s.Name != "SourceRegistry" {
			continue
		}
		if s.Register == nil {
			t.Error("SourceRegistry spec has nil Register")
		}
		if s.Gate != "" {
			t.Errorf("SourceRegistry gate = %q, want empty (always-on)", s.Gate)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Error("SourceRegistry spec has empty Description")
		}
		if len(s.Keywords) == 0 {
			t.Error("SourceRegistry spec has no Keywords")
		}
		return
	}
	t.Fatal("manifest missing SourceRegistry spec")
}
