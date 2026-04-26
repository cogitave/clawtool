// Command clawtool is the canonical tool layer for AI coding agents.
//
// See wiki/decisions/004 onward for the architectural direction and
// wiki/decisions/005 for positioning. This is a v0.1 prototype: minimal
// MCP server with a single core tool (Bash) wired up end-to-end.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/server"
	"github.com/cogitave/clawtool/internal/version"
)

const usage = `clawtool — canonical tool layer for AI coding agents

Usage:
  clawtool serve            Run as an MCP server over stdio.
  clawtool version          Print the build version.
  clawtool help             Show this help.

Subcommands wired to no-op stubs in this prototype (v0.1):
  clawtool init             Initialize ~/.config/clawtool/config.toml
  clawtool tools list       List available tools
  clawtool tools enable <selector>
  clawtool tools disable <selector>
  clawtool tools status <selector>
  clawtool source add <name> -- <command...>
  clawtool profile use <name>
  clawtool group create <name> <selectors...>

See ADR-004 for the selector grammar and ADR-006 for naming rules.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		if err := server.ServeStdio(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "clawtool: serve failed: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "clawtool: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
