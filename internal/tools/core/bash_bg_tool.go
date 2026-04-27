// Package core — MCP surface for Bash background tasks. The
// underlying registry is in bash_bg.go; this file is the wiring
// layer mapping {BashOutput, BashKill} onto Get/Kill helpers and
// rendering the snapshot under the standard core-tool envelope.
package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// bashTaskResult wraps a BashTaskSnapshot under BaseResult so the
// snapshot ships with the same operation/duration_ms framing every
// other core tool emits.
type bashTaskResult struct {
	BaseResult
	BashTaskSnapshot
}

func (r bashTaskResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Command)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s &\n", r.Command)
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
		b.WriteString("(no output yet)\n")
	}
	extras := []string{
		fmt.Sprintf("task: %s", r.ID),
		fmt.Sprintf("status: %s", r.Status),
	}
	if string(r.Status) != "active" {
		extras = append(extras, fmt.Sprintf("exit %d", r.ExitCode))
	}
	if r.TimedOut {
		extras = append(extras, "TIMED OUT")
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// RegisterBashOutput exposes GetBashTask over MCP as BashOutput.
func RegisterBashOutput(s *server.MCPServer) {
	tool := mcp.NewTool(
		"BashOutput",
		mcp.WithDescription(
			"Snapshot of a background Bash task: live stdout, stderr, status "+
				"(active / done / failed / cancelled), and exit_code once terminal. "+
				"Pair with `Bash background=true` for fire-and-forget execution.",
		),
		mcp.WithString("task_id",
			mcp.Required(),
			mcp.Description("The task_id returned by `Bash background=true`."),
		),
	)

	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("task_id")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: task_id"), nil
		}
		snap, ok := GetBashTask(id)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("no background bash task: %s", id)), nil
		}
		return resultOf(bashTaskResult{
			BaseResult:       BaseResult{Operation: "BashOutput"},
			BashTaskSnapshot: snap,
		}), nil
	})
}

// RegisterBashKill exposes KillBashTask over MCP as BashKill. The
// snapshot is returned post-cancel so the caller sees the terminal
// status (or `cancelled` if the kill won the race against a quick
// exit).
func RegisterBashKill(s *server.MCPServer) {
	tool := mcp.NewTool(
		"BashKill",
		mcp.WithDescription(
			"Cancel a background Bash task. Sends SIGKILL to the whole "+
				"process group (children too). No-op when the task is already "+
				"terminal. Returns the task's snapshot post-kill.",
		),
		mcp.WithString("task_id",
			mcp.Required(),
			mcp.Description("The task_id returned by `Bash background=true`."),
		),
	)

	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("task_id")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: task_id"), nil
		}
		snap, ok := KillBashTask(id)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("no background bash task: %s", id)), nil
		}
		return resultOf(bashTaskResult{
			BaseResult:       BaseResult{Operation: "BashKill"},
			BashTaskSnapshot: snap,
		}), nil
	})
}
