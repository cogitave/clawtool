// Package biam — Unix-socket dispatch server. Lets `clawtool send
// --async` (a separate CLI process from the daemon) hand a prompt
// off to the daemon's BIAM runner so the dispatch goroutine lives
// in the daemon process. That guarantees the WatchHub frame
// broadcasts cross to the orchestrator's socket subscribers — the
// CLI's own in-process WatchHub never leaves its process.
//
// Without this socket, async CLI dispatches would spawn a
// short-lived runner inside the CLI process, frames would
// broadcast only on the CLI's WatchHub, and the orchestrator
// (subscribed to the daemon's hub) would see zero stream lines
// even though the task itself transits SQLite via the store hook.
//
// Wire format: JSON-line dispatch request → JSON-line dispatch
// response. One request per connection, then close.
//
// Permissions: socket file is mode 0600 — same posture as the
// task-watch socket. XDG_STATE_HOME lives outside config + data,
// matching the runtime-state convention.
package biam

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/xdg"
)

// DefaultDispatchSocketPath sits beside DefaultWatchSocketPath in
// $XDG_STATE_HOME/clawtool/. Both sockets share the same lifecycle
// (daemon up = both bound; daemon down = both gone) so a CLI
// client either uses both or neither.
func DefaultDispatchSocketPath() string {
	return filepath.Join(xdg.StateDir(), "dispatch.sock")
}

// DispatchRequest is the JSON-line wire request. `Action` is an
// enum so the protocol can grow (cancel, list, etc.) without
// breaking older clients — they ignore unknown actions and fall
// through to an error response.
type DispatchRequest struct {
	Action   string         `json:"action"` // "submit"
	Instance string         `json:"instance,omitempty"`
	Prompt   string         `json:"prompt,omitempty"`
	Opts     map[string]any `json:"opts,omitempty"`
}

// DispatchResponse is the JSON-line wire response. Exactly one of
// `TaskID` / `Error` is populated.
type DispatchResponse struct {
	TaskID string `json:"task_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// dispatchSubmitter is the slim runner interface ServeDispatchSocket
// needs. *Runner implements it; tests can stub.
type dispatchSubmitter interface {
	Submit(ctx context.Context, instance, prompt string, opts map[string]any) (string, error)
}

// ServeDispatchSocket binds the dispatch socket at `path`, accepting
// one request per connection until ctx cancels. `runner` is the
// daemon's process-wide BIAM runner — its goroutine lives in the
// daemon process, so frames it broadcasts via Watch.BroadcastFrame
// reach every WatchHub subscriber on the daemon (including
// orchestrator socket clients). Pass an empty path to use the
// default.
//
// Auth: socket file mode 0600 + parent dir 0700. No bearer token —
// any process running as the same user can submit, mirroring the
// trust model of the watch socket.
func ServeDispatchSocket(ctx context.Context, runner dispatchSubmitter, path string) error {
	if runner == nil {
		return errors.New("biam dispatchsocket: nil runner")
	}
	if path == "" {
		path = DefaultDispatchSocketPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("biam dispatchsocket: mkdir parent: %w", err)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("biam dispatchsocket: listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return fmt.Errorf("biam dispatchsocket: chmod %s: %w", path, err)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				_ = os.Remove(path)
				return nil
			}
			fmt.Fprintf(os.Stderr, "biam dispatchsocket: accept: %v\n", err)
			select {
			case <-ctx.Done():
				wg.Wait()
				_ = os.Remove(path)
				return nil
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer c.Close()
			handleDispatchClient(ctx, c, runner)
		}(conn)
	}
}

// handleDispatchClient processes one request per connection.
// Errors are emitted as a structured error response rather than
// closing the connection — gives the CLI a clean diagnostic.
func handleDispatchClient(ctx context.Context, c net.Conn, runner dispatchSubmitter) {
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	dec := json.NewDecoder(bufio.NewReader(c))
	var req DispatchRequest
	if err := dec.Decode(&req); err != nil {
		_ = encodeDispatchResponse(c, DispatchResponse{Error: fmt.Sprintf("parse request: %v", err)})
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	switch req.Action {
	case "submit", "":
		if strings.TrimSpace(req.Prompt) == "" {
			_ = encodeDispatchResponse(c, DispatchResponse{Error: "submit: empty prompt"})
			return
		}
		taskID, err := runner.Submit(ctx, req.Instance, req.Prompt, req.Opts)
		if err != nil {
			_ = encodeDispatchResponse(c, DispatchResponse{Error: err.Error()})
			return
		}
		_ = encodeDispatchResponse(c, DispatchResponse{TaskID: taskID})
	default:
		_ = encodeDispatchResponse(c, DispatchResponse{Error: fmt.Sprintf("unknown action %q", req.Action)})
	}
}

func encodeDispatchResponse(c net.Conn, resp DispatchResponse) error {
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	enc := json.NewEncoder(c)
	enc.SetEscapeHTML(false)
	return enc.Encode(resp)
}

// DispatchClient is the CLI-side handle for submitting a dispatch
// request to a running daemon. Single-use — Dial + Submit + Close.
// Caller is expected to defer Close.
type DispatchClient struct {
	conn net.Conn
}

// DialDispatchSocket connects to the daemon's dispatch socket.
// Empty path uses the default. Returns ErrNoDispatchSocket when
// the socket is missing — useful for "is the daemon running?"
// detection in CLI flows that fall back gracefully.
func DialDispatchSocket(path string) (*DispatchClient, error) {
	if path == "" {
		path = DefaultDispatchSocketPath()
	}
	c, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
			return nil, ErrNoDispatchSocket
		}
		// connection refused / EAGAIN — daemon present-or-stale,
		// surface the raw error so the operator sees what's wrong.
		return nil, fmt.Errorf("dial dispatch socket: %w", err)
	}
	return &DispatchClient{conn: c}, nil
}

// Submit sends one dispatch request and waits for the response.
// The connection is closed afterwards regardless of outcome.
func (c *DispatchClient) Submit(ctx context.Context, instance, prompt string, opts map[string]any) (string, error) {
	if c == nil || c.conn == nil {
		return "", errors.New("dispatch client: not connected")
	}
	defer c.conn.Close()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(15 * time.Second)
	}
	_ = c.conn.SetDeadline(deadline)

	req := DispatchRequest{
		Action:   "submit",
		Instance: instance,
		Prompt:   prompt,
		Opts:     opts,
	}
	enc := json.NewEncoder(c.conn)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}

	dec := json.NewDecoder(bufio.NewReader(c.conn))
	var resp DispatchResponse
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	if resp.TaskID == "" {
		return "", errors.New("dispatch: empty task_id in response")
	}
	return resp.TaskID, nil
}

// Close releases the connection. Idempotent; safe to call after
// Submit (which already closes).
func (c *DispatchClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// ErrNoDispatchSocket signals the CLI fallback path: no daemon is
// running. Callers can either error out with a "start the daemon"
// hint or fall back to the legacy in-process runner (with the
// caveat that frames won't reach the orchestrator).
var ErrNoDispatchSocket = errors.New("biam dispatchsocket: socket not reachable — start `clawtool serve` first")
