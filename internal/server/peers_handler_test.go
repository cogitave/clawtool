package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/a2a"
)

// newPeersTestMux mounts /v1/peers + /v1/peers/ on a fresh registry.
// Returns the mux, the registry (so the test can pre-seed peers
// without a network round-trip), and a clean-up func that resets
// the global registry slot — important because a2a.SetGlobal is
// process-scoped and tests run sequentially against the same slot.
func newPeersTestMux(t *testing.T, token string) (*http.ServeMux, *a2a.Registry, func()) {
	t.Helper()
	prev := a2a.GetGlobal()
	reg := a2a.NewRegistry(filepath.Join(t.TempDir(), "peers.json"))
	a2a.SetGlobal(reg)
	mux := http.NewServeMux()
	authed := authMiddleware(token)
	mux.Handle("/v1/peers", authed(http.HandlerFunc(handlePeers)))
	mux.Handle("/v1/peers/", authed(http.HandlerFunc(handlePeers)))
	cleanup := func() { a2a.SetGlobal(prev) }
	return mux, reg, cleanup
}

func peersDo(t *testing.T, srv *httptest.Server, method, path, token string, body []byte) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func TestPeers_503WhenRegistryNotInstalled(t *testing.T) {
	prev := a2a.GetGlobal()
	a2a.SetGlobal(nil)
	defer a2a.SetGlobal(prev)

	mux := http.NewServeMux()
	authed := authMiddleware("tok")
	mux.Handle("/v1/peers", authed(http.HandlerFunc(handlePeers)))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := peersDo(t, srv, http.MethodGet, "/v1/peers", "tok", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

func TestPeers_RegisterThenList(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(a2a.RegisterInput{
		DisplayName: "claude-laptop",
		Backend:     "claude-code",
		Path:        t.TempDir(),
	})
	resp, out := peersDo(t, srv, http.MethodPost, "/v1/peers/register", "tok", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status=%d body=%s", resp.StatusCode, out)
	}
	var peer a2a.Peer
	if err := json.Unmarshal(out, &peer); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	if peer.PeerID == "" {
		t.Fatal("expected non-empty peer_id")
	}

	resp, out = peersDo(t, srv, http.MethodGet, "/v1/peers", "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.StatusCode, out)
	}
	var listed struct {
		Peers []a2a.Peer `json:"peers"`
		Count int        `json:"count"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.Count != 1 || listed.Peers[0].PeerID != peer.PeerID {
		t.Errorf("list mismatch: %+v", listed)
	}
}

func TestPeers_Register_RejectsBadJSON(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := peersDo(t, srv, http.MethodPost, "/v1/peers/register", "tok", []byte("{not json"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestPeers_Register_RejectsMissingFields(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	body, _ := json.Marshal(a2a.RegisterInput{Backend: "claude-code"})
	resp, _ := peersDo(t, srv, http.MethodPost, "/v1/peers/register", "tok", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing display_name should 400, got %d", resp.StatusCode)
	}
}

func TestPeers_HeartbeatRefreshesPeer(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p, err := reg.Register(a2a.RegisterInput{
		DisplayName: "pre-seeded", Backend: "codex", Path: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"status": "busy"})
	resp, out := peersDo(t, srv, http.MethodPost, "/v1/peers/"+p.PeerID+"/heartbeat", "tok", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", resp.StatusCode, out)
	}
	var got a2a.Peer
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != a2a.PeerBusy {
		t.Errorf("status=%q, want busy", got.Status)
	}
}

func TestPeers_Heartbeat_404UnknownID(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := peersDo(t, srv, http.MethodPost, "/v1/peers/does-not-exist/heartbeat", "tok", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}

func TestPeers_DeregisterRemovesPeer(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p, _ := reg.Register(a2a.RegisterInput{
		DisplayName: "doomed", Backend: "claude-code", Path: t.TempDir(),
	})
	resp, _ := peersDo(t, srv, http.MethodDelete, "/v1/peers/"+p.PeerID, "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deregister status=%d", resp.StatusCode)
	}
	if reg.Get(p.PeerID) != nil {
		t.Error("peer still present after deregister")
	}
}

func TestPeers_Get_FindsByID(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p, _ := reg.Register(a2a.RegisterInput{
		DisplayName: "findable", Backend: "gemini", Path: t.TempDir(),
	})
	resp, out := peersDo(t, srv, http.MethodGet, "/v1/peers/"+p.PeerID, "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status=%d body=%s", resp.StatusCode, out)
	}
	var got a2a.Peer
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PeerID != p.PeerID {
		t.Errorf("peer_id mismatch: got %q want %q", got.PeerID, p.PeerID)
	}
}

func TestPeers_List_FilterByBackend(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir1, dir2 := t.TempDir(), t.TempDir()
	reg.Register(a2a.RegisterInput{DisplayName: "a", Backend: "claude-code", Path: dir1})
	reg.Register(a2a.RegisterInput{DisplayName: "b", Backend: "codex", Path: dir2})

	resp, out := peersDo(t, srv, http.MethodGet, "/v1/peers?backend=codex", "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var listed struct {
		Peers []a2a.Peer `json:"peers"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Peers) != 1 || listed.Peers[0].DisplayName != "b" {
		t.Errorf("filter mismatch: %+v", listed.Peers)
	}
}

func TestPeers_RejectsBadMethod(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// PATCH on /v1/peers — no handler.
	resp, _ := peersDo(t, srv, http.MethodPatch, "/v1/peers", "tok", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", resp.StatusCode)
	}
}

func TestPeers_RequiresAuth(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := peersDo(t, srv, http.MethodGet, "/v1/peers", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}
}
