// Package worker — sandbox-worker server (ADR-029 phase 1).
//
// Listens on a single TCP port, accepts one bearer-authenticated
// WebSocket dial from the daemon, dispatches Request frames to
// per-kind handlers, writes Response frames back. Closes the
// listener after the first client (single-tenant by design;
// future phase will pool workers per-conversation).
package worker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ServerOptions configures the worker's listener.
type ServerOptions struct {
	Listen   string // ":2024" or "127.0.0.1:0" (port 0 = pick a free port)
	Token    string // bearer token; clients must present `Authorization: Bearer <token>`
	Workdir  string // root the worker resolves relative paths against; default cwd
	MaxBytes int    // per-response cap (default 4 MiB)
}

// Run is the worker's main entrypoint. Blocks until ctx is
// cancelled or the listener errors out fatally.
func Run(ctx context.Context, opts ServerOptions) error {
	if strings.TrimSpace(opts.Listen) == "" {
		return errors.New("worker: --listen is required")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return errors.New("worker: bearer token required")
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 4 * 1024 * 1024
	}
	if opts.Workdir == "" {
		opts.Workdir = "/workspace"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Bearer auth — constant-time so token-validity timing
		// doesn't leak the prefix. Mirrors internal/server's
		// authMiddleware.
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(h[len(prefix):])), []byte(opts.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // no Origin check; daemon is the only trusted dial
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()

		serveConn(r.Context(), conn, opts)
	})

	srv := &http.Server{
		Addr:              opts.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	fmt.Fprintf(os.Stderr, "clawtool sandbox-worker: listening on %s (workdir=%s)\n", opts.Listen, opts.Workdir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen %s: %w", opts.Listen, err)
	}
	return nil
}

// serveConn reads request frames in a loop until the WebSocket
// closes. Each request gets its own goroutine so a slow exec
// doesn't block reads (responses use the conn's send mutex via
// websocket.Conn's internal serialisation). serveConn joins all
// in-flight dispatch goroutines before returning so the caller's
// `defer conn.CloseNow()` doesn't fire while a handler is still
// holding the websocket.
func serveConn(ctx context.Context, conn *websocket.Conn, opts ServerOptions) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		req, derr := DecodeRequest(raw)
		if derr != nil {
			_ = writeErr(ctx, conn, "", 1, derr.Error())
			continue
		}
		wg.Add(1)
		go func(r *Request) {
			defer wg.Done()
			body, status, herr := dispatch(ctx, r, opts)
			if herr != nil {
				_ = writeErr(ctx, conn, r.ID, status, herr.Error())
				return
			}
			_ = writeOK(ctx, conn, r.ID, body)
		}(req)
	}
}

func writeOK(ctx context.Context, conn *websocket.Conn, id string, body json.RawMessage) error {
	resp := Response{V: ProtocolVersion, ID: id, Status: 0, Body: body}
	b, _ := json.Marshal(&resp)
	return conn.Write(ctx, websocket.MessageText, b)
}

func writeErr(ctx context.Context, conn *websocket.Conn, id string, status int, msg string) error {
	resp := Response{V: ProtocolVersion, ID: id, Status: status, Error: msg}
	b, _ := json.Marshal(&resp)
	return conn.Write(ctx, websocket.MessageText, b)
}

// dispatch routes a request to its kind-specific handler and
// returns the encoded body. Returns (nil, status, err) on
// caller / worker errors.
func dispatch(ctx context.Context, r *Request, opts ServerOptions) (json.RawMessage, int, error) {
	switch r.Kind {
	case KindPing:
		body, merr := MarshalBody(map[string]string{"pong": "ok", "v": ProtocolVersion})
		if merr != nil {
			return nil, 2, merr
		}
		return body, 0, nil

	case KindExec:
		var req ExecRequest
		if err := UnmarshalBody(r.Body, &req); err != nil {
			return nil, 1, err
		}
		return handleExec(ctx, req, opts)

	case KindRead:
		var req ReadRequest
		if err := UnmarshalBody(r.Body, &req); err != nil {
			return nil, 1, err
		}
		return handleRead(req, opts)

	case KindWrite:
		var req WriteRequest
		if err := UnmarshalBody(r.Body, &req); err != nil {
			return nil, 1, err
		}
		return handleWrite(req, opts)

	case KindStat:
		var req StatRequest
		if err := UnmarshalBody(r.Body, &req); err != nil {
			return nil, 1, err
		}
		return handleStat(req, opts)

	default:
		return nil, 1, fmt.Errorf("unknown kind %q", r.Kind)
	}
}

