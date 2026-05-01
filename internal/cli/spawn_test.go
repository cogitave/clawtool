package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// recordingLauncher captures the launchPlan a single spawn run
// would have executed without forking a real terminal. Mirrors
// the capturedSpawn helper in bootstrap_test.go.
type recordingLauncher struct {
	calls   int
	gotPlan launchPlan
	retTerm string
	retPID  int
	retErr  error
}

func (r *recordingLauncher) Launch(_ context.Context, p launchPlan) (string, int, error) {
	r.calls++
	r.gotPlan = p
	chosen := r.retTerm
	if chosen == "" {
		chosen = p.Terminal
	}
	return chosen, r.retPID, r.retErr
}

// installSpawnSeams swaps defaultLauncher + registerPeerHTTP
// with deterministic stubs for the test's lifetime. mirrors
// stubBootstrap's t.Cleanup discipline.
func installSpawnSeams(t *testing.T, l launcher, hf func(method, path string, body *bytes.Reader, out any) error) {
	t.Helper()
	prevL := defaultLauncher
	prevH := registerPeerHTTP
	defaultLauncher = l
	registerPeerHTTP = hf
	t.Cleanup(func() {
		defaultLauncher = prevL
		registerPeerHTTP = prevH
	})
}

// resetSpawnEnv blanks every environment knob autodetectTerminal
// reads, so a test running on a developer's tmux session doesn't
// see a leaking $TMUX. t.Setenv un-sets at cleanup automatically.
func resetSpawnEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TMUX", "STY", "WSL_DISTRO_NAME"} {
		t.Setenv(k, "")
	}
}

// TestSpawn_DryRun: --dry-run prints the plan and never touches
// the launcher or the daemon.
func TestSpawn_DryRun(t *testing.T) {
	resetSpawnEnv(t)
	rl := &recordingLauncher{}
	calls := 0
	installSpawnSeams(t, rl, func(method, path string, _ *bytes.Reader, _ any) error {
		calls++
		return nil
	})
	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"spawn", "codex", "--dry-run", "--cwd", "/tmp"})
	if rc != 0 {
		t.Fatalf("rc=%d, want 0; stderr=%s", rc, errb.String())
	}
	if rl.calls != 0 {
		t.Errorf("dry-run reached the launcher; calls=%d", rl.calls)
	}
	if calls != 0 {
		t.Errorf("dry-run hit the daemon; calls=%d", calls)
	}
	body := out.String()
	for _, want := range []string{
		"dry-run plan",
		"backend:      codex",
		"family=codex",
		"spawn:        codex",
		"--dangerously-bypass-approvals-and-sandbox",
		"\"dry_run\": true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing %q\n--- got ---\n%s", want, body)
		}
	}
}

// TestSpawn_AutoDetectTerminal: with $TMUX set the cascade picks
// "tmux"; with nothing set on Linux it falls through to "headless".
func TestSpawn_AutoDetectTerminal(t *testing.T) {
	resetSpawnEnv(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	if got := autodetectTerminal(); got != "tmux" {
		t.Errorf("with $TMUX: got %q, want tmux", got)
	}
	t.Setenv("TMUX", "")
	t.Setenv("STY", "12345.pts-0.host")
	if got := autodetectTerminal(); got != "screen" {
		t.Errorf("with $STY: got %q, want screen", got)
	}

	// End-to-end: a dry-run with --terminal honors the flag and
	// skips autodetection (so the test result is independent of
	// the host's actual binary set).
	resetSpawnEnv(t)
	rl := &recordingLauncher{retTerm: "tmux"}
	installSpawnSeams(t, rl, func(string, string, *bytes.Reader, any) error { return nil })
	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"spawn", "claude-code", "--dry-run", "--terminal", "tmux", "--cwd", "/tmp"})
	if rc != 0 {
		t.Fatalf("rc=%d, want 0; stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "terminal:     tmux") {
		t.Errorf("explicit --terminal tmux not honored:\n%s", out.String())
	}
}

// TestSpawn_RegistersPeer: a real (non-dry-run) call hits the
// launcher, then POSTs /v1/peers/register with the right backend
// + role + spawn metadata, and surfaces the assigned peer_id in
// the result JSON.
func TestSpawn_RegistersPeer(t *testing.T) {
	resetSpawnEnv(t)
	rl := &recordingLauncher{retTerm: "tmux", retPID: 99999}

	var seenMethod, seenPath string
	var seenBody []byte
	stub := func(method, path string, body *bytes.Reader, out any) error {
		seenMethod = method
		seenPath = path
		if body != nil {
			seenBody, _ = readAllReader(body)
		}
		// Decode into out as a *a2a.Peer with a fixed peer_id
		// so the caller has something stable to assert on.
		peer := &a2a.Peer{
			PeerID:      "peer-spawned-xyz",
			DisplayName: "spawned",
			Backend:     "codex",
		}
		bb, _ := json.Marshal(peer)
		return json.Unmarshal(bb, out)
	}
	installSpawnSeams(t, rl, stub)

	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{
		"spawn", "codex",
		"--terminal", "tmux",
		"--display-name", "codex-spawn-1",
		"--cwd", "/tmp",
	})
	if rc != 0 {
		t.Fatalf("rc=%d, want 0; stderr=%s", rc, errb.String())
	}
	if rl.calls != 1 {
		t.Errorf("launcher calls=%d, want 1", rl.calls)
	}
	if rl.gotPlan.Bin != "codex" {
		t.Errorf("plan.Bin=%q, want codex", rl.gotPlan.Bin)
	}
	if seenMethod != http.MethodPost {
		t.Errorf("daemon method=%q, want POST", seenMethod)
	}
	if seenPath != "/v1/peers/register" {
		t.Errorf("daemon path=%q, want /v1/peers/register", seenPath)
	}
	// Body shape: a2a.RegisterInput{backend, role, metadata}.
	var in a2a.RegisterInput
	if err := json.Unmarshal(seenBody, &in); err != nil {
		t.Fatalf("body not RegisterInput JSON: %v\n%s", err, seenBody)
	}
	if in.Backend != "codex" {
		t.Errorf("RegisterInput.Backend=%q, want codex", in.Backend)
	}
	if in.Role != a2a.RoleAgent {
		t.Errorf("RegisterInput.Role=%q, want %q", in.Role, a2a.RoleAgent)
	}
	if in.Metadata["spawned_by"] != "clawtool" {
		t.Errorf("metadata.spawned_by=%q, want clawtool", in.Metadata["spawned_by"])
	}
	if in.Metadata["spawn_terminal"] != "tmux" {
		t.Errorf("metadata.spawn_terminal=%q, want tmux", in.Metadata["spawn_terminal"])
	}
	if in.PID != 99999 {
		t.Errorf("RegisterInput.PID=%d, want 99999", in.PID)
	}

	// Stdout JSON should carry the assigned peer_id back.
	var got SpawnResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out.String())
	}
	if got.PeerID != "peer-spawned-xyz" {
		t.Errorf("PeerID=%q, want peer-spawned-xyz", got.PeerID)
	}
	if got.Terminal != "tmux" {
		t.Errorf("Terminal=%q, want tmux", got.Terminal)
	}
	if got.Backend != "codex" || got.Family != "codex" {
		t.Errorf("Backend/Family=%q/%q, want codex/codex", got.Backend, got.Family)
	}
}

