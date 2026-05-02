// Package sandbox — runner.go is the one-shot execution helper
// shared by `clawtool sandbox run` (CLI escape hatch) and the
// SandboxRun MCP tool (chat-driven callers). Both surfaces want
// the same primitive: take a parsed Profile + (command, args,
// stdin), spawn it inside the host-native engine, capture
// stdout/stderr/exit_code, return after a deadline.
//
// Intentionally tiny — engines already do the heavy lifting via
// Engine.Wrap; this file only adds:
//   - exec.Cmd construction with stdin / pipe wiring
//   - timeout + process-group reaping (mirrors the Bash tool's
//     contract: output preserved across SIGKILL)
//   - a wire-shape struct callers can serialize directly
//
// Lives next to sandbox.go so it picks up engineRegistry +
// SelectEngine without importing core / cli (no cycle risk —
// callers in CLI / MCP tool layers consume RunOneShot).
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunRequest is the typed input for RunOneShot. Profile is the
// already-parsed sandbox profile (caller did config lookup +
// ParseProfile); Command + Args are what to actually exec inside
// the sandbox; Stdin is an optional payload piped to the child.
//
// Timeout = 0 means "use the engine's default" (60s). Negative
// values are clamped to the default.
type RunRequest struct {
	Profile *Profile
	Command string
	Args    []string
	Stdin   string
	Timeout time.Duration
}

// RunResult is the wire-shape every RunOneShot caller surfaces
// — CLI's `sandbox run` JSON path and the SandboxRun MCP tool
// both serialize this directly. Field names mirror the Bash tool's
// response so chat-driven callers can compose the two without a
// translation layer.
type RunResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
	Engine   string `json:"engine"`
	Profile  string `json:"profile"`
}

// defaultRunTimeout matches the SandboxRun MCP tool's default and
// the Bash tool's "sensible 1-minute one-off" precedent.
const defaultRunTimeout = 60 * time.Second

// RunOneShot spawns command+args inside the engine selected for
// this host, wrapping with profile, with the supplied stdin and
// timeout. Output is captured even when the deadline kills the
// child — same contract as core.executeBash, just sandbox-aware.
//
// Returns an error only on setup failure (no command, profile
// nil, engine wrap rejection). Process exit codes — including
// non-zero — are returned via RunResult.ExitCode without an
// error so callers can render a structured failure rather than
// crashing on a SIGTERM exit-status.
func RunOneShot(ctx context.Context, req RunRequest) (RunResult, error) {
	if req.Profile == nil {
		return RunResult{}, errors.New("sandbox: profile is required")
	}
	if strings.TrimSpace(req.Command) == "" {
		return RunResult{}, errors.New("sandbox: command is required")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}

	engine := SelectEngine()
	res := RunResult{Engine: engine.Name(), Profile: req.Profile.Name}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, req.Command, req.Args...)
	if req.Stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(req.Stdin))
	}

	// Wrap BEFORE attaching pipes so the engine can rewrite
	// argv (e.g. bwrap prefixes its own flags + `--` before the
	// real command). Same ordering startStreamingExecFull uses
	// in agents/transport.go.
	if err := engine.Wrap(runCtx, cmd, req.Profile); err != nil {
		return res, fmt.Errorf("sandbox %s wrap (engine=%s): %w",
			req.Profile.Name, engine.Name(), err)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Process-group SIGKILL on context cancel: without this, a
	// runaway child shell+grandchild survives the deadline and
	// the call hangs waiting on the pipe. Linux/Darwin only;
	// other platforms keep stdlib's default-cancel.
	applyProcessGroup(cmd)

	runErr := cmd.Run()
	res.Stdout = outBuf.String()
	res.Stderr = errBuf.String()

	if runErr == nil {
		res.ExitCode = 0
		return res, nil
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	// LookPath / Start failure (binary missing, sandbox engine
	// rejected start, …) — surface as exit_code=-1 and stash the
	// error text in stderr so the caller can render it.
	res.ExitCode = -1
	if res.Stderr == "" {
		res.Stderr = runErr.Error()
	}
	return res, nil
}

// applyProcessGroup is duplicated here (instead of imported from
// internal/tools/core) to keep this package leaf-level — core
// imports sandbox transitively. The two implementations stay in
// sync via the per-OS build-tag pair below.
//
// On unix: put the child in its own process group so SIGKILL
// reaps grandchildren too. On non-unix: no-op; stdlib's default
// Cancel handler is good enough.

// (per-OS impl in runner_unix.go / runner_other.go)
