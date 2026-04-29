// Package cli — `clawtool peer` subcommand. Phase 1 surface for
// ADR-024 peer discovery: the runtime-side primitive every hook
// (claude-code, codex, gemini, opencode) calls to register the
// running session into the daemon's peer registry.
//
// Three verbs:
//
//	clawtool peer register --backend X [--display-name Y] [--session ID]
//	clawtool peer heartbeat [--session ID] [--status busy|online]
//	clawtool peer deregister [--session ID]
//
// State: each register writes the assigned peer_id to a session-
// keyed file under ~/.config/clawtool/peers.d/<session>.id, so the
// downstream heartbeat / deregister calls find the right peer
// without the hook having to thread the id explicitly. Session IDs
// come from the runtime's hook payload (claude-code's transcript_path
// already has one); when --session is omitted, falls back to
// "default" — single-session-per-host hosts work out of the box.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/daemon"
)

const peerUsage = `Usage:
  clawtool peer register --backend <claude-code|codex|gemini|opencode|clawtool>
                         [--display-name <text>] [--session <id>]
                         [--circle <name>] [--path <abs-path>]
                         [--role agent|orchestrator] [--tmux-pane <id>]
                                           POST /v1/peers/register; persist the
                                           assigned peer_id under the session
                                           key for later heartbeat/deregister.
  clawtool peer heartbeat [--session <id>] [--status online|busy|offline]
                                           POST /v1/peers/{id}/heartbeat using
                                           the saved peer_id.
  clawtool peer deregister [--session <id>]
                                           DELETE /v1/peers/{id} and remove the
                                           session-keyed state file.

This is the runtime-side primitive — claude-code's bundled hooks fire it
automatically; for codex / gemini / opencode wire it from your runtime's
session hook (see ` + "`clawtool hooks install <runtime>`" + ` for the snippet).
`

// runPeer dispatches `clawtool peer ...`.
func (a *App) runPeer(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, peerUsage)
		return 2
	}
	switch argv[0] {
	case "register":
		return a.runPeerRegister(argv[1:])
	case "heartbeat":
		return a.runPeerHeartbeat(argv[1:])
	case "deregister":
		return a.runPeerDeregister(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool peer: unknown subcommand %q\n\n%s", argv[0], peerUsage)
		return 2
	}
}

func (a *App) runPeerRegister(argv []string) int {
	fs := flag.NewFlagSet("peer register", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	backend := fs.String("backend", "", "Runtime family (claude-code|codex|gemini|opencode|clawtool). Required.")
	displayName := fs.String("display-name", "", "Human-friendly label (defaults to user@host).")
	session := fs.String("session", defaultSessionKey(), "Session identifier — keys the saved peer_id.")
	circle := fs.String("circle", "", "Group name (defaults to tmux session or 'default').")
	path := fs.String("path", "", "Project root path (defaults to cwd).")
	role := fs.String("role", "", "agent | orchestrator (default agent).")
	pane := fs.String("tmux-pane", os.Getenv("TMUX_PANE"), "tmux pane id (auto-detected from $TMUX_PANE).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *backend == "" {
		fmt.Fprintln(a.Stderr, "clawtool peer register: --backend is required")
		return 2
	}
	// Fallback: pull session id from the runtime's hook event JSON
	// when neither --session nor the env var was supplied. Claude
	// Code, for instance, ships {"session_id": "..."} on stdin for
	// every hook fire — so a one-line shell hook (`clawtool peer
	// register --backend claude-code`) gets correct keying for free.
	if *session == "default" && a.Stdin != nil {
		if id := readSessionFromStdin(a.Stdin); id != "" {
			*session = id
		}
	}
	if *displayName == "" {
		*displayName = defaultDisplayName(*backend)
	}
	if *path == "" {
		if cwd, err := os.Getwd(); err == nil {
			*path = cwd
		}
	}

	in := a2a.RegisterInput{
		DisplayName: *displayName,
		Path:        *path,
		Backend:     *backend,
		Circle:      *circle,
		TmuxPane:    *pane,
		PID:         os.Getpid(),
	}
	if *role != "" {
		in.Role = a2a.PeerRole(*role)
	}
	body, _ := json.Marshal(in)

	var peer a2a.Peer
	if err := daemonRequest(http.MethodPost, "/v1/peers/register", bytes.NewReader(body), &peer); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer register: %v\n", err)
		return 1
	}
	if err := writePeerIDFile(*session, peer.PeerID); err != nil {
		// Non-fatal: the peer registered, we just couldn't persist
		// the id locally. Surface the warning so the operator can
		// fix permissions but don't fail the hook.
		fmt.Fprintf(a.Stderr, "clawtool peer register: warning: persist peer_id: %v\n", err)
	}
	fmt.Fprintln(a.Stdout, peer.PeerID)
	return 0
}

