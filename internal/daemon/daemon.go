// Package daemon manages a single persistent `clawtool serve --listen
// --mcp-http` process the operator's hosts (Codex / OpenCode / Gemini /
// Claude Code) all fan into. Per ADR-014 (recursive) and the operator's
// design call: every host that registers clawtool as an MCP server
// should connect to the SAME backend so BIAM identity, task store,
// and notify channels are shared. Stdio-spawning a child per host
// would create N independent identities and N independent BIAM
// stores — cross-host notify cannot work that way.
//
// State lives at $XDG_CONFIG_HOME/clawtool/daemon.json (LF-delimited,
// 0600). Token file (bearer) lives at $XDG_CONFIG_HOME/clawtool/
// listener-token. Ensure starts the daemon if missing, returns the
// existing state otherwise; Stop SIGTERMs and cleans up.
//
// This package is the only place that knows the daemon's process
// lifecycle. Adapters (mcp_host.go) and CLI (`clawtool daemon …`)
// drive it through Ensure / Stop / Status — they don't touch the
// state file directly.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// State is the persisted snapshot of a running daemon.
type State struct {
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
	TokenFile string    `json:"token_file"`
	LogFile   string    `json:"log_file"`
}

// URL is the MCP-over-HTTP endpoint hosts dial.
func (s *State) URL() string {
	if s == nil || s.Port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", s.Port)
}

// HealthURL is the unauthenticated probe URL the daemon exposes for
// readiness checks.
func (s *State) HealthURL() string {
	if s == nil || s.Port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1/health", s.Port)
}

// StatePath returns the file Ensure / Stop persist to. Honors
// $XDG_CONFIG_HOME, else ~/.config/clawtool/daemon.json.
func StatePath() string {
	return filepath.Join(configDir(), "daemon.json")
}

// TokenPath returns the bearer-token file the daemon and adapters
// share. Same XDG conventions as StatePath.
func TokenPath() string {
	return filepath.Join(configDir(), "listener-token")
}

// LogPath returns the daemon's combined-output log path.
func LogPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "clawtool", "daemon.log")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "clawtool", "daemon.log")
	}
	return "daemon.log"
}

func configDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "clawtool")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "clawtool")
	}
	return "."
}

