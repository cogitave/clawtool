// Package core — AgentDetect MCP tool.
//
// Mirrors the `clawtool agents detect <agent>` CLI verb (commit
// ef4c698) on the MCP transport. Returns the same structured
// snapshot — {adapter, detected, claimed, exit_code} — so MCP
// clients can probe a host adapter without shelling out to the
// CLI.
//
// Use case: an LLM running inside one host (e.g. claude-code)
// wants to verify clawtool's adapter is configured + claimed
// before issuing dispatch commands. Today that requires either
// shelling out via `Bash clawtool agents detect …` or reading
// `agents status --json`. This tool short-circuits both.
//
// Read-only; no side-effects. Same exit-code contract as the CLI:
//
//	0 — adapter detected on this host AND claimed by clawtool
//	1 — adapter detected, NOT claimed (or transient Status err)
//	2 — adapter NOT detected on this host
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// agentDetectResult is the structured payload returned by the
// AgentDetect tool. snake_case JSON tags follow the project-wide
// convention (mirrors agents.Status / BuildInfo).
type agentDetectResult struct {
	BaseResult
	Adapter  string `json:"adapter"`
	Detected bool   `json:"detected"`
	Claimed  bool   `json:"claimed"`
	ExitCode int    `json:"exit_code"`
	// StatusErr captures a transient adapter.Status() failure
	// when probing — the field is omitempty so the success path
	// stays clean. Distinct from BaseResult.ErrorReason which
	// reflects "the tool itself errored" (e.g. unknown agent).
	StatusErr string `json:"status_error,omitempty"`
}

// Render produces a short human banner that mirrors the CLI verb's
// phrasing. Useful when the MCP client surfaces tool output
// directly to the operator (chat UI fallback when StructuredContent
// isn't introspected).
func (r agentDetectResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Adapter)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(r.Adapter))
	b.WriteByte('\n')
	switch r.ExitCode {
	case 0:
		fmt.Fprintf(&b, "  ✓ detected and claimed by clawtool (exit 0)\n")
	case 1:
		if r.StatusErr != "" {
			fmt.Fprintf(&b, "  detected; status check errored: %s (exit 1)\n", r.StatusErr)
		} else {
			fmt.Fprintf(&b, "  detected but NOT claimed — run `clawtool agents claim %s` (exit 1)\n", r.Adapter)
		}
	default:
		fmt.Fprintf(&b, "  not detected on this host (exit 2)\n")
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterAgentDetectTool adds the `AgentDetect` MCP tool to s.
// Wired from manifest.go via registry.ToolSpec.Register so the
// surface-drift test catches missing routing-rows / allowed-tools
// entries automatically.
func RegisterAgentDetectTool(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"AgentDetect",
			mcp.WithDescription(
				"Probe one host AI coding agent (claude-code, codex, gemini, "+
					"opencode) and report whether it's installed on this host AND "+
					"whether clawtool has claimed its native tools. Use when the "+
					"operator says \"is codex set up?\" or before SendMessage to "+
					"verify the target adapter is reachable. Returns `detected`, "+
					"`claimed`, and an `exit_code` matching the `clawtool agents "+
					"detect` CLI: 0=detected+claimed, 1=detected-not-claimed (run "+
					"`clawtool agents claim`), 2=not-detected. NOT for "+
					"enumerating every registered adapter — use AgentList. "+
					"Read-only.",
			),
			mcp.WithString("agent", mcp.Required(),
				mcp.Description("Adapter name (e.g. 'claude-code'). Use AgentList to enumerate registered adapters.")),
		),
		runAgentDetect,
	)
}

// runAgentDetect is the MCP handler. Mirrors runAgentsDetect in
// internal/cli/agents.go — same classification logic, same exit
// code semantics, just packaged as a structured tool result
// instead of a process exit code.
func runAgentDetect(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := agentDetectResult{
		BaseResult: BaseResult{Operation: "AgentDetect", Engine: "agents"},
	}
	defer func() { out.DurationMs = time.Since(start).Milliseconds() }()

	name, err := req.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: agent"), nil
	}
	out.Adapter = name

	adapter, err := agents.Find(name)
	if err != nil {
		out.ErrorReason = fmt.Sprintf("unknown agent %q", name)
		return resultOf(out), nil
	}

	s, statusErr := adapter.Status()
	switch {
	case statusErr != nil:
		out.Detected = s.Detected
		out.Claimed = s.Claimed
		out.StatusErr = statusErr.Error()
		out.ExitCode = 1
	case !s.Detected:
		out.ExitCode = 2
	case !s.Claimed:
		out.Detected = true
		out.ExitCode = 1
	default:
		out.Detected = true
		out.Claimed = true
		out.ExitCode = 0
	}
	return resultOf(out), nil
}
