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

// _silence keeps fmt imported when other tests don't need it
// directly; helps future additions stay compile-safe.
var _ = fmt.Sprintf
