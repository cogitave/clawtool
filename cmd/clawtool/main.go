// Command clawtool is the canonical tool layer for AI coding agents.
//
// See wiki/decisions/004 onward for the architectural direction and
// wiki/decisions/005 for positioning. v0.2 wires config + CLI subcommands
// on top of the v0.1 stdio MCP server.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/cli"
	"github.com/cogitave/clawtool/internal/server"
	"github.com/cogitave/clawtool/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		// Same usage that the CLI prints; reuse it for consistency.
		return cli.New().Run(nil)
	}

	switch argv[0] {
	case "serve":
		if err := server.ServeStdio(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "clawtool: serve failed: %v\n", err)
			return 1
		}
		return 0
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return 0
	default:
		return cli.New().Run(argv)
	}
}
