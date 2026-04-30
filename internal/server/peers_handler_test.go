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
	cleanup := func() {
		// Drain in-flight SaveAsync goroutines before letting
		// t.TempDir's RemoveAll run. macOS's stricter unlinkat
		// rejects the temp dir with "directory not empty" if a
		// pending atomicfile.WriteFileMkdir hasn't finished yet.
		reg.WaitForSaves()
		a2a.SetGlobal(prev)
	}
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

// --- Inbox / messaging ---------------------------------------------

func TestInbox_SendThenDrain(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	recipient, _ := reg.Register(a2a.RegisterInput{
		DisplayName: "B", Backend: "claude-code", Path: t.TempDir(),
	})
	body, _ := json.Marshal(a2a.Message{Text: "hi", FromPeer: "sender-id"})
	resp, out := peersDo(t, srv, http.MethodPost, "/v1/peers/"+recipient.PeerID+"/messages", "tok", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send status=%d body=%s", resp.StatusCode, out)
	}
	resp, out = peersDo(t, srv, http.MethodGet, "/v1/peers/"+recipient.PeerID+"/messages", "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drain status=%d body=%s", resp.StatusCode, out)
	}
	var got struct {
		Messages []a2a.Message `json:"messages"`
		Count    int           `json:"count"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != 1 || got.Messages[0].Text != "hi" {
		t.Errorf("unexpected drain: %+v", got)
	}
	// Second drain must be empty (we consumed it).
	resp, out = peersDo(t, srv, http.MethodGet, "/v1/peers/"+recipient.PeerID+"/messages", "tok", nil)
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != 0 {
		t.Errorf("second drain non-empty: %+v", got)
	}
}

func TestInbox_PeekKeepsMessages(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p, _ := reg.Register(a2a.RegisterInput{DisplayName: "p", Backend: "claude-code", Path: t.TempDir()})
	body, _ := json.Marshal(a2a.Message{Text: "still here"})
	peersDo(t, srv, http.MethodPost, "/v1/peers/"+p.PeerID+"/messages", "tok", body)
	// peek=1
	resp, out := peersDo(t, srv, http.MethodGet, "/v1/peers/"+p.PeerID+"/messages?peek=1", "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peek status=%d", resp.StatusCode)
	}
	var got struct{ Count int }
	json.Unmarshal(out, &got)
	if got.Count != 1 {
		t.Errorf("peek count=%d, want 1", got.Count)
	}
	// real drain still finds it
	_, out = peersDo(t, srv, http.MethodGet, "/v1/peers/"+p.PeerID+"/messages", "tok", nil)
	json.Unmarshal(out, &got)
	if got.Count != 1 {
		t.Errorf("post-peek drain count=%d, want 1", got.Count)
	}
}

func TestInbox_404UnknownRecipient(t *testing.T) {
	mux, _, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	body, _ := json.Marshal(a2a.Message{Text: "ghost"})
	resp, _ := peersDo(t, srv, http.MethodPost, "/v1/peers/nope/messages", "tok", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d, want 404", resp.StatusCode)
	}
}

func TestInbox_RejectsEmptyText(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p, _ := reg.Register(a2a.RegisterInput{DisplayName: "x", Backend: "claude-code", Path: t.TempDir()})
	body, _ := json.Marshal(a2a.Message{Text: "   "})
	resp, _ := peersDo(t, srv, http.MethodPost, "/v1/peers/"+p.PeerID+"/messages", "tok", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty text status=%d, want 400", resp.StatusCode)
	}
}

func TestInbox_BroadcastSkipsSender(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, _ := reg.Register(a2a.RegisterInput{DisplayName: "a", Backend: "claude-code", Path: t.TempDir()})
	b, _ := reg.Register(a2a.RegisterInput{DisplayName: "b", Backend: "claude-code", Path: t.TempDir()})
	c, _ := reg.Register(a2a.RegisterInput{DisplayName: "c", Backend: "codex", Path: t.TempDir()})

	body, _ := json.Marshal(a2a.Message{Text: "all hands", FromPeer: a.PeerID})
	resp, out := peersDo(t, srv, http.MethodPost, "/v1/peers/broadcast", "tok", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("broadcast status=%d body=%s", resp.StatusCode, out)
	}
	var bx struct {
		DeliveredTo int `json:"delivered_to"`
	}
	json.Unmarshal(out, &bx)
	if bx.DeliveredTo != 2 {
		t.Errorf("delivered_to=%d, want 2 (b + c, NOT a)", bx.DeliveredTo)
	}
	// Sender's own inbox stays empty.
	if reg.DrainInbox(a.PeerID, true /* peek */); reg.DrainInbox(a.PeerID, true) != nil && len(reg.DrainInbox(a.PeerID, true)) != 0 {
		t.Errorf("sender's inbox should not receive its own broadcast")
	}
	// Both other peers got it.
	if got := reg.DrainInbox(b.PeerID, false); len(got) != 1 || got[0].Text != "all hands" {
		t.Errorf("b inbox = %+v", got)
	}
	if got := reg.DrainInbox(c.PeerID, false); len(got) != 1 || got[0].Text != "all hands" {
		t.Errorf("c inbox = %+v", got)
	}
}

func TestInbox_DeregisterClearsInbox(t *testing.T) {
	mux, reg, cleanup := newPeersTestMux(t, "tok")
	defer cleanup()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p, _ := reg.Register(a2a.RegisterInput{DisplayName: "p", Backend: "claude-code", Path: t.TempDir()})
	body, _ := json.Marshal(a2a.Message{Text: "doomed"})
	peersDo(t, srv, http.MethodPost, "/v1/peers/"+p.PeerID+"/messages", "tok", body)
	if got := reg.DrainInbox(p.PeerID, true); len(got) != 1 {
		t.Fatalf("pre-deregister peek count=%d, want 1", len(got))
	}
	peersDo(t, srv, http.MethodDelete, "/v1/peers/"+p.PeerID, "tok", nil)
	if got := reg.DrainInbox(p.PeerID, true); len(got) != 0 {
		t.Errorf("inbox not cleared on deregister: %+v", got)
	}
}
