// Package core — UnattendedVerify MCP tool. Mirror of the
// `clawtool unattended verify <session_id>` CLI verb so an agent
// can audit a past unattended-mode session for tamper-evidence
// without shelling out. Reads the JSONL audit log, walks every
// {event, sig} row, and verifies the Ed25519 signature against
// the local BIAM identity's public key.
//
// Why a tool, not a script: agents that get woken up on a watch
// event (PR merged, CI failed) frequently want to confirm "the
// log of what I did last time is still intact" before chaining
// new dispatches on top. Forcing them through Bash to invoke the
// CLI surface costs a process-spawn round-trip per check; the
// in-process tool keeps that under a millisecond.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/unattended"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type unattendedVerifyResult struct {
	BaseResult
	SessionID string `json:"session_id"`
	AuditPath string `json:"audit_path"`
	Total     int    `json:"total"`
	Valid     int    `json:"valid"`
	Invalid   int    `json:"invalid"`
	Malformed int    `json:"malformed"`
}

func (r unattendedVerifyResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("UnattendedVerify")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session: %s\n", r.SessionID)
	fmt.Fprintf(&b, "audit:   %s\n", r.AuditPath)
	fmt.Fprintf(&b, "total: %d · valid: %d · invalid: %d · malformed: %d\n",
		r.Total, r.Valid, r.Invalid, r.Malformed)
	if r.Invalid > 0 || r.Malformed > 0 {
		b.WriteString("✗ audit log NOT clean — at least one line failed signature verification or didn't parse\n")
	} else if r.Total > 0 {
		b.WriteString("✓ every line verifies\n")
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterUnattendedVerify wires the UnattendedVerify tool. Idempotent.
func RegisterUnattendedVerify(s *server.MCPServer) {
	tool := mcp.NewTool(
		"UnattendedVerify",
		mcp.WithDescription(
			"Verify the Ed25519 signatures on an unattended-mode session's "+
				"JSONL audit log. Use when auditing a past unattended run for "+
				"tamper-evidence. Args: session_id. Returns: {valid, invalid, "+
				"malformed, total}.",
		),
		mcp.WithString("session_id", mcp.Required(),
			mcp.Description("The unattended session ID — the UUID printed at session start "+
				"and used as the directory name under "+
				"$XDG_DATA_HOME/clawtool/sessions/<id>/.")),
	)

	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, err := req.RequireString("session_id")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: session_id"), nil
		}
		start := time.Now()
		out := unattendedVerifyResult{
			BaseResult: BaseResult{Operation: "UnattendedVerify", Engine: "unattended"},
			SessionID:  sessionID,
		}
		report, err := unattended.VerifySession(sessionID, nil)
		out.AuditPath = report.AuditPath
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Total = report.Total
		out.Valid = report.Valid
		out.Invalid = report.Invalid
		out.Malformed = report.Malformed
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}
