package setuptools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/mark3labs/mcp-go/mcp"
)

// mkPeerListReq fabricates an MCP CallToolRequest for the
// PeerList tool with the (optional) circle / backend / status
// filters. Mirrors mkOnboardStatusReq in onboard_status_test.go.
func mkPeerListReq(filters map[string]string) mcp.CallToolRequest {
	args := map[string]any{}
	for k, v := range filters {
		if v != "" {
			args[k] = v
		}
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "PeerList",
			Arguments: args,
		},
	}
}

// withStubbedHTTP swaps the package-level peerListHTTP with `fn`
// for the duration of one test, restoring it on Cleanup. The
// indirection keeps these tests isolated from the daemon (no real
// listener required) and matches the same stub-the-package-var
// pattern source_registry_test.go uses for probeMCP / probeSmithery.
func withStubbedHTTP(t *testing.T, fn func(method, path string, body *bytes.Reader, out any) error) {
	t.Helper()
	orig := peerListHTTP
	peerListHTTP = fn
	t.Cleanup(func() { peerListHTTP = orig })
}

// TestPeerList_Empty — daemon returns zero peers; tool surfaces
// the same shape with Count=0 and no error.
func TestPeerList_Empty(t *testing.T) {
	calls := 0
	var seenPath string
	withStubbedHTTP(t, func(method, path string, _ *bytes.Reader, out any) error {
		calls++
		seenPath = path
		if method != http.MethodGet {
			t.Errorf("method=%q, want GET", method)
		}
		ptr, ok := out.(*peerListResult)
		if !ok {
			t.Fatalf("out is %T, want *peerListResult", out)
		}
		ptr.Peers = []a2a.Peer{}
		ptr.Count = 0
		ptr.AsOf = time.Now().UTC()
		return nil
	})

	res, err := runPeerList(context.Background(), mkPeerListReq(nil))
	if err != nil {
		t.Fatalf("runPeerList: %v", err)
	}
	if calls != 1 {
		t.Errorf("daemon stub calls=%d, want 1", calls)
	}
	if seenPath != "/v1/peers" {
		t.Errorf("path=%q, want /v1/peers (no filters)", seenPath)
	}
	got, ok := res.StructuredContent.(peerListResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want peerListResult", res.StructuredContent)
	}
	if got.Count != 0 || len(got.Peers) != 0 {
		t.Errorf("expected empty result, got %+v", got)
	}
}

// TestPeerList_ThreePeers — fixture returns three peers across
// two circles + three backends; PeerList passes them through
// verbatim (filtering is the daemon's job, not the tool's).
// Also exercises the --circle filter forwarding by asserting the
// query string the daemon stub sees.
func TestPeerList_ThreePeers(t *testing.T) {
	now := time.Now().UTC()
	master := []a2a.Peer{
		{
			PeerID: "peer-aaa", DisplayName: "alice@host/claude-code",
			Backend: "claude-code", Circle: "default",
			Role: a2a.RoleAgent, Status: a2a.PeerStatus("online"),
			LastSeen: now.Add(-5 * time.Second),
		},
		{
			PeerID: "peer-bbb", DisplayName: "bob@host/codex",
			Backend: "codex", Circle: "ml",
			Role: a2a.RoleAgent, Status: a2a.PeerStatus("busy"),
			LastSeen: now.Add(-30 * time.Second),
		},
		{
			PeerID: "peer-ccc", DisplayName: "carol@host/gemini",
			Backend: "gemini", Circle: "default",
			Role: a2a.RoleOrchestrator, Status: a2a.PeerStatus("offline"),
			LastSeen: now.Add(-10 * time.Minute),
		},
	}

	var seenPath string
	withStubbedHTTP(t, func(_ string, path string, _ *bytes.Reader, out any) error {
		seenPath = path
		ptr := out.(*peerListResult)
		// New slice each call so a `circle=ml` invocation
		// doesn't alias the master across calls.
		filtered := make([]a2a.Peer, 0, len(master))
		// Crude path-based filter — handler is mocking the
		// daemon's behavior, not exercising real query parsing.
		wantCircle := ""
		if i := strings.Index(path, "circle="); i >= 0 {
			rest := path[i+len("circle="):]
			if amp := strings.Index(rest, "&"); amp >= 0 {
				wantCircle = rest[:amp]
			} else {
				wantCircle = rest
			}
		}
		for _, p := range master {
			if wantCircle == "" || p.Circle == wantCircle {
				filtered = append(filtered, p)
			}
		}
		ptr.Peers = filtered
		ptr.Count = len(filtered)
		ptr.AsOf = now
		return nil
	})

	// ── No filter: all three ────────────────────────────────────
	res, err := runPeerList(context.Background(), mkPeerListReq(nil))
	if err != nil {
		t.Fatalf("runPeerList no-filter: %v", err)
	}
	got := res.StructuredContent.(peerListResult)
	if got.Count != 3 || len(got.Peers) != 3 {
		t.Fatalf("Count=%d len=%d, want 3/3; peers=%+v", got.Count, len(got.Peers), got.Peers)
	}
	wantIDs := map[string]bool{"peer-aaa": true, "peer-bbb": true, "peer-ccc": true}
	for _, p := range got.Peers {
		if !wantIDs[p.PeerID] {
			t.Errorf("unexpected peer_id %q in result", p.PeerID)
		}
	}
	if seenPath != "/v1/peers" {
		t.Errorf("no-filter path=%q, want /v1/peers", seenPath)
	}

	// ── --circle ml: only peer-bbb ─────────────────────────────
	res, err = runPeerList(context.Background(), mkPeerListReq(map[string]string{"circle": "ml"}))
	if err != nil {
		t.Fatalf("runPeerList circle=ml: %v", err)
	}
	got = res.StructuredContent.(peerListResult)
	if got.Count != 1 || got.Peers[0].PeerID != "peer-bbb" {
		t.Errorf("circle=ml: got %+v, want only peer-bbb", got)
	}
	if !strings.Contains(seenPath, "circle=ml") {
		t.Errorf("expected circle=ml in path; got %q", seenPath)
	}

	// ── backend filter forwarded ───────────────────────────────
	_, err = runPeerList(context.Background(), mkPeerListReq(map[string]string{"backend": "codex"}))
	if err != nil {
		t.Fatalf("runPeerList backend=codex: %v", err)
	}
	if !strings.Contains(seenPath, "backend=codex") {
		t.Errorf("expected backend=codex in path; got %q", seenPath)
	}
}

// TestPeerList_DaemonError — the http stub returns an error;
// the tool surfaces it as a structured tool-error result (not a
// Go error) so the calling MCP client renders the message.
func TestPeerList_DaemonError(t *testing.T) {
	withStubbedHTTP(t, func(_, _ string, _ *bytes.Reader, _ any) error {
		return errors.New("dial daemon: connection refused")
	})

	res, err := runPeerList(context.Background(), mkPeerListReq(nil))
	if err != nil {
		t.Fatalf("runPeerList returned Go err: %v (want nil; errors surface as IsError result)", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on daemon-down; got %+v", res)
	}
	// The MCP client sees the plain text in the result content.
	if !strings.Contains(fmt.Sprintf("%v", res.Content), "PeerList") {
		t.Errorf("expected PeerList prefix in error content; got %+v", res.Content)
	}
}
