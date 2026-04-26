package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper builds a minimal mux + auth wrapper for unit testing the
// handlers without booting the full MCP server. Each test gets its own
// token + httptest server so they're independent.
func newTestMux(token string) *http.ServeMux {
	mux := http.NewServeMux()
	authed := authMiddleware(token)
	mux.Handle("/v1/health", authed(http.HandlerFunc(handleHealth)))
	mux.Handle("/v1/agents", authed(http.HandlerFunc(handleAgents)))
	mux.Handle("/v1/send_message", authed(http.HandlerFunc(handleSendMessage)))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	})
	return mux
}

func TestAuth_RejectsMissingHeader(t *testing.T) {
	srv := httptest.NewServer(newTestMux("abc123"))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuth_RejectsWrongPrefix(t *testing.T) {
	srv := httptest.NewServer(newTestMux("abc123"))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/health", nil)
	req.Header.Set("Authorization", "Basic abc123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for non-bearer scheme; got %d", resp.StatusCode)
	}
}

func TestAuth_RejectsWrongToken(t *testing.T) {
	srv := httptest.NewServer(newTestMux("real-token"))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong token; got %d", resp.StatusCode)
	}
}

func TestAuth_AcceptsValidToken(t *testing.T) {
	srv := httptest.NewServer(newTestMux("real-token"))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer real-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200; got %d", resp.StatusCode)
	}
}

func TestHealth_ReturnsStatusAndVersion(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	body := getJSON(t, srv.URL+"/v1/health", "t")
	if body["status"] != "ok" {
		t.Errorf("status: got %v", body["status"])
	}
	if body["version"] == nil {
		t.Error("version field missing")
	}
}

func TestAgents_ReturnsRegistry(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	body := getJSON(t, srv.URL+"/v1/agents", "t")
	if body["agents"] == nil {
		t.Fatal("agents field missing")
	}
	// count must be int (json.Number when decoded into any → float64).
	count, ok := body["count"].(float64)
	if !ok {
		t.Fatalf("count not numeric; got %T", body["count"])
	}
	if int(count) < 0 {
		t.Errorf("count negative: %v", count)
	}
}

func TestAgents_StatusFilter(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	// status=callable should never error and should return a (possibly
	// empty) agents array.
	body := getJSON(t, srv.URL+"/v1/agents?status=callable", "t")
	if body["agents"] == nil {
		t.Fatal("agents field missing under filter")
	}
}

func TestSendMessage_RequiresPOST(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/send_message", nil)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405; got %d", resp.StatusCode)
	}
}

func TestSendMessage_RequiresPrompt(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/send_message",
		strings.NewReader(`{"instance":"claude"}`))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400; got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "prompt is required") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestSendMessage_UnknownInstanceErrors(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/send_message",
		strings.NewReader(`{"instance":"ghost-agent","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400; got %d", resp.StatusCode)
	}
}

func TestUnknownPath_404WithEndpointList(t *testing.T) {
	srv := httptest.NewServer(newTestMux("t"))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404; got %d", resp.StatusCode)
	}
}

func TestLoadToken_RejectsEmpty(t *testing.T) {
	if _, err := loadToken(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestLoadToken_RejectsMissingFile(t *testing.T) {
	if _, err := loadToken("/nonexistent/path/zzz"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadToken_RejectsEmptyContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadToken(path); err == nil {
		t.Error("expected error for empty token file")
	}
}

func TestLoadToken_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	if err := os.WriteFile(path, []byte("  abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := loadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "abc123" {
		t.Errorf("expected trimmed; got %q", tok)
	}
}

func TestInitTokenFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "listener-token")
	tok, err := InitTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 { // 32 bytes hex-encoded
		t.Errorf("token should be 64-char hex; got len=%d", len(tok))
	}
	gotTok, err := loadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotTok != tok {
		t.Error("init/load round-trip mismatch")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file should be 0600; got %v", info.Mode().Perm())
	}
}

func TestServeHTTP_RefusesEmptyListen(t *testing.T) {
	err := ServeHTTP(context.Background(), HTTPOptions{TokenFile: "anything"})
	if err == nil {
		t.Error("expected error for empty listen")
	}
}

func TestServeHTTP_RefusesEmptyTokenFile(t *testing.T) {
	err := ServeHTTP(context.Background(), HTTPOptions{Listen: ":0"})
	if err == nil {
		t.Error("expected error for empty token file")
	}
}

// recipe handlers: separate mux so we can hit them without booting
// the full MCP server, and so the token used here doesn't leak into
// other tests.
func newRecipeMux(token string) *http.ServeMux {
	mux := http.NewServeMux()
	authed := authMiddleware(token)
	mux.Handle("/v1/recipes", authed(http.HandlerFunc(handleRecipes)))
	mux.Handle("/v1/recipe/apply", authed(http.HandlerFunc(handleRecipeApply)))
	return mux
}

func TestRecipes_ListReturnsRows(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	body := getJSON(t, srv.URL+"/v1/recipes", "t")
	if body["recipes"] == nil {
		t.Fatal("recipes field missing")
	}
	if c, _ := body["count"].(float64); int(c) <= 0 {
		t.Errorf("count should be > 0; got %v", body["count"])
	}
}

func TestRecipes_FilterByCategory(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	body := getJSON(t, srv.URL+"/v1/recipes?category=agents", "t")
	if body["recipes"] == nil {
		t.Fatal("recipes field missing")
	}
}

func TestRecipes_RejectsUnknownCategory(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/recipes?category=nope", nil)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown category; got %d", resp.StatusCode)
	}
}

func TestRecipeApply_RequiresName(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/recipe/apply",
		strings.NewReader(`{"repo":"/tmp/x"}`))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400; got %d", resp.StatusCode)
	}
}

func TestRecipeApply_RequiresRepo(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/recipe/apply",
		strings.NewReader(`{"name":"license"}`))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400; got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "repo is required") {
		t.Errorf("body should mention repo: %s", body)
	}
}

func TestRecipeApply_UnknownNameErrors(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/v1/recipe/apply",
		strings.NewReader(`{"name":"ghost-recipe","repo":"/tmp/x"}`))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400; got %d", resp.StatusCode)
	}
}

func TestRecipeApply_HappyPath(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	body := strings.NewReader(`{"name":"conventional-commits-ci","repo":"` + dir + `"}`)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/recipe/apply", body)
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200; got %d (%s)", resp.StatusCode, raw)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if v, _ := got["verify_ok"].(bool); !v {
		t.Errorf("verify_ok should be true; got %v", got["verify_ok"])
	}
	// File must exist on disk after apply.
	if _, err := os.Stat(filepath.Join(dir, ".github/workflows/commit-format.yml")); err != nil {
		t.Errorf("recipe file not present after apply: %v", err)
	}
}

func TestRecipes_RequiresAuth(t *testing.T) {
	srv := httptest.NewServer(newRecipeMux("t"))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/recipes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 unauth; got %d", resp.StatusCode)
	}
}

// getJSON is a small helper for the auth-stamped read endpoints.
func getJSON(t *testing.T, url, token string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d (%s)", url, resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
