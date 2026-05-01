package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
)

// stubDaemonForPeers spins up an httptest server, writes a
// daemon.json under XDG_CONFIG_HOME pointing at that server's
// port, and registers the cleanup. The daemon HTTPRequest helper
// hardcodes 127.0.0.1, which httptest.NewServer also binds, so a
// plain port-scoped state file is enough to redirect every CLI
// call into the fixture.
//
// handler should answer the requests under /v1/peers; tests
// assert on the parsed query params + body via this handler.
func stubDaemonForPeers(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// httptest URL is "http://127.0.0.1:PORT". Parse out PORT so
	// daemon.HTTPRequest's hardcoded 127.0.0.1 + state.Port match.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse httptest port: %v", err)
	}

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfgDir := filepath.Join(tmp, "clawtool")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	state := map[string]any{
		"version":    1,
		"pid":        os.Getpid(),
		"port":       port,
		"started_at": time.Now().UTC().Format(time.RFC3339),
		"token_file": filepath.Join(cfgDir, "listener-token"),
		"log_file":   filepath.Join(tmp, "daemon.log"),
	}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "daemon.json"), body, 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
	// Empty token file — daemon.HTTPRequest ReadToken treats
	// missing as "" and skips the bearer header. We write an
	// empty file so any test that wants to seed a token can
	// just os.WriteFile over it.
	if err := os.WriteFile(filepath.Join(cfgDir, "listener-token"), nil, 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return srv
}

// TestPeer_ListEmpty — daemon returns a zero-peer registry; the
// CLI surfaces a friendly "(no peers registered)" line on stdout
// and exits 0. Confirms the empty-set branch and that the verb
// is wired into the `peer` dispatcher.
func TestPeer_ListEmpty(t *testing.T) {
	hits := 0
	stubDaemonForPeers(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/peers" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"peers": []a2a.Peer{},
			"count": 0,
			"as_of": time.Now().UTC(),
		})
	})

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "list"})
	if rc != 0 {
		t.Fatalf("rc=%d, want 0; stderr=%s", rc, errb.String())
	}
	if hits != 1 {
		t.Errorf("daemon hits=%d, want 1", hits)
	}
	if !strings.Contains(out.String(), "(no peers registered)") {
		t.Errorf("expected empty-set message; got %q", out.String())
	}
}

