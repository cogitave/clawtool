// Package core — Version MCP tool.
//
// Exposes version.BuildInfo over the MCP transport so a connected
// agent (claude-code, codex, gemini, opencode) can ask "what
// clawtool am I talking to?" without shelling out to
// `clawtool version --json`. Mirrors the HTTP `/v1/health` build
// surface (commit 54bf658) and the CLI `clawtool version --json`
// flag (commit 239eede), so all three wire-protocol consumers see
// identical shape.
//
// Read-only; no side-effects, no auth-sensitive data. Operators
// can call it freely. The tool's only argument-less; the response
// embeds version.BuildInfo via struct embedding so the JSON keys
// (`name`, `version`, `go_version`, `platform`, `commit`,
// `modified`) match the rest of the surface verbatim.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/version"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// versionResult is the structured payload returned by the Version
// tool. Embeds BuildInfo so JSON keys match the project-wide
// snake_case convention (mirrors agents.Status / agentListEntry /
// the /v1/health build object).
type versionResult struct {
	BaseResult
	version.BuildInfo
}

// Render produces the human-readable banner. Errors take the
// canonical "✗ <verb> — <reason>" line; happy path is a small
// header + bulleted facts + footer with timing.
func (r versionResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("%s %s", r.Name, r.Version)))
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  go:        %s\n", r.GoVersion)
	fmt.Fprintf(&b, "  platform:  %s\n", r.Platform)
	if r.Commit != "" {
		modSuffix := ""
		if r.Modified {
			modSuffix = " (modified)"
		}
		fmt.Fprintf(&b, "  commit:    %s%s\n", r.Commit, modSuffix)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterVersionTool adds the `Version` MCP tool to s. Wired
// from manifest.go via the registry.ToolSpec.Register hook so
// the gating behaviour (this tool is always-on / no policy gate)
// stays consistent with the rest of the registry.
func RegisterVersionTool(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"Version",
			mcp.WithDescription(
				"Snapshot of the running clawtool binary's identity: name, "+
					"semver, Go runtime version, GOOS/GOARCH platform, VCS "+
					"commit + modified flag (when the build embedded VCS info). "+
					"Same shape as `clawtool version --json` and the `build` "+
					"field of GET /v1/health. Read-only. Useful for monitoring "+
					"scripts that gate on a minimum version or for diagnosing "+
					"version mismatches across hosts.",
			),
		),
		runVersion,
	)
}

// runVersion is the MCP handler. No arguments, no errors that
// aren't already self-evident from version.Info() — runtime
// metadata is always available.
func runVersion(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := versionResult{
		BaseResult: BaseResult{Operation: "Version", Engine: "version"},
		BuildInfo:  version.Info(),
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}