// handleExec runs a shell command in opts.Workdir and returns
// the structured result. Mirrors mcp__clawtool__Bash's contract
// so the daemon can route transparently.
func handleExec(ctx context.Context, req ExecRequest, opts ServerOptions) (json.RawMessage, int, error) {
	cwd := opts.Workdir
	if req.Cwd != "" {
		cwd = resolveInside(opts.Workdir, req.Cwd)
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/bin/bash", "-c", req.Command)
	cmd.Dir = cwd
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), envSlice(req.Env)...)
	}
	start := time.Now()
	stdout, stderr, exitCode, timedOut := runCmd(cmd, opts.MaxBytes)
	dur := time.Since(start)

	body, merr := MarshalBody(ExecResponse{
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exitCode,
		DurationMs: dur.Milliseconds(),
		TimedOut:   timedOut,
		Cwd:        cwd,
	})
	if merr != nil {
		return nil, 2, merr
	}
	return body, 0, nil
}

// handleRead is the worker's Read tool counterpart. Stays simple
// in Phase 1: read whole file, line slice on demand. No
// format-aware decoding (PDF / docx) — that path stays host-side
// for now and routes via mode=off / explicit fallback.
func handleRead(req ReadRequest, opts ServerOptions) (json.RawMessage, int, error) {
	abs := resolveInside(opts.Workdir, req.Path)
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, 1, err
	}
	content := string(b)
	if req.LineStart > 0 || req.LineEnd > 0 {
		lines := strings.Split(content, "\n")
		start := req.LineStart - 1
		if start < 0 {
			start = 0
		}
		end := req.LineEnd
		if end <= 0 || end > len(lines) {
			end = len(lines)
		}
		if start > end {
			start = end
		}
		content = strings.Join(lines[start:end], "\n")
	}
	body, merr := MarshalBody(ReadResponse{
		Content:    content,
		TotalLines: strings.Count(string(b), "\n") + 1,
		SizeBytes:  int64(len(b)),
	})
	if merr != nil {
		return nil, 2, merr
	}
	return body, 0, nil
}

func handleWrite(req WriteRequest, opts ServerOptions) (json.RawMessage, int, error) {
	abs := resolveInside(opts.Workdir, req.Path)
	created := false
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		created = true
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, 1, err
		}
	}
	if req.Mode == "create" && !created {
		return nil, 1, fmt.Errorf("file already exists at %s (mode=create)", abs)
	}
	if err := os.WriteFile(abs, []byte(req.Content), 0o644); err != nil {
		return nil, 1, err
	}
	body, merr := MarshalBody(WriteResponse{BytesWritten: len(req.Content), Created: created})
	if merr != nil {
		return nil, 2, merr
	}
	return body, 0, nil
}

func handleStat(req StatRequest, opts ServerOptions) (json.RawMessage, int, error) {
	abs := resolveInside(opts.Workdir, req.Path)
	st, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		body, _ := MarshalBody(StatResponse{Exists: false})
		return body, 0, nil
	}
	if err != nil {
		return nil, 1, err
	}
	body, merr := MarshalBody(StatResponse{
		Exists:  true,
		IsDir:   st.IsDir(),
		Size:    st.Size(),
		ModeStr: st.Mode().String(),
	})
	if merr != nil {
		return nil, 2, merr
	}
	return body, 0, nil
}

// resolveInside makes the worker honour its workdir as an FS root.
// Absolute paths in the request are interpreted relative to the
// workdir's "/" — so `Read /foo.txt` becomes `<workdir>/foo.txt`.
// Callers wanting host paths must explicitly disable worker mode.
func resolveInside(workdir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Join(workdir, filepath.Clean(p))
	}
	return filepath.Join(workdir, p)
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// runCmd is a thin wrapper that captures stdout / stderr with a
// per-stream cap and reports timed-out separately so the response
// frame can carry the distinction. Mirrors internal/tools/core's
// existing Bash semantics.
func runCmd(cmd *exec.Cmd, maxBytes int) (stdout, stderr string, exitCode int, timedOut bool) {
	var so, se strings.Builder
	cmd.Stdout = capWriter(&so, maxBytes)
	cmd.Stderr = capWriter(&se, maxBytes)
	err := cmd.Run()
	stdout = so.String()
	stderr = se.String()
	exitCode = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			if errors.Is(err, context.DeadlineExceeded) {
				timedOut = true
			}
		}
	}
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == -1 {
		// ctx timeout signal path
		timedOut = true
	}
	return
}

type capWriterT struct {
	dst *strings.Builder
	cap int
}

func (c *capWriterT) Write(p []byte) (int, error) {
	if c.dst.Len() >= c.cap {
		return len(p), nil // drop silently after cap; caller-visible via TimedOut/Truncated future fields
	}
	room := c.cap - c.dst.Len()
	if room >= len(p) {
		c.dst.Write(p)
	} else {
		c.dst.Write(p[:room])
	}
	return len(p), nil
}

func capWriter(dst *strings.Builder, cap int) *capWriterT { return &capWriterT{dst: dst, cap: cap} }
