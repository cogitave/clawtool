// Package core — SourceRegistry MCP tool.
//
// Mirrors the `clawtool source registry [--limit N] [--url URL]
// [--json]` CLI verb (commit cdecb8c) on the MCP transport. An
// agent (typically running as a clawtool MCP client) can probe
// the official MCP Registry directly — no shell-out, no
// CLAWTOOL_PORTAL or token plumbing needed because the registry
// is anonymous read-only.
//
// Use case: an installer/bootstrap agent wants to discover what
// MCP servers exist in the upstream ecosystem before issuing
// `source add` against the embedded catalog. The CLI verb
// requires `Bash clawtool source registry --json` round-trips;
// the MCP tool short-circuits.
//
// Read-only; no side-effects. The registry endpoint is the
// official `https://registry.modelcontextprotocol.io` host by
// default; agents can override via `url` for testing or private
// mirrors.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/catalog"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// sourceRegistryMCPEntry is the structured per-server payload.
// Field shape matches the CLI's RegistryResult.Servers entries
// verbatim (catalog.RegistryServer) — that's the documented
// wire contract. Defined here rather than re-exporting the
// catalog struct because the MCP wire convention prefers a
// JSON-tagged shape under our own package.
type sourceRegistryMCPEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

// sourceRegistryResult is the tool's full response: BaseResult
// metadata + the resolved registry URL the probe actually hit
// + the count + the array of server projections.
type sourceRegistryResult struct {
	BaseResult
	URL     string                   `json:"url"`
	Count   int                      `json:"count"`
	Servers []sourceRegistryMCPEntry `json:"servers"`
}

// Render is the human-readable banner. Mirrors the CLI verb's
// shape so an agent reading content[].text gets the same prose
// the operator sees in their terminal.
func (r sourceRegistryResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("%d server(s) — %s", r.Count, r.URL)))
	b.WriteByte('\n')
	for _, s := range r.Servers {
		ver := s.Version
		if ver == "" {
			ver = "(no version)"
		}
		fmt.Fprintf(&b, "  %s [%s]\n", s.Name, ver)
		if d := strings.TrimSpace(s.Description); d != "" {
			fmt.Fprintf(&b, "    %s\n", d)
		}
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterSourceRegistryTool adds the `SourceRegistry` MCP tool
// to s. Wired from manifest.go via registry.ToolSpec.Register so
// the surface-drift test catches missing routing-rows /
// allowed-tools entries automatically.
func RegisterSourceRegistryTool(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"SourceRegistry",
			mcp.WithDescription(
				"Probe the official MCP Registry "+
					"(registry.modelcontextprotocol.io by default) and "+
					"return the first N server entries. Returns "+
					"{url, count, servers: [{name, description, version}]}. "+
					"Same shape as `clawtool source registry --json`. "+
					"Read-only; no auth required (the registry is "+
					"anonymous). Pass `limit` (1..50, default 10) to "+
					"control page size; pass `url` to override the "+
					"registry base URL for tests / private mirrors.",
			),
			mcp.WithNumber("limit",
				mcp.Description("Max servers to fetch (1..50, default 10).")),
			mcp.WithString("url",
				mcp.Description("Registry base URL. Defaults to the official MCP Registry.")),
		),
		runSourceRegistry,
	)
}

// runSourceRegistry is the MCP handler. Mirrors runSourceRegistry
// in internal/cli/source.go — same probe call, same URL clamp,
// just packaged as a structured tool result instead of a process
// exit code + stdout banner.
func runSourceRegistry(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := sourceRegistryResult{
		BaseResult: BaseResult{Operation: "SourceRegistry", Engine: "registry"},
	}
	defer func() { out.DurationMs = time.Since(start).Milliseconds() }()

	limit := int(req.GetFloat("limit", 10))
	url := strings.TrimSpace(req.GetString("url", ""))

	res, err := catalog.ProbeRegistry(ctx, url, limit)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}

	out.URL = res.BaseURL
	out.Count = res.Count
	out.Servers = make([]sourceRegistryMCPEntry, 0, len(res.Servers))
	for _, s := range res.Servers {
		out.Servers = append(out.Servers, sourceRegistryMCPEntry{
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
		})
	}
	return resultOf(out), nil
}
