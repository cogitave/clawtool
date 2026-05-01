package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
)

// withStubTmuxExec rebinds tmuxRunArgv + tmuxOutputArgv to a
// shared in-process recorder. No fork/exec happens, so the suite
// is portable across Linux and macOS (the previous stub shelled
// out to /bin/true, which doesn't exist on Darwin and broke
// macos-latest CI). stdoutFor — when non-nil — supplies fake
// stdout for tmuxPaneAlive's list-panes call. Cleanup restores
// the production bindings.
func withStubTmuxExec(t *testing.T, stdoutFor func(args []string) string) *[][]string {
	t.Helper()
	var (
		mu    sync.Mutex
		calls [][]string
	)
	record := func(name string, args ...string) []string {
		full := append([]string{name}, args...)
		mu.Lock()
		calls = append(calls, append([]string(nil), full...))
		mu.Unlock()
		return full
	}
	prevRun, prevOut := tmuxRunArgv, tmuxOutputArgv
	tmuxRunArgv = func(name string, args ...string) error {
		record(name, args...)
		return nil
	}
	tmuxOutputArgv = func(name string, args ...string) ([]byte, error) {
		full := record(name, args...)
		if stdoutFor != nil {
			return []byte(stdoutFor(full)), nil
		}
		return nil, nil
	}
	t.Cleanup(func() {
		tmuxRunArgv = prevRun
		tmuxOutputArgv = prevOut
	})
	return &calls
}

// TestTmuxSendKeys_StubExec asserts the 3-call sequence (text →
// Escape → Enter) mirroring repowire's _tmux_send_keys.
func TestTmuxSendKeys_StubExec(t *testing.T) {
	calls := withStubTmuxExec(t, nil)
	if err := tmuxSendKeys("%1", "merhaba"); err != nil {
		t.Fatalf("tmuxSendKeys: %v", err)
	}
	want := [][]string{
		{"tmux", "send-keys", "-t", "%1", "-l", "merhaba"},
		{"tmux", "send-keys", "-t", "%1", "Escape"},
		{"tmux", "send-keys", "-t", "%1", "Enter"},
	}
	if len(*calls) != 3 {
		t.Fatalf("calls=%d, want 3\n%v", len(*calls), *calls)
	}
	for i, w := range want {
		if !equalSlice((*calls)[i], w) {
			t.Errorf("call[%d] = %v, want %v", i, (*calls)[i], w)
		}
	}
	// Malformed pane id must be rejected before tmux is invoked.
	*calls = nil
	for _, bad := range []string{"", "%", "$(rm -rf)", "%1; echo pwn", "abc"} {
		if err := tmuxSendKeys(bad, "x"); err == nil {
			t.Errorf("tmuxSendKeys(%q) should have failed", bad)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("malformed pane ids leaked into tmux: %v", *calls)
	}
}

// TestTmuxPaneAlive_True — list-panes output contains the queried
// pane id → returns true.
func TestTmuxPaneAlive_True(t *testing.T) {
	withStubTmuxExec(t, func(args []string) string {
		if len(args) >= 2 && args[1] == "list-panes" {
			return "%0\n%1\n%2\n"
		}
		return ""
	})
	if !tmuxPaneAlive("%1") {
		t.Errorf("tmuxPaneAlive(%%1) = false, want true")
	}
}

// TestTmuxPaneAlive_False — pane absent from list output → false.
// Models the recipient-session-crashed recovery path.
func TestTmuxPaneAlive_False(t *testing.T) {
	withStubTmuxExec(t, func(args []string) string {
		if len(args) >= 2 && args[1] == "list-panes" {
			return "%0\n%2\n"
		}
		return ""
	})
	if tmuxPaneAlive("%1") {
		t.Errorf("tmuxPaneAlive(%%1) = true, want false")
	}
}

// TestPeerSend_PushesToTmuxWhenPanePresent — daemon answers
// /v1/peers/<id> with TmuxPane=%1; runPeerSend posts to the
// inbox AND fires tmux send-keys 3-step at %1.
func TestPeerSend_PushesToTmuxWhenPanePresent(t *testing.T) {
	const peerID = "peer-target"
	now := time.Now().UTC()
	var postSeen, getSeen bool
	stubDaemonForPeers(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/peers/"+peerID+"/messages":
			postSeen = true
			_ = json.NewEncoder(w).Encode(a2a.Message{ID: "m-saved", Text: "merhaba", Timestamp: now})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/peers/"+peerID:
			getSeen = true
			_ = json.NewEncoder(w).Encode(a2a.Peer{
				PeerID: peerID, DisplayName: "claude-fresh", TmuxPane: "%1", LastSeen: now,
			})
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
	})
	tmuxCalls := withStubTmuxExec(t, func(args []string) string {
		if len(args) >= 2 && args[1] == "list-panes" {
			return "%1\n"
		}
		return ""
	})
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	if rc := app.Run([]string{"peer", "send", peerID, "merhaba"}); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	if !postSeen {
		t.Errorf("inbox POST never landed (canonical delivery missing)")
	}
	if !getSeen {
		t.Errorf("tmux_pane GET never landed")
	}
	// 1 list-panes + 3 send-keys.
	if got := len(*tmuxCalls); got != 4 {
		t.Errorf("tmux calls=%d, want 4\n%v", got, *tmuxCalls)
	}
	if !strings.Contains(errb.String(), "→ pushed to tmux pane %1") {
		t.Errorf("stderr missing tmux-push notice; got %q", errb.String())
	}
}

