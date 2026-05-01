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

// TestTmuxSocketEnv asserts every tmux invocation prepends `-L
// <socket>` when CLAWTOOL_TMUX_SOCKET is set. Covers send-keys (the
// hot path), list-panes (liveness probe), and kill-pane (auto-close
// hook). Without this, the e2e Docker harness's `tmux -L claw-test`
// server is unreachable and the auto-close pane never fires.
func TestTmuxSocketEnv(t *testing.T) {
	t.Setenv("CLAWTOOL_TMUX_SOCKET", "claw-test")

	// send-keys — verify the 3-call sequence each carries -L claw-test.
	calls := withStubTmuxExec(t, func(args []string) string {
		if containsArg(args, "list-panes") {
			return "%1\n"
		}
		return ""
	})
	if err := tmuxSendKeys("%1", "ping"); err != nil {
		t.Fatalf("tmuxSendKeys: %v", err)
	}
	for i, c := range *calls {
		if !hasSocketFlag(c, "claw-test") {
			t.Errorf("send-keys call[%d] missing -L claw-test: %v", i, c)
		}
	}

	// list-panes (via tmuxPaneAlive) carries the same flag.
	*calls = nil
	if !tmuxPaneAlive("%1") {
		t.Errorf("tmuxPaneAlive returned false unexpectedly")
	}
	if len(*calls) != 1 || !hasSocketFlag((*calls)[0], "claw-test") {
		t.Errorf("list-panes call missing -L claw-test: %v", *calls)
	}

	// kill-pane carries the same flag.
	*calls = nil
	if err := tmuxKillPane("%42"); err != nil {
		t.Fatalf("tmuxKillPane: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 kill-pane call, got %d: %v", len(*calls), *calls)
	}
	if !hasSocketFlag((*calls)[0], "claw-test") {
		t.Errorf("kill-pane call missing -L claw-test: %v", (*calls)[0])
	}
	wantKill := []string{"tmux", "-L", "claw-test", "kill-pane", "-t", "%42"}
	if !equalSlice((*calls)[0], wantKill) {
		t.Errorf("kill-pane argv = %v, want %v", (*calls)[0], wantKill)
	}

	// Sanity: with the env unset, no -L flag appears.
	t.Setenv("CLAWTOOL_TMUX_SOCKET", "")
	*calls = nil
	if err := tmuxKillPane("%7"); err != nil {
		t.Fatalf("tmuxKillPane (no env): %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 kill-pane call (no env), got %d", len(*calls))
	}
	for _, a := range (*calls)[0] {
		if a == "-L" {
			t.Errorf("CLAWTOOL_TMUX_SOCKET unset but -L flag leaked: %v", (*calls)[0])
		}
	}
}

// TestKillTmuxPaneAndMaybeWindow_KillsBothWhenWindowEmpty asserts the
// Q1 contract end-to-end at the cli adapter layer: kill-pane fires
// AND list-panes returns no remaining panes → kill-window fires too.
func TestKillTmuxPaneAndMaybeWindow_KillsBothWhenWindowEmpty(t *testing.T) {
	calls := withStubTmuxExec(t, func(args []string) string {
		// list-panes is the empty-window probe — return empty
		// stdout so tmuxWindowEmpty reports "no panes left".
		if containsArg(args, "list-panes") {
			return ""
		}
		return ""
	})
	if err := KillTmuxPaneAndMaybeWindow("%42", "@7"); err != nil {
		t.Fatalf("KillTmuxPaneAndMaybeWindow: %v", err)
	}
	// Expect: kill-pane %42, list-panes -t @7, kill-window -t @7.
	if got := len(*calls); got != 3 {
		t.Fatalf("expected 3 tmux calls, got %d: %v", got, *calls)
	}
	if !equalSlice((*calls)[0], []string{"tmux", "kill-pane", "-t", "%42"}) {
		t.Errorf("call[0] = %v", (*calls)[0])
	}
	// The probe + kill must target the window.
	if !containsArg((*calls)[1], "@7") || !containsArg((*calls)[1], "list-panes") {
		t.Errorf("call[1] missing list-panes -t @7: %v", (*calls)[1])
	}
	if !equalSlice((*calls)[2], []string{"tmux", "kill-window", "-t", "@7"}) {
		t.Errorf("call[2] = %v, want kill-window -t @7", (*calls)[2])
	}
}

// TestKillTmuxPaneAndMaybeWindow_KeepsWindowWhenOtherPanesAlive
// asserts list-panes returning more than zero panes short-circuits
// the kill-window step. The pane is still killed.
func TestKillTmuxPaneAndMaybeWindow_KeepsWindowWhenOtherPanesAlive(t *testing.T) {
	calls := withStubTmuxExec(t, func(args []string) string {
		if containsArg(args, "list-panes") {
			// Two panes remaining — window is NOT empty.
			return "%50\n%51\n"
		}
		return ""
	})
	if err := KillTmuxPaneAndMaybeWindow("%50", "@9"); err != nil {
		t.Fatalf("KillTmuxPaneAndMaybeWindow: %v", err)
	}
	// Expect kill-pane + list-panes; NO kill-window.
	if got := len(*calls); got != 2 {
		t.Fatalf("expected 2 tmux calls, got %d: %v", got, *calls)
	}
	for _, c := range *calls {
		if containsArg(c, "kill-window") {
			t.Errorf("kill-window leaked despite remaining panes: %v", c)
		}
	}
}

// TestKillTmuxPaneAndMaybeWindow_EmptyWindowIDSkipsCleanup asserts
// the legacy fallback: callers that pass windowID="" (peer
// registered before the spawner started recording window_id) get
// pane-only behaviour. No list-panes probe, no kill-window.
func TestKillTmuxPaneAndMaybeWindow_EmptyWindowIDSkipsCleanup(t *testing.T) {
	calls := withStubTmuxExec(t, nil)
	if err := KillTmuxPaneAndMaybeWindow("%60", ""); err != nil {
		t.Fatalf("KillTmuxPaneAndMaybeWindow: %v", err)
	}
	if got := len(*calls); got != 1 {
		t.Fatalf("expected 1 tmux call (kill-pane only), got %d: %v", got, *calls)
	}
	if !equalSlice((*calls)[0], []string{"tmux", "kill-pane", "-t", "%60"}) {
		t.Errorf("call[0] = %v", (*calls)[0])
	}
}

// containsArg reports whether argv contains the literal arg.
func containsArg(argv []string, arg string) bool {
	for _, a := range argv {
		if a == arg {
			return true
		}
	}
	return false
}

// hasSocketFlag returns true when argv contains a `-L <socket>`
// adjacent pair. Order-sensitive: the flag must precede the
// subcommand so tmux interprets it as a server-selection flag rather
// than a subcommand argument.
func hasSocketFlag(argv []string, socket string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-L" && argv[i+1] == socket {
			return true
		}
	}
	return false
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