// ReadToken returns the bearer token contents (whitespace-trimmed).
// Empty string + nil error if the file is missing — Ensure ensures
// the file exists before exposing the token to callers.
func ReadToken() (string, error) {
	b, err := os.ReadFile(TokenPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ReadState returns the persisted state, or (nil, nil) if no daemon
// has been started yet. Parse errors are returned verbatim so callers
// can decide whether to wipe + retry.
func ReadState() (*State, error) {
	b, err := os.ReadFile(StatePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StatePath(), err)
	}
	return &s, nil
}

// writeState persists s atomically (temp+rename, mode 0600).
func writeState(s *State) error {
	if err := os.MkdirAll(filepath.Dir(StatePath()), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := StatePath() + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, StatePath())
}

// IsRunning returns true when the recorded PID is alive AND the
// port still answers /v1/health within a short timeout. Both checks
// matter: a stale state file from a crashed daemon must not look
// healthy, and a port that no longer belongs to us (recycled by
// some other process) must not look ours.
func IsRunning(s *State) bool {
	if s == nil || s.PID == 0 || s.Port == 0 {
		return false
	}
	if !pidAlive(s.PID) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.HealthURL(), nil)
	if err != nil {
		return false
	}
	tok, _ := ReadToken()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// pidAlive uses signal 0 (POSIX no-op delivery test) to probe the
// process. Returns true iff the PID exists and we have permission
// to signal it.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// Best effort on Windows — FindProcess always succeeds and
		// signal 0 isn't supported. Treat as alive; the health
		// probe will catch dead ports.
		return true
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

// Ensure starts the daemon if it isn't already running and returns
// the live State. Idempotent: if the daemon is already healthy, the
// existing state is returned without spawning.
//
// Spawn flow: pick a free port, ensure the bearer token, fork the
// detached process, write state, poll /v1/health for up to 5s.
//
// Concurrency: two CLI invocations within the spawn window
// (read-state → IsRunning → spawn → write-state) would both see
// "no daemon" and both fork, leaving an orphan racing for the
// state file + ports. We bracket the whole sequence with an OS
// advisory lock on a sibling .lock file (flock on POSIX,
// LockFileEx on Windows via fileLockExclusive). The fast path —
// a healthy daemon already running — does not need the lock; we
// re-check IsRunning inside the lock so a concurrent winner's
// state is observed before we duplicate-spawn.
func Ensure(ctx context.Context) (*State, error) {
	if s, err := ReadState(); err == nil && IsRunning(s) {
		return s, nil
	}

	unlock, err := acquireSpawnLock()
	if err != nil {
		return nil, fmt.Errorf("ensure: acquire spawn lock: %w", err)
	}
	defer unlock()

	// Re-check after acquiring — a concurrent invocation may have
	// won the race and left a healthy daemon for us.
	if s, err := ReadState(); err == nil && IsRunning(s) {
		return s, nil
	}

	tokenPath := TokenPath()
	if _, err := os.Stat(tokenPath); errors.Is(err, os.ErrNotExist) {
		if _, err := initTokenFile(tokenPath); err != nil {
			return nil, fmt.Errorf("init token: %w", err)
		}
	}

	port, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("pick port: %w", err)
	}

	logPath := LogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve self: %w", err)
	}

	cmd := exec.Command(self,
		"serve",
		"--listen", fmt.Sprintf("127.0.0.1:%d", port),
		"--token-file", tokenPath,
		"--mcp-http",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	detachCmd(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	// Don't reap — operator wants a real detached process. The OS
	// adopts it once the parent exits. cmd.Wait elsewhere would
	// block; we rely on PID + health probe for liveness.

	state := &State{
		Version:   1,
		PID:       cmd.Process.Pid,
		Port:      port,
		StartedAt: time.Now().UTC(),
		TokenFile: tokenPath,
		LogFile:   logPath,
	}
	if err := writeState(state); err != nil {
		// Daemon is up but we can't persist — kill it so we don't
		// leak a process the operator can't track.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		return nil, fmt.Errorf("write state: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if IsRunning(state) {
			return state, nil
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = os.Remove(StatePath())
			return nil, fmt.Errorf("daemon failed to come up within 5s (logs: %s)", logPath)
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = os.Remove(StatePath())
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// Stop sends SIGTERM, waits up to 5s, escalates to SIGKILL, then
// removes the state file. No-op if no daemon is recorded.
func Stop() error {
	s, err := ReadState()
	if err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	if !pidAlive(s.PID) {
		_ = os.Remove(StatePath())
		return nil
	}
	p, err := os.FindProcess(s.PID)
	if err != nil {
		return fmt.Errorf("find process %d: %w", s.PID, err)
	}
	if err := p.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("SIGTERM %d: %w", s.PID, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(s.PID) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(s.PID) {
		_ = p.Signal(syscall.SIGKILL)
	}
	_ = os.Remove(StatePath())
	return nil
}

// pickFreePort asks the OS for an unused localhost port by listening
// on :0, recording the assignment, and closing immediately. Carries
// a small race window before the daemon binds, but the daemon
// retries-once on bind failure (via Ensure's polling loop).
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected listener addr type")
	}
	return addr.Port, nil
}

// FormatStatus renders the daemon state as a multi-line human string
// for `clawtool daemon status`. Used by the CLI; tests assert on
// substrings not whole layout.
func FormatStatus(s *State) string {
	if s == nil {
		return "daemon: not running (no state file at " + StatePath() + ")"
	}
	healthy := "yes"
	if !IsRunning(s) {
		healthy = "no (stale)"
	}
	return strings.Join([]string{
		fmt.Sprintf("daemon: pid %d", s.PID),
		fmt.Sprintf("  url:        %s", s.URL()),
		fmt.Sprintf("  health:     %s", healthy),
		fmt.Sprintf("  token-file: %s", s.TokenFile),
		fmt.Sprintf("  log-file:   %s", s.LogFile),
		fmt.Sprintf("  started:    %s", s.StartedAt.Format(time.RFC3339)),
	}, "\n")
}

// initTokenFile writes a fresh 32-byte hex bearer token to path with
// 0600. Mirrors internal/server.InitTokenFile but kept local so this
// package doesn't import server (which would create an import cycle
// via agents → daemon → server → agents).
func initTokenFile(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}
