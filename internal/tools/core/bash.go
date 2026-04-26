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
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultTimeoutMs = 120_000 // 2 minutes
	maxTimeoutMs     = 600_000 // 10 minutes
)

type bashResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	Cwd        string `json:"cwd"`
}

// RegisterBash adds the Bash tool to the given MCP server.
func RegisterBash(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Bash",
		mcp.WithDescription(
			"Run a shell command via /bin/bash. "+
				"Returns structured JSON with stdout, stderr, exit_code, duration_ms, "+
				"timed_out, and cwd. Output is preserved even when the command times out.",
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
	)

	s.AddTool(tool, runBash)
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

	res := executeBash(ctx, command, cwd, time.Duration(timeoutMs)*time.Millisecond)
	body, _ := json.Marshal(res)
	return mcp.NewToolResultText(string(body)), nil
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
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exitCode,
		DurationMs: dur.Milliseconds(),
		TimedOut:   timedOut,
		Cwd:        cwd,
	}
}
