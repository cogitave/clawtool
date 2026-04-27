// Package core — Mcp* MCP tools (ADR-019). Surface stub for
// v0.16.4: `McpList` ships read-only; `McpNew` / `McpBuild` /
// `McpRun` / `McpInstall` surface a deferred-feature error so
// the noun + tool names register today (agents discover them via
// ToolSearch and don't have to relearn the surface in v0.17).
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type mcpListResult struct {
	BaseResult
	Projects []string `json:"projects"`
	Root     string   `json:"root"`
}

func (r mcpListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	if len(r.Projects) == 0 {
		b.WriteString("(no MCP server projects detected — `clawtool mcp new <name>` lands in v0.17)\n")
	} else {
		fmt.Fprintf(&b, "%d MCP project(s)\n\n", len(r.Projects))
		for _, p := range r.Projects {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	fmt.Fprintf(&b, "  search root: %s\n", r.Root)
	fmt.Fprintln(&b, "  marker:      <project>/.clawtool/mcp.toml")
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type mcpDeferredResult struct {
	BaseResult
	Verb string `json:"verb"`
}

func (r mcpDeferredResult) Render() string { return r.ErrorLine("Mcp" + r.Verb) }

// RegisterMcpTools wires the Mcp* surface. Always-on; the deferred
// verbs surface an error path so the catalog stays complete.
func RegisterMcpTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"McpList",
			mcp.WithDescription(
				"List MCP server projects under the given root (default cwd). "+
					"A project is detected via the `.clawtool/mcp.toml` marker "+
					"the v0.17 generator writes. v0.16.4 ships an empty walker; "+
					"the walker upgrades transparently once the generator lands. "+
					"ADR-019 — wiki/decisions/019-mcp-authoring-scaffolder.md.",
			),
			mcp.WithString("root",
				mcp.Description("Search root path. Defaults to the server's cwd.")),
		),
		runMcpList,
	)

	for _, verb := range []string{"New", "Run", "Build", "Install"} {
		boundVerb := verb
		s.AddTool(
			mcp.NewTool(
				"Mcp"+verb,
				mcp.WithDescription(
					"clawtool MCP server scaffolder — `"+strings.ToLower(verb)+
						"` verb (ADR-019). Generator lands in v0.17; v0.16.4 surfaces "+
						"the tool name so models discover the namespace early. Returns "+
						"a clear deferred-feature error today.",
				),
			),
			func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out := mcpDeferredResult{
					BaseResult: BaseResult{Operation: "Mcp" + boundVerb, Engine: "mcpgen"},
					Verb:       boundVerb,
				}
				out.ErrorReason = errors.New(
					"Mcp" + boundVerb + ": generator lands in v0.17 — see ADR-019 (wiki/decisions/019-mcp-authoring-scaffolder.md)",
				).Error()
				return resultOf(out), nil
			},
		)
	}
}

func runMcpList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root := strings.TrimSpace(req.GetString("root", "."))
	if root == "" {
		root = "."
	}
	out := mcpListResult{
		BaseResult: BaseResult{Operation: "McpList", Engine: "mcpgen"},
		Root:       root,
	}
	// v0.16.4 walker stub — ships empty so the contract is wired.
	// v0.17 fills in the actual walk against `.clawtool/mcp.toml`.
	return resultOf(out), nil
}
