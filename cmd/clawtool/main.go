// Command clawtool is the canonical tool layer for AI coding agents.
//
// See wiki/decisions/004 onward for the architectural direction and
// wiki/decisions/005 for positioning. v0.2 wires config + CLI subcommands
// on top of the v0.1 stdio MCP server. v0.11 (ADR-014 Phase 2) extends
// the `serve` subcommand with an HTTP gateway behind --listen.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cogitave/clawtool/internal/cli"
	"github.com/cogitave/clawtool/internal/server"
	"github.com/cogitave/clawtool/internal/version"
)

// rootCtx is the process-wide context every long-running entrypoint
// roots its work under. SIGINT / SIGTERM cancel it, which propagates
// through ServeStdio / ServeHTTP / the runner / cli subcommands so
// deferred cleanup actually runs (HTTP graceful shutdown,
// runner.Stop's WaitGroup join, store.Close, audit-log Close, tmp
// worktree reap). Pre-fix this was context.Background() everywhere
// and Ctrl-C left the daemon mid-write.
var rootCtx context.Context

func main() {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	rootCtx = ctx
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		// Same usage that the CLI prints; reuse it for consistency.
		return cli.New().Run(nil)
	}

	switch argv[0] {
	case "serve":
		return runServe(argv[1:])
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return 0
	default:
		return cli.New().Run(argv)
	}
}

// runServe handles `clawtool serve [stdio|http subcommand]`. Default
// (no flags) keeps the v0.10 behaviour: stdio MCP server. Passing
// --listen mounts the HTTP gateway. `serve init-token` writes a fresh
// listener token and exits.
func runServe(argv []string) int {
	// Subcommand: `clawtool serve init-token [<path>]`.
	if len(argv) >= 1 && argv[0] == "init-token" {
		path := defaultTokenPath()
		if len(argv) >= 2 {
			path = argv[1]
		}
		tok, err := server.InitTokenFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "clawtool: init-token: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "wrote token to %s (chmod 0600). Use it as the bearer in `Authorization: Bearer …`.\n", path)
		// Print to stdout so a script can capture it.
		fmt.Println(tok)
		return 0
	}

	// Otherwise parse --listen / --token-file / --mcp-http flags.
	opts, err := parseServeFlags(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawtool serve: %v\n%s", err, serveUsage)
		return 2
	}

	if opts.Listen == "" {
		// Default path: stdio MCP server.
		if err := server.ServeStdio(rootCtx); err != nil {
			fmt.Fprintf(os.Stderr, "clawtool: serve failed: %v\n", err)
			return 1
		}
		return 0
	}

	if err := server.ServeHTTP(rootCtx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: serve --listen %s failed: %v\n", opts.Listen, err)
		return 1
	}
	return 0
}

func parseServeFlags(argv []string) (server.HTTPOptions, error) {
	opts := server.HTTPOptions{}
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--listen":
			if i+1 >= len(argv) {
				return opts, fmt.Errorf("--listen requires a value (e.g. ':8080')")
			}
			opts.Listen = argv[i+1]
			i++
		case "--token-file":
			if i+1 >= len(argv) {
				return opts, fmt.Errorf("--token-file requires a path")
			}
			opts.TokenFile = argv[i+1]
			i++
		case "--mcp-http":
			opts.MCPHTTP = true
		case "--help", "-h":
			fmt.Fprint(os.Stderr, serveUsage)
			return opts, fmt.Errorf("help requested")
		default:
			return opts, fmt.Errorf("unknown flag %q", v)
		}
	}
	if opts.Listen != "" && opts.TokenFile == "" {
		opts.TokenFile = defaultTokenPath()
	}
	return opts, nil
}

func defaultTokenPath() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "clawtool", "listener-token")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "listener-token"
	}
	return filepath.Join(home, ".config", "clawtool", "listener-token")
}

const serveUsage = `Usage:
  clawtool serve                       Run as an MCP server over stdio (default).
  clawtool serve --listen :8080 [--token-file <path>] [--mcp-http]
                                       Run the HTTP gateway. Token file
                                       defaults to
                                       $XDG_CONFIG_HOME/clawtool/listener-token
                                       (or $HOME/.config/clawtool/...).
                                       Bearer-token auth is mandatory.
  clawtool serve init-token [<path>]   Generate a fresh 32-byte hex token
                                       at <path> (default the same listener-
                                       token path) and print it to stdout.

Endpoints (HTTP gateway):
  GET  /v1/health
  GET  /v1/agents [?status=callable]
  POST /v1/send_message  body: {"instance":"...","prompt":"...","opts":{}}

TLS termination is delegated to a reverse proxy (nginx / caddy /
Cloudflare Tunnel). clawtool listens plaintext on the bound address.
`