// TestSpawn_RejectsUnknownBackend: a backend not in
// supportedSpawnBackends fails fast with rc=2 and never invokes
// the launcher or the daemon.
func TestSpawn_RejectsUnknownBackend(t *testing.T) {
	resetSpawnEnv(t)
	rl := &recordingLauncher{}
	calls := 0
	installSpawnSeams(t, rl, func(string, string, *bytes.Reader, any) error {
		calls++
		return nil
	})
	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"spawn", "hermes", "--dry-run"})
	if rc != 2 {
		t.Fatalf("rc=%d, want 2 (usage error)", rc)
	}
	if rl.calls != 0 || calls != 0 {
		t.Errorf("rejected backend still touched seams: launcher=%d, daemon=%d", rl.calls, calls)
	}
	if !strings.Contains(errb.String(), "unknown backend") {
		t.Errorf("expected 'unknown backend' in stderr; got %q", errb.String())
	}
}

// TestSpawn_RegisterFailureSurfacesError: when the daemon stub
// errors, the verb exits non-zero and prints a warning so the
// operator knows the agent is running but unregistered.
func TestSpawn_RegisterFailureSurfacesError(t *testing.T) {
	resetSpawnEnv(t)
	rl := &recordingLauncher{retTerm: "headless"}
	installSpawnSeams(t, rl, func(string, string, *bytes.Reader, any) error {
		return errors.New("dial daemon: connection refused")
	})
	app, out, errb, _ := newApp(t)
	rc := app.Run([]string{"spawn", "gemini", "--terminal", "headless", "--cwd", "/tmp"})
	if rc == 0 {
		t.Fatalf("expected non-zero rc on register failure; out=%s err=%s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "register peer") {
		t.Errorf("expected 'register peer' in stderr; got %q", errb.String())
	}
	if !strings.Contains(out.String(), "warning: peer registration failed") {
		t.Errorf("expected warning in stdout; got %q", out.String())
	}
}

// TestSpawn_E2E_AgainstStubDaemon proves the full path through
// daemon.HTTPRequest works against a real httptest server (no
// registerPeerHTTP stub). Confirms within ~2s that the peer is
// registered — the operator's hard latency requirement.
func TestSpawn_E2E_AgainstStubDaemon(t *testing.T) {
	resetSpawnEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/peers/register" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(a2a.Peer{
			PeerID: "peer-e2e", Backend: "claude-code",
		})
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfgDir := filepath.Join(tmp, "clawtool")
	_ = os.MkdirAll(cfgDir, 0o700)
	state := map[string]any{
		"version":    1,
		"pid":        os.Getpid(),
		"port":       port,
		"started_at": time.Now().UTC().Format(time.RFC3339),
		"token_file": filepath.Join(cfgDir, "listener-token"),
		"log_file":   filepath.Join(tmp, "daemon.log"),
	}
	body, _ := json.Marshal(state)
	_ = os.WriteFile(filepath.Join(cfgDir, "daemon.json"), body, 0o600)
	_ = os.WriteFile(filepath.Join(cfgDir, "listener-token"), nil, 0o600)

	rl := &recordingLauncher{retTerm: "headless"}
	prevL := defaultLauncher
	defaultLauncher = rl
	t.Cleanup(func() { defaultLauncher = prevL })

	app, out, errb, _ := newApp(t)
	start := time.Now()
	rc := app.Run([]string{"spawn", "claude-code", "--terminal", "headless", "--cwd", tmp})
	if rc != 0 {
		t.Fatalf("rc=%d, want 0; stderr=%s", rc, errb.String())
	}
	if dur := time.Since(start); dur > 2*time.Second {
		t.Errorf("spawn round-trip took %s, want < 2s (operator constraint)", dur)
	}
	if !strings.Contains(out.String(), "peer-e2e") {
		t.Errorf("expected peer-e2e in stdout; got %q", out.String())
	}
}

// readAllReader copies a *bytes.Reader's contents without
// permanently consuming the source — useful when the production
// code re-uses the body for a retry.
func readAllReader(r *bytes.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	buf := make([]byte, r.Len())
	_, err := r.Read(buf)
	return buf, err
}