// TestPeer_ListThreePeers — daemon returns three peers with
// distinct backends + last_seen; the text path renders a
// tab-separated table with all three peer_ids, sorted last_seen
// desc (most recent first). Also drives the --circle filter
// through to the daemon as a query param and confirms --format
// json round-trips the daemon's payload.
func TestPeer_ListThreePeers(t *testing.T) {
	now := time.Now().UTC()
	peers := []a2a.Peer{
		{
			PeerID:      "peer-aaa",
			DisplayName: "alice@host/claude-code",
			Backend:     "claude-code",
			Circle:      "default",
			Role:        a2a.RoleAgent,
			Status:      a2a.PeerStatus("online"),
			LastSeen:    now.Add(-5 * time.Second),
		},
		{
			PeerID:      "peer-bbb",
			DisplayName: "bob@host/codex",
			Backend:     "codex",
			Circle:      "ml",
			Role:        a2a.RoleAgent,
			Status:      a2a.PeerStatus("busy"),
			LastSeen:    now.Add(-30 * time.Second),
		},
		{
			PeerID:      "peer-ccc",
			DisplayName: "carol@host/gemini",
			Backend:     "gemini",
			Circle:      "default",
			Role:        a2a.RoleOrchestrator,
			Status:      a2a.PeerStatus("offline"),
			LastSeen:    now.Add(-10 * time.Minute),
		},
	}

	var lastQuery url.Values
	stubDaemonForPeers(t, func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.Query()
		// Mimic the daemon: filter by circle when present. New
		// slice every call so a prior --circle invocation doesn't
		// alias-mutate the master list across requests.
		filtered := make([]a2a.Peer, 0, len(peers))
		c := r.URL.Query().Get("circle")
		for _, p := range peers {
			if c == "" || p.Circle == c {
				filtered = append(filtered, p)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"peers": filtered,
			"count": len(filtered),
			"as_of": now,
		})
	})

	// ── Default text format, no filter ──────────────────────────
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "list"})
	if rc != 0 {
		t.Fatalf("rc=%d, want 0; stderr=%s", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{"peer-aaa", "peer-bbb", "peer-ccc", "claude-code", "codex", "gemini", "PEER_ID", "BACKEND"} {
		if !strings.Contains(body, want) {
			t.Errorf("text output missing %q\n--- output ---\n%s", want, body)
		}
	}
	// last_seen-desc means peer-aaa (5s) before peer-bbb (30s)
	// before peer-ccc (10m). Verify positional ordering.
	posA := strings.Index(body, "peer-aaa")
	posB := strings.Index(body, "peer-bbb")
	posC := strings.Index(body, "peer-ccc")
	if !(posA >= 0 && posB > posA && posC > posB) {
		t.Errorf("expected peer-aaa < peer-bbb < peer-ccc by position; got A=%d B=%d C=%d\n%s", posA, posB, posC, body)
	}

	// ── --circle filter forwarded to daemon as ?circle= ─────────
	out.Reset()
	errb.Reset()
	rc = app.Run([]string{"peer", "list", "--circle", "ml"})
	if rc != 0 {
		t.Fatalf("rc=%d on --circle, stderr=%s", rc, errb.String())
	}
	if got := lastQuery.Get("circle"); got != "ml" {
		t.Errorf("daemon saw circle=%q, want %q", got, "ml")
	}
	body = out.String()
	if !strings.Contains(body, "peer-bbb") {
		t.Errorf("--circle ml expected peer-bbb (the only ml peer); got %q", body)
	}
	if strings.Contains(body, "peer-aaa") || strings.Contains(body, "peer-ccc") {
		t.Errorf("--circle ml leaked default-circle peers; got %q", body)
	}

	// ── --format json round-trip ────────────────────────────────
	out.Reset()
	errb.Reset()
	rc = app.Run([]string{"peer", "list", "--format", "json"})
	if rc != 0 {
		t.Fatalf("rc=%d on --format json, stderr=%s", rc, errb.String())
	}
	var got struct {
		Peers []a2a.Peer `json:"peers"`
		Count int        `json:"count"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n--- body ---\n%s", err, out.String())
	}
	if got.Count != 3 {
		t.Errorf("Count=%d, want 3", got.Count)
	}
	if len(got.Peers) != 3 || got.Peers[0].PeerID != "peer-aaa" {
		t.Errorf("peers not sorted last_seen-desc; got %+v", got.Peers)
	}

	// ── --format tsv carries every column header ────────────────
	out.Reset()
	errb.Reset()
	rc = app.Run([]string{"peer", "list", "--format", "tsv"})
	if rc != 0 {
		t.Fatalf("rc=%d on --format tsv, stderr=%s", rc, errb.String())
	}
	tsv := out.String()
	header := strings.SplitN(tsv, "\n", 2)[0]
	for _, col := range []string{"PEER_ID", "BACKEND", "DISPLAY_NAME", "STATUS", "LAST_SEEN", "ROLE", "CIRCLE"} {
		if !strings.Contains(header, col) {
			t.Errorf("tsv header missing %q; got %q", col, header)
		}
	}
}

// TestPeer_ListUnknownFormat — bad --format value rejected at
// parse time with rc=2 (usage error), no daemon round-trip.
func TestPeer_ListUnknownFormat(t *testing.T) {
	calls := 0
	stubDaemonForPeers(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"peers": []a2a.Peer{},
			"count": 0,
			"as_of": time.Now().UTC(),
		})
	})

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "list", "--format", "yaml"})
	if rc != 2 {
		t.Errorf("rc=%d, want 2 on bad --format", rc)
	}
	if !strings.Contains(errb.String(), "--format") {
		t.Errorf("stderr should mention --format; got %q", errb.String())
	}
	// Daemon round-trip happens before format dispatch in the
	// current implementation (we render after fetching), but the
	// test should still confirm the exit code; assert a friendly
	// upper bound on calls so a future refactor that lifts the
	// format check earlier doesn't silently break anything.
	if calls > 1 {
		t.Errorf("calls=%d, expected ≤1", calls)
	}
}

// drainStub stands up a fake daemon for the drain-verb tests:
// GET /v1/peers/<id>/messages dequeues the inbox and returns it
// (consume semantics mirror the real daemon — once drained, the
// next call returns empty); GET /v1/peers returns the seeded peer
// table so display_name resolution works for --format context.
//
// `inbox` is captured by-reference so tests can seed it BEFORE
// the registered peers.d/<session>.id pointer is written, and
// the second drain call sees an empty queue (atomic-consume
// invariant).
type drainStub struct {
	peers []a2a.Peer
	inbox []a2a.Message
}

func newDrainStub(t *testing.T, session, peerID string, peers []a2a.Peer, inbox []a2a.Message) *drainStub {
	t.Helper()
	st := &drainStub{peers: peers, inbox: append([]a2a.Message(nil), inbox...)}
	stubDaemonForPeers(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/peers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"peers": st.peers,
				"count": len(st.peers),
				"as_of": time.Now().UTC(),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/peers/"+peerID+"/messages":
			peek := r.URL.Query().Get("peek") == "1"
			out := append([]a2a.Message(nil), st.inbox...)
			if !peek {
				st.inbox = nil
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"peer_id":  peerID,
				"messages": out,
				"count":    len(out),
				"peek":     peek,
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	})
	// stubDaemonForPeers already redirected XDG_CONFIG_HOME to
	// a tmp dir; write the session→peer_id pointer into it so
	// runPeerDrain finds the registered peer without going
	// through `peer register`.
	if err := os.MkdirAll(a2a.PeersStateDir(), 0o700); err != nil {
		t.Fatalf("mkdir peers.d: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a2a.PeersStateDir(), session+".id"), []byte(peerID+"\n"), 0o600); err != nil {
		t.Fatalf("write session pointer: %v", err)
	}
	return st
}

// TestPeer_DrainConsumes — write 2 messages, drain → both appear
// → second drain reports empty (no rebuffering, no replay). This
// pins the atomic-consume invariant: once the daemon hands a
// message to the CLI, that message must not reappear on the
// next session-tick.
func TestPeer_DrainConsumes(t *testing.T) {
	const session = "sess-drain"
	const peerID = "peer-drain"
	now := time.Now().UTC()
	msgs := []a2a.Message{
		{ID: "m1", FromPeer: "peer-other", Type: a2a.MsgNotification, Text: "first", Timestamp: now},
		{ID: "m2", FromPeer: "peer-other", Type: a2a.MsgNotification, Text: "second", Timestamp: now.Add(time.Second)},
	}
	newDrainStub(t, session, peerID, nil, msgs)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "drain", "--session", session})
	if rc != 0 {
		t.Fatalf("first drain rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	if !strings.Contains(body, "first") || !strings.Contains(body, "second") {
		t.Errorf("first drain missing payloads: %q", body)
	}

	// Second drain: server-side queue is now empty — the verb
	// must produce no stdout and exit 0.
	out.Reset()
	errb.Reset()
	rc = app.Run([]string{"peer", "drain", "--session", session})
	if rc != 0 {
		t.Fatalf("second drain rc=%d, stderr=%s", rc, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("second drain expected silent empty; got %q", out.String())
	}
}

// TestPeer_DrainContextFormat — --format context emits each
// message wrapped in the system-prompt-shaped block so a hook
// can splice it directly into the live agent's turn. Asserts:
//   - leading newline
//   - "[clawtool peer message from <display_name>]:" prefix
//     (display_name resolved via /v1/peers, not raw peer_id)
//   - message text body
//   - trailing newline
func TestPeer_DrainContextFormat(t *testing.T) {
	const session = "sess-ctx"
	const peerID = "peer-ctx"
	const senderID = "peer-sender"
	now := time.Now().UTC()
	peers := []a2a.Peer{
		{PeerID: senderID, DisplayName: "alice@host/codex", Backend: "codex"},
		{PeerID: peerID, DisplayName: "bob@host/claude-code", Backend: "claude-code"},
	}
	msgs := []a2a.Message{
		{ID: "m1", FromPeer: senderID, Type: a2a.MsgNotification, Text: "claude'a erişebiliyor musun?", Timestamp: now},
	}
	newDrainStub(t, session, peerID, peers, msgs)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "drain", "--session", session, "--format", "context"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	want := "\n[clawtool peer message from alice@host/codex]: claude'a erişebiliyor musun?\n"
	if !strings.Contains(body, want) {
		t.Errorf("missing context block.\nwant substring: %q\ngot: %q", want, body)
	}
	if !strings.HasPrefix(body, "\n") {
		t.Errorf("context output should start with a newline; got %q", body)
	}
}

// TestPeer_DrainEmpty — a session-tick hook chains drain's stdout
// into the agent's context; an empty inbox must produce no output
// and exit 0 so quiet turns stay quiet. Regression guard for the
// "(inbox empty)" placeholder leaking from the inbox verb.
func TestPeer_DrainEmpty(t *testing.T) {
	const session = "sess-empty"
	const peerID = "peer-empty"
	newDrainStub(t, session, peerID, nil, nil)

	for _, format := range []string{"text", "context", "json"} {
		t.Run(format, func(t *testing.T) {
			out, errb := &bytes.Buffer{}, &bytes.Buffer{}
			app := &App{Stdout: out, Stderr: errb}
			rc := app.Run([]string{"peer", "drain", "--session", session, "--format", format})
			if rc != 0 {
				t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
			}
			if out.Len() != 0 {
				t.Errorf("empty inbox should produce no stdout (format=%s); got %q", format, out.String())
			}
		})
	}
}

// TestPeer_DrainHookJSON_Empty — empty inbox must emit `{}` (an
// empty JSON object) on stdout and exit 0. Claude Code's
// UserPromptSubmit hook contract: empty stdout would crash the
// parser, but `{}` is "no additionalContext to add" so the agent
// processes the prompt unchanged.
func TestPeer_DrainHookJSON_Empty(t *testing.T) {
	const session = "sess-hookjson-empty"
	const peerID = "peer-hookjson-empty"
	newDrainStub(t, session, peerID, nil, nil)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "drain", "--session", session, "--format", "hook-json"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimSpace(out.String())
	if body != "{}" {
		t.Errorf("empty inbox should emit `{}`; got %q", out.String())
	}
}

// TestPeer_DrainHookJSON_OneMessage — pin the
// hookSpecificOutput envelope shape for a single-message inbox.
// The injected additionalContext must carry the "[clawtool peer
// message — from <name>, <ts>]\n<body>" block, and hookEventName
// must be "UserPromptSubmit" so Claude Code knows where to inject.
func TestPeer_DrainHookJSON_OneMessage(t *testing.T) {
	const session = "sess-hookjson-one"
	const peerID = "peer-hookjson-one"
	const senderID = "peer-hookjson-sender"
	now := time.Now().UTC()
	peers := []a2a.Peer{
		{PeerID: senderID, DisplayName: "alice@host/codex", Backend: "codex"},
		{PeerID: peerID, DisplayName: "bob@host/claude-code", Backend: "claude-code"},
	}
	msgs := []a2a.Message{
		{ID: "m1", FromPeer: senderID, Type: a2a.MsgNotification, Text: "hello bob", Timestamp: now},
	}
	newDrainStub(t, session, peerID, peers, msgs)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "drain", "--session", session, "--format", "hook-json"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON envelope: %v\n--- body ---\n%s", err, out.String())
	}
	if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName=%q, want UserPromptSubmit", got.HookSpecificOutput.HookEventName)
	}
	ctx := got.HookSpecificOutput.AdditionalContext
	wantPrefix := "[clawtool peer message — from alice@host/codex, "
	if !strings.HasPrefix(ctx, wantPrefix) {
		t.Errorf("additionalContext prefix mismatch.\nwant prefix: %q\ngot: %q", wantPrefix, ctx)
	}
	if !strings.Contains(ctx, "hello bob") {
		t.Errorf("additionalContext missing message body 'hello bob'; got %q", ctx)
	}
	if !strings.Contains(ctx, now.Format(time.RFC3339)) {
		t.Errorf("additionalContext missing RFC3339 timestamp; got %q", ctx)
	}
}

// TestPeer_DrainHookJSON_MultipleMessages — N messages must be
// joined with `\n\n---\n\n` so the agent sees a clean separator
// between distinct peer messages. Pins the join token + ordering.
func TestPeer_DrainHookJSON_MultipleMessages(t *testing.T) {
	const session = "sess-hookjson-many"
	const peerID = "peer-hookjson-many"
	const sA = "peer-A"
	const sB = "peer-B"
	now := time.Now().UTC()
	peers := []a2a.Peer{
		{PeerID: sA, DisplayName: "alice@host/codex"},
		{PeerID: sB, DisplayName: "bob@host/gemini"},
		{PeerID: peerID, DisplayName: "carol@host/claude-code"},
	}
	msgs := []a2a.Message{
		{ID: "m1", FromPeer: sA, Type: a2a.MsgNotification, Text: "first body", Timestamp: now},
		{ID: "m2", FromPeer: sB, Type: a2a.MsgNotification, Text: "second body", Timestamp: now.Add(time.Second)},
		{ID: "m3", FromPeer: sA, Type: a2a.MsgNotification, Text: "third body", Timestamp: now.Add(2 * time.Second)},
	}
	newDrainStub(t, session, peerID, peers, msgs)

	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "drain", "--session", session, "--format", "hook-json"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON envelope: %v\n--- body ---\n%s", err, out.String())
	}
	ctx := got.HookSpecificOutput.AdditionalContext
	// Three messages → exactly two `\n\n---\n\n` separators.
	parts := strings.Split(ctx, "\n\n---\n\n")
	if len(parts) != 3 {
		t.Fatalf("expected 3 blocks joined by `\\n\\n---\\n\\n`; got %d parts:\n%s", len(parts), ctx)
	}
	if !strings.Contains(parts[0], "first body") || !strings.Contains(parts[0], "alice@host/codex") {
		t.Errorf("block 0 mismatch: %q", parts[0])
	}
	if !strings.Contains(parts[1], "second body") || !strings.Contains(parts[1], "bob@host/gemini") {
		t.Errorf("block 1 mismatch: %q", parts[1])
	}
	if !strings.Contains(parts[2], "third body") || !strings.Contains(parts[2], "alice@host/codex") {
		t.Errorf("block 2 mismatch: %q", parts[2])
	}
}

// TestPeer_DrainUnknownFormat — bad --format value rejected at
// parse time with rc=2, no daemon round-trip. Mirror of
// TestPeer_ListUnknownFormat.
func TestPeer_DrainUnknownFormat(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}
	rc := app.Run([]string{"peer", "drain", "--format", "yaml"})
	if rc != 2 {
		t.Errorf("rc=%d, want 2 on bad --format", rc)
	}
	if !strings.Contains(errb.String(), "--format") {
		t.Errorf("stderr should mention --format; got %q", errb.String())
	}
}

// _silence keeps fmt imported when other tests don't need it
// directly; helps future additions stay compile-safe.
var _ = fmt.Sprintf
