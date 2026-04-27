// Package agents — Transport is the byte-forwarding layer for ADR-014's
// relay surface. Each Transport wraps one upstream CLI's published
// headless mode (`codex exec`, `opencode run`, `gemini -p`, `claude -p`)
// or, in later iterations, its app-server / ACP daemon. clawtool
// passes prompt → transport → upstream and returns the streaming
// response untouched. We do **not** parse or rewrite the wire format.
//
// Per ADR-007 applied recursively (see [[007 Leverage best-in-class
// not reinvent]] in the wiki): we never re-implement an upstream's
// agent loop. Each transport is a thin process boundary, ~50 LoC of
// glue.

package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Transport forwards a prompt to an already-installed upstream CLI
// (or its bridge / app-server) and returns the streamed response.
//
// The returned reader streams whatever wire format the upstream emits
// (NDJSON of stream-json events for claude/gemini, JSON-RPC frames
// for codex app-server, ACP messages for opencode acp, plain text
// otherwise). Closing the reader cancels the upstream process.
type Transport interface {
	Family() string
	Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error)
}

// SendOptions documents the keys Transports look for in the opts map.
// All keys are optional; transports that don't understand a key
// silently ignore it (forward-compat).
type SendOptions struct {
	SessionID string   // upstream session UUID for resume (claude / codex / opencode)
	Model     string   // vendor-specific model name
	Format    string   // "text" | "json" | "stream-json" — passed through where supported
	Cwd       string   // working directory for the upstream CLI
	ExtraArgs []string // raw passthrough argv appended to the upstream command
}

// ParseOptions extracts the well-known keys from a free-form opts map.
// Unknown keys are tolerated — the caller may surface them per-transport.
func ParseOptions(opts map[string]any) SendOptions {
	out := SendOptions{}
	if v, ok := opts["session_id"].(string); ok {
		out.SessionID = v
	}
	if v, ok := opts["model"].(string); ok {
		out.Model = v
	}
	if v, ok := opts["format"].(string); ok {
		out.Format = v
	}
	if v, ok := opts["cwd"].(string); ok {
		out.Cwd = v
	}
	if v, ok := opts["extra_args"].([]string); ok {
		out.ExtraArgs = v
	} else if v, ok := opts["extra_args"].([]any); ok {
		for _, a := range v {
			if s, ok := a.(string); ok {
				out.ExtraArgs = append(out.ExtraArgs, s)
			}
		}
	}
	return out
}

// ErrSelfDispatch is returned when something asks clawtool to dispatch
// a prompt back to the Claude Code session it's running inside —
// that's an infinite loop the supervisor refuses to enter.
var ErrSelfDispatch = errors.New("refusing to dispatch to the calling Claude Code session — would loop")

// ErrBinaryMissing is returned when a transport's upstream CLI binary
// is not on PATH. The bridge recipe should have installed it; the
// supervisor surfaces this so `clawtool bridge add <family>` can be
// suggested.
type ErrBinaryMissing struct {
	Family string
	Binary string
}

func (e ErrBinaryMissing) Error() string {
	return fmt.Sprintf(
		"%s bridge unavailable: %q binary not on PATH (run `clawtool bridge add %s`)",
		e.Family, e.Binary, e.Family,
	)
}

// streamingProcess wraps an exec.Cmd whose stdout pipe streams to the
// caller. Closing the wrapper SIGTERMs the process and waits.
//
// Used by every shell-out transport; centralised here so backpressure
// + cancellation semantics are uniform across families.
type streamingProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

func (s *streamingProcess) Read(p []byte) (int, error) {
	return s.stdout.Read(p)
}

func (s *streamingProcess) Close() error {
	// Close stdout so the upstream sees EOF and exits naturally;
	// also send SIGTERM in case it's still mid-stream so we don't
	// dangle a zombie when the HTTP client disconnects.
	_ = s.stdout.Close()
	if s.cmd != nil && s.cmd.Process != nil {
		// os.Interrupt is portable: SIGINT on unix, CTRL_BREAK_EVENT
		// on windows. CLIs we wrap all clean up on either signal.
		_ = s.cmd.Process.Signal(os.Interrupt)
	}
	if s.cmd != nil {
		_ = s.cmd.Wait()
	}
	return nil
}

// startStreamingExec spawns the given command and returns a ReadCloser
// that streams stdout. stderr is captured but discarded — transports
// surface CLI errors via the exit code on Close.
//
// Stdin is explicitly bound to a closed reader. Some upstream CLIs
// (codex exec, opencode acp) read from stdin to pick up *additional*
// prompt input and will block forever if stdin is left attached to
// the parent process or to a still-open pipe. A pre-closed reader
// signals "no extra input" cleanly.
func startStreamingExec(ctx context.Context, name string, args []string, cwd string) (io.ReadCloser, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = bytes.NewReader(nil)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Discard stderr by default — transports that want it can override
	// post-hoc (Phase 1 keeps the surface minimal).
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	return &streamingProcess{cmd: cmd, stdout: stdout}, nil
}

// joinModel translates the well-known SendOptions.Model into the
// upstream CLI's --model flag. Empty model means "let the upstream
// choose its own default" — never override silently.
func joinModel(model string, flag string) []string {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	return []string{flag, model}
}
