// Package server starts the clawtool MCP server.
//
// Per ADR-004, clawtool exposes itself as one MCP server over stdio.
// Per ADR-006, core tools use PascalCase names (Bash, Read, Edit, ...).
// This v0.1 prototype registers only Bash; later versions wire the full
// tool registry, source instances, and the ToolSearch primitive.
package server

import (
	"context"
	"fmt"

	"github.com/cogitave/clawtool/internal/tools/core"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/mark3labs/mcp-go/server"
)

// ServeStdio runs clawtool as an MCP server speaking over stdio.
func ServeStdio(ctx context.Context) error {
	s := server.NewMCPServer(
		version.Name,
		version.Version,
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	core.RegisterBash(s)
	core.RegisterGrep(s)
	core.RegisterRead(s)

	if err := server.ServeStdio(s); err != nil {
		return fmt.Errorf("stdio serve: %w", err)
	}
	return nil
}
