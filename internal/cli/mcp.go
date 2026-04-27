// Package cli — `clawtool mcp` subcommand surface (ADR-019).
//
// MCP authoring scaffolder. v0.16.4 ships the surface stub + the
// `clawtool mcp list` read-only verb, which is fully implementable
// today (walks .clawtool/mcp.toml markers under cwd). The
// generator (`new` / `build` / `run` / `install`) lands in v0.17
// per the ADR; until then those verbs surface a clear deferred-
// feature error, the same pattern used for `portal ask` in v0.16.1.
//
// Why ship the stub now: the noun + CLI shape + MCP tool names
// (`McpNew` / `McpList` / …) need to land before agents start
// using them. Booking the namespace is cheap; rewriting it post
// adoption is not.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const mcpUsage = `Usage:
  clawtool mcp new <project-name> [--output <dir>] [--yes]
                                   Generate a new MCP server (Go / Python /
                                   TypeScript) in <project-name>/. Wizard
                                   asks for description, language, transport,
                                   packaging, first tool. ADR-019 — generator
                                   ships v0.17.
  clawtool mcp list [--root <dir>] List MCP server projects under <dir>
                                   (default cwd). Detects via the
                                   .clawtool/mcp.toml marker.
  clawtool mcp run <path>          Start the project's MCP server in dev
                                   mode (stdio). Defers to v0.17.
  clawtool mcp build <path>        Compile / package the project. Defers
                                   to v0.17.
  clawtool mcp install <path> [--as <instance>]
                                   Build + register the project as
                                   [sources.<instance>] in config.toml.
                                   Defers to v0.17.

Sister surface: clawtool skill (Agent Skills, agentskills.io).
mcp = MCP server source code; skill = agent-side skill folder.

Full design: docs/mcp-authoring.md (lands with the v0.17 generator)
and wiki/decisions/019-mcp-authoring-scaffolder.md (accepted).
`

// runMcp is wired from cli.go's main switch. v0.16.4 implements
// `list` natively + leaves the other verbs for v0.17.
func (a *App) runMcp(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, mcpUsage)
		return 2
	}
	switch argv[0] {
	case "new":
		return a.deferredMcpVerb("new", argv[1:])
	case "list":
		return dispatchPlainErr(a.Stderr, "mcp list", a.McpList(argv[1:]))
	case "run":
		return a.deferredMcpVerb("run", argv[1:])
	case "build":
		return a.deferredMcpVerb("build", argv[1:])
	case "install":
		return a.deferredMcpVerb("install", argv[1:])
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, mcpUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool mcp: unknown subcommand %q\n\n%s", argv[0], mcpUsage)
		return 2
	}
}

// dispatchPlainErr is a tiny helper so error printing is uniform
// across the new verbs. Not promoted to a package helper because
// the existing `dispatchPortalErr` already has its own shape.
func dispatchPlainErr(stderr io.Writer, verb string, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "clawtool %s: %v\n", verb, err)
	return 1
}

// McpNotImplementedError is the deferred-feature sentinel surfaced
// by `mcp new / run / build / install` until v0.17 lands the
// generator. Pattern matches `portal.AskNotImplementedError`.
var McpNotImplementedError = errors.New(
	"mcp <verb>: generator lands in v0.17 — see ADR-019 (wiki/decisions/019-mcp-authoring-scaffolder.md) for the full design",
)

func (a *App) deferredMcpVerb(verb string, _ []string) int {
	fmt.Fprintf(a.Stderr, "clawtool mcp %s: %v\n", verb, McpNotImplementedError)
	fmt.Fprintln(a.Stderr, "")
	fmt.Fprintln(a.Stderr, "Today: scaffold by hand using mcp-go / FastMCP / @modelcontextprotocol/sdk.")
	fmt.Fprintln(a.Stderr, "Once built, register via `clawtool source add <name> --command \"...\"` —")
	fmt.Fprintln(a.Stderr, "this is exactly the path `mcp install` will write a shortcut for in v0.17.")
	return 1
}

// ── mcp list (read-only, ships now) ──────────────────────────────

// McpList walks the given root (default cwd) for projects bearing
// a `.clawtool/mcp.toml` marker. v0.16.4 ships an empty walker —
// no markers exist yet because v0.17 will write them. The
// command is wired today so the surface is complete; the walker
// upgrades transparently when generated artifacts arrive.
func (a *App) McpList(argv []string) error {
	root := "."
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--root" && i+1 < len(argv) {
			root = argv[i+1]
			i++
		}
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	// Walk implementation deferred until v0.17 — we don't want
	// to ship a half-baked recursive scan that misbehaves on
	// huge trees. For now, surface the surface contract.
	fmt.Fprintln(a.Stdout, "(no MCP server projects detected — `clawtool mcp new <name>` lands in v0.17)")
	fmt.Fprintf(a.Stdout, "  search root: %s\n", root)
	fmt.Fprintln(a.Stdout, "  marker:      <project>/.clawtool/mcp.toml")
	return nil
}