func (a *App) runPeerHeartbeat(argv []string) int {
	fs := flag.NewFlagSet("peer heartbeat", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	session := fs.String("session", defaultSessionKey(), "Session identifier (matches the register call).")
	status := fs.String("status", "", "Optional: online | busy | offline.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *session == "default" && a.Stdin != nil {
		if id := readSessionFromStdin(a.Stdin); id != "" {
			*session = id
		}
	}
	peerID, err := readPeerIDFile(*session)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer heartbeat: %v\n", err)
		return 1
	}
	body, _ := json.Marshal(map[string]string{"status": *status})
	var got a2a.Peer
	if err := daemonRequest(http.MethodPost, "/v1/peers/"+peerID+"/heartbeat", bytes.NewReader(body), &got); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool peer heartbeat: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) runPeerDeregister(argv []string) int {
	fs := flag.NewFlagSet("peer deregister", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	session := fs.String("session", defaultSessionKey(), "Session identifier (matches the register call).")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *session == "default" && a.Stdin != nil {
		if id := readSessionFromStdin(a.Stdin); id != "" {
			*session = id
		}
	}
	peerID, err := readPeerIDFile(*session)
	if err != nil {
		// Already deregistered or never registered — silent success
		// so SessionEnd hooks don't surface noise on idempotent runs.
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool peer deregister: %v\n", err)
		return 1
	}
	var got a2a.Peer
	if err := daemonRequest(http.MethodDelete, "/v1/peers/"+peerID, nil, &got); err != nil {
		// Best-effort: still try to remove the local state file
		// so the next session doesn't inherit a stale id.
		_ = removePeerIDFile(*session)
		fmt.Fprintf(a.Stderr, "clawtool peer deregister: %v\n", err)
		return 1
	}
	_ = removePeerIDFile(*session)
	return 0
}

// daemonRequest dials the local daemon's HTTP listener with the
// shared bearer token. Times out at 5 s — well under any hook's
// 60 s budget so a wedged daemon doesn't stall a Stop event.
func daemonRequest(method, path string, body *bytes.Reader, out any) error {
	state, err := daemon.ReadState()
	if err != nil {
		return fmt.Errorf("read daemon state: %w", err)
	}
	if state == nil {
		return errors.New("no daemon running — start it with `clawtool daemon start`")
	}
	tok, _ := daemon.ReadToken()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, path)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reader interface {
		Read(p []byte) (int, error)
	}
	if body != nil {
		reader = body
	}
	var req *http.Request
	if reader != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, body)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("dial daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("daemon returned %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// peerStateDir returns ~/.config/clawtool/peers.d (XDG-aware).
func peerStateDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "clawtool", "peers.d")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "clawtool", "peers.d")
	}
	return "peers.d"
}

func peerIDFile(session string) string {
	if session == "" {
		session = "default"
	}
	return filepath.Join(peerStateDir(), sanitizeSession(session)+".id")
}

func writePeerIDFile(session, peerID string) error {
	dir := peerStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(peerIDFile(session), []byte(peerID+"\n"), 0o600)
}

func readPeerIDFile(session string) (string, error) {
	b, err := os.ReadFile(peerIDFile(session))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func removePeerIDFile(session string) error {
	if err := os.Remove(peerIDFile(session)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// sanitizeSession strips path separators / weird chars from the
// session key so a malicious or malformed value can't escape
// peers.d. Whitelist [A-Za-z0-9._-]; everything else collapses
// to '-'.
func sanitizeSession(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

// defaultSessionKey resolves a key from the env (CLAWTOOL_PEER_SESSION
// preferred, then CLAUDE_SESSION_ID for claude-code parity), falling
// back to "default" for single-session hosts.
func defaultSessionKey() string {
	for _, k := range []string{"CLAWTOOL_PEER_SESSION", "CLAUDE_SESSION_ID"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "default"
}

func defaultDisplayName(backend string) string {
	user := firstNonEmpty(os.Getenv("USER"), os.Getenv("USERNAME"), "user")
	host, _ := os.Hostname()
	if host == "" {
		host = "host"
	}
	return fmt.Sprintf("%s@%s/%s", user, host, backend)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// readSessionFromStdin best-effort decodes a single Claude-Code-
// style hook event from stdin and returns its session_id. Empty
// string when stdin is empty / not JSON / has no session_id —
// callers fall back to "default" in that case.
//
// Capped at 64 KiB so a runaway producer can't OOM the hook.
func readSessionFromStdin(r io.Reader) string {
	limited := io.LimitReader(r, 64*1024)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) == 0 {
		return ""
	}
	var ev struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return ""
	}
	return strings.TrimSpace(ev.SessionID)
}
