// Command stub-server is a tiny MCP server used only by clawtool's e2e
// tests. It implements one tool, `echo`, that returns its input prefixed
// with "echo:" so the e2e can verify routing through clawtool's source
// proxy works end-to-end.
//
// We deliberately use the same mark3labs/mcp-go server library that
// clawtool itself uses; that way a shape change in the library would
// fail both clawtool and the stub at the same time, surfacing the
// breakage at e2e time.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer(
		"stub",
		"0.0.1",
		server.WithToolCapabilities(true),
	)

	echo := mcp.NewTool(
		"echo",
		mcp.WithDescription("Echo input back, prefixed with echo:."),
		mcp.WithString("text", mcp.Required(),
			mcp.Description("Text to echo back.")),
	)
	s.AddTool(echo, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := req.RequireString("text")
		if err != nil {
			return mcp.NewToolResultError("missing text"), nil
		}
		return mcp.NewToolResultText("echo:" + text), nil
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "stub-server: %v\n", err)
		os.Exit(1)
	}
}