// TestPeerSend_NoPushWhenPaneAbsent — recipient has no tmux_pane
// → zero tmux calls, inbox POST still fires. Subtests verify
// both the natural no-pane case and the --no-push escape hatch
// (the flag must skip the /v1/peers/<id> lookup AND tmux entirely
// even when the peer DOES have a pane).
func TestPeerSend_NoPushWhenPaneAbsent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		argv      []string
		hasPane   bool
		wantHits  bool // /v1/peers/<id> GET expected?
		peerIDArg string
	}{
		{name: "no_pane_registered", argv: []string{"peer", "send", "peer-x", "hello"}, hasPane: false, wantHits: true, peerIDArg: "peer-x"},
		{name: "no_push_flag_with_pane", argv: []string{"peer", "send", "--no-push", "peer-y", "hello"}, hasPane: true, wantHits: false, peerIDArg: "peer-y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			var getSeen bool
			stubDaemonForPeers(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/peers/"+tc.peerIDArg+"/messages":
					_ = json.NewEncoder(w).Encode(a2a.Message{ID: "m1", Text: "x", Timestamp: now})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/peers/"+tc.peerIDArg:
					getSeen = true
					p := a2a.Peer{PeerID: tc.peerIDArg, DisplayName: "x"}
					if tc.hasPane {
						p.TmuxPane = "%1"
					}
					_ = json.NewEncoder(w).Encode(p)
				default:
					t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
				}
			})
			tmuxCalls := withStubTmuxExec(t, nil)
			out, errb := &bytes.Buffer{}, &bytes.Buffer{}
			app := &App{Stdout: out, Stderr: errb}
			if rc := app.Run(tc.argv); rc != 0 {
				t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
			}
			if len(*tmuxCalls) != 0 {
				t.Errorf("expected zero tmux calls; got %v", *tmuxCalls)
			}
			if strings.Contains(errb.String(), "pushed to tmux") {
				t.Errorf("stderr advertised a push that did not happen: %q", errb.String())
			}
			if tc.wantHits != getSeen {
				t.Errorf("/v1/peers/<id> GET seen=%v, want %v", getSeen, tc.wantHits)
			}
		})
	}
}

// TestPeerPush_Broadcast — push --broadcast resolves /v1/peers
// and fires send-keys at every tmux-aware peer. NO inbox POSTs.
func TestPeerPush_Broadcast(t *testing.T) {
	now := time.Now().UTC()
	peers := []a2a.Peer{
		{PeerID: "p1", DisplayName: "alice", TmuxPane: "%1"},
		{PeerID: "p2", DisplayName: "bob"}, // no pane — skipped
		{PeerID: "p3", DisplayName: "carol", TmuxPane: "%3"},
	}
	stubDaemonForPeers(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/peers":
			_ = json.NewEncoder(w).Encode(map[string]any{"peers": peers, "count": len(peers), "as_of": now})
		case r.Method == http.MethodPost:
			t.Errorf("peer push must NOT POST (got %s %s)", r.Method, r.URL.Path)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
	})
	tmuxCalls := withStubTmuxExec(t, func(args []string) string {
		if len(args) >= 2 && args[1] == "list-panes" {
			return "%1\n%3\n"
		}
		return ""
	})
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	if rc := app.Run([]string{"peer", "push", "--broadcast", "merhaba"}); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	// 2 list-panes + (3 send-keys × 2 panes) = 8 calls.
	if got := len(*tmuxCalls); got != 8 {
		t.Fatalf("tmux calls=%d, want 8\n%v", got, *tmuxCalls)
	}
	saw := map[string]int{"%1": 0, "%3": 0}
	for _, c := range *tmuxCalls {
		for i, a := range c {
			if a == "-t" && i+1 < len(c) {
				if _, ok := saw[c[i+1]]; ok {
					saw[c[i+1]]++
				}
			}
		}
	}
	if saw["%1"] == 0 || saw["%3"] == 0 {
		t.Errorf("expected both %%1 and %%3 to receive send-keys; got %v", saw)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
