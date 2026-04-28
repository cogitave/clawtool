// Package core implements clawtool's canonical core tools per ADR-005.
//
// Quality bar reminder: each core tool must measurably beat the
// corresponding native built-in across the major agents. For Bash:
//   - timeout-safe: output is captured even when the process is killed
//   - predictable cwd: defaults to $HOME, never the daemon's cwd
//   - structured result: stdout/stderr/exit_code/duration_ms/timed_out
//     returned as JSON even on timeout
//
// v0.1 implements timeout-safe + structured result + cwd. Secret redaction
// and per-session command history land in v0.2.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/sandbox/worker"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultTimeoutMs = 120_000 // 2 minutes
	maxTimeoutMs     = 600_000 // 10 minutes
)

type bashResult struct {
	BaseResult
	Command  string `json:"command"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
	Cwd      string `json:"cwd"`
}

// RegisterBash adds the Bash tool to the given MCP server.
func RegisterBash(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Bash",
		mcp.WithDescription(
			"Run a shell command via /bin/bash. "+
				"Returns structured JSON with stdout, stderr, exit_code, duration_ms, "+
				"timed_out, and cwd. Output is preserved even when the command times out. "+
				"Set background=true to fire-and-forget: returns a task_id immediately; "+
				"poll output via BashOutput, terminate via BashKill.",
		),
		mcp.WithString("command",
			mcp.Required(),
			mcp.Description("The shell command to execute. Run via 'bash -c'."),
		),
		mcp.WithString("cwd",
			mcp.Description("Working directory. Defaults to $HOME if empty."),
		),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Hard timeout in milliseconds. Default 120000 (2m), max 600000 (10m)."),
		),
		mcp.WithBoolean("background",
			mcp.Description("Run asynchronously. Returns a task_id immediately. Poll via BashOutput. Default false."),
		),
	)

	s.AddTool(tool, runBash)
}

// bashBackgroundResult is the JSON envelope emitted when a Bash call uses
// background=true. The agent receives task_id immediately and polls via
// BashOutput; the synchronous bashResult shape would have to wait for
// the process to exit, defeating the purpose.
type bashBackgroundResult struct {
	BaseResult
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	TaskID    string `json:"task_id"`
	TimeoutMs int    `json:"timeout_ms"`
}

func (r bashBackgroundResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Command)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s &\n", r.Command)
	fmt.Fprintf(&b, "task_id: %s\n", r.TaskID)
	fmt.Fprintf(&b, "(poll via BashOutput · kill via BashKill)\n")
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(
		fmt.Sprintf("cwd: %s", r.Cwd),
		fmt.Sprintf("timeout: %dms", r.TimeoutMs),
	))
	return b.String()
}

func runBash(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: command"), nil
	}

	cwd := req.GetString("cwd", "")
	timeoutMs := int(req.GetFloat("timeout_ms", float64(defaultTimeoutMs)))
	if timeoutMs <= 0 {
		timeoutMs = defaultTimeoutMs
	}
	if timeoutMs > maxTimeoutMs {
		timeoutMs = maxTimeoutMs
	}

	if req.GetBool("background", false) {
		resolvedCwd := cwd
		if resolvedCwd == "" {
			resolvedCwd = homeDir()
		}
		id, err := SubmitBackgroundBash(ctx, command, resolvedCwd, timeoutMs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := bashBackgroundResult{
			BaseResult: BaseResult{Operation: "Bash"},
			Command:    command,
			Cwd:        resolvedCwd,
			TaskID:     id,
			TimeoutMs:  timeoutMs,
		}
		return resultOf(out), nil
	}

	// ADR-029 phase 2: when sandbox-worker is wired, route the
	// foreground Bash call through it. Background mode keeps using
	// the host path (BashOutput/BashKill state lives in this
	// process); future phase 3 wires bg through the worker too.
	if wc := worker.Global(); wc != nil {
		if res, ok := tryWorkerExec(ctx, wc, command, cwd, timeoutMs); ok {
			return resultOf(res), nil
		}
		// Worker call failed — log to stderr (caller still gets a
		// result via host fallback). The fallback preserves
		// availability even when the worker container is down.
	}

	res := executeBash(ctx, command, cwd, time.Duration(timeoutMs)*time.Millisecond)
	return resultOf(res), nil
}

// tryWorkerExec attempts to dispatch a Bash command through the
// sandbox-worker. Returns the result + ok=true on success. On
// transport / auth failure it returns ok=false so the caller falls
// back to host execution; this is deliberate — a misconfigured
// worker should not break the operator's tool surface, just log
// and degrade.
func tryWorkerExec(ctx context.Context, wc *worker.Client, command, cwd string, timeoutMs int) (bashResult, bool) {
	resp, err := wc.Exec(ctx, worker.ExecRequest{
		Command:   command,
		Cwd:       cwd,
		TimeoutMs: timeoutMs,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: sandbox-worker exec failed (%v); falling back to host execution\n", err)
		return bashResult{}, false
	}
	return bashResult{
		BaseResult: BaseResult{
			Operation:  "Bash",
			DurationMs: resp.DurationMs,
		},
		Command:  command,
		Stdout:   resp.Stdout,
		Stderr:   resp.Stderr,
		ExitCode: resp.ExitCode,
		TimedOut: resp.TimedOut,
		Cwd:      resp.Cwd,
	}, true
}

// Render satisfies the Renderer contract. Reads like a terminal
// session: prompt+command, body, then a footer with the standard
// "exit · ms · cwd" tail.
func (r bashResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Command)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\n", r.Command)
	if r.Stdout != "" {
		b.WriteString(strings.TrimRight(r.Stdout, "\n"))
		b.WriteByte('\n')
	}
	if r.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(strings.TrimRight(r.Stderr, "\n"))
		b.WriteByte('\n')
	}
	if r.Stdout == "" && r.Stderr == "" {
		b.WriteString("(no output)\n")
	}
	extras := []string{
		fmt.Sprintf("exit %d", r.ExitCode),
		fmt.Sprintf("cwd: %s", r.Cwd),
	}
	if r.TimedOut {
		extras = append(extras, "TIMED OUT")
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// executeBash runs `bash -c command` with a hard timeout. Output captured
// from both pipes is returned even if the process is killed by the timeout.
func executeBash(ctx context.Context, command, cwd string, timeout time.Duration) bashResult {
	if cwd == "" {
		cwd = homeDir()
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", command)
	cmd.Dir = cwd
	applyProcessGroup(cmd)

	start := time.Now()
	stdout, stderr, exitCode, _ := runWithSplitOutput(cmd)
	dur := time.Since(start)

	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)

	return bashResult{
		BaseResult: BaseResult{
			Operation:  "Bash",
			DurationMs: dur.Milliseconds(),
		},
		Command:  command,
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		TimedOut: timedOut,
		Cwd:      cwd,
	}
}
