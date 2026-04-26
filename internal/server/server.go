// Package server starts the clawtool MCP server.
//
// Per ADR-004, clawtool exposes itself as one MCP server over stdio.
// Per ADR-006, core tools use PascalCase names (Bash, Read, Edit, ...).
// Per ADR-008, configured sources spawn as child MCP servers and their
// tools are aggregated under `<instance>__<tool>` wire names.
//
// v0.4 turn 2 wires the source proxy: ServeStdio loads config + secrets,
// builds a sources.Manager, starts each configured source, and registers
// both core tools (filtered by config) AND aggregated source tools on the
// parent server before serving.
package server

import (
	"context"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/sources"
	"github.com/cogitave/clawtool/internal/tools/core"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/mark3labs/mcp-go/server"
)

// ServeStdio runs clawtool as an MCP server speaking over stdio. It blocks
// until stdin closes (the conventional MCP shutdown signal) or an
// unrecoverable error occurs.
func ServeStdio(ctx context.Context) error {
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	sec, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}

	mgr := sources.NewManager(cfg, sec)
	// A source-start failure on any one instance is non-fatal — the manager
	// records the failure for surface and we continue with whichever
	// sources came up. We log to stderr so operators can see what went
	// wrong; we do not surface to the MCP wire (that channel is reserved
	// for protocol traffic).
	if err := mgr.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: some sources failed to start: %v\n", err)
	}
	defer mgr.Stop()

	s := server.NewMCPServer(
		version.Name,
		version.Version,
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	// Core tools, filtered by config.IsEnabled. ADR-005 / ADR-006: agents
	// can disable any core tool and use the agent's native one instead.
	if cfg.IsEnabled("Bash").Enabled {
		core.RegisterBash(s)
	}
	if cfg.IsEnabled("Grep").Enabled {
		core.RegisterGrep(s)
	}
	if cfg.IsEnabled("Read").Enabled {
		core.RegisterRead(s)
	}

	// Aggregated source tools — one entry per (running instance × tool),
	// already named in wire form `<instance>__<tool>`. Each handler closure
	// routes the call to the right child via the manager.
	for _, st := range mgr.AggregatedTools() {
		s.AddTool(st.Tool, st.Handler)
	}

	if err := server.ServeStdio(s); err != nil {
		return fmt.Errorf("stdio serve: %w", err)
	}
	return nil
}
