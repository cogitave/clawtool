// `clawtool daemon` — manage the persistent shared MCP server every
// host (Codex / OpenCode / Gemini / Claude Code) fans into. The
// adapter (internal/agents/mcp_host.go) calls daemon.Ensure under
// the hood when the operator runs `clawtool agents claim <host>`,
// but the CLI exposes the lifecycle directly so the operator can
// start / stop / inspect the daemon without going through claim.
package cli

import (
	"context"
	"fmt"

	"github.com/cogitave/clawtool/internal/daemon"
)

func (a *App) runDaemon(args []string) int {
	if len(args) == 0 {
		a.printDaemonUsage()
		return 0
	}
	switch args[0] {
	case "start":
		return a.runDaemonStart()
	case "stop":
		return a.runDaemonStop()
	case "status":
		return a.runDaemonStatus()
	case "path":
		return a.runDaemonPath()
	case "url":
		return a.runDaemonURL()
	case "restart":
		if rc := a.runDaemonStop(); rc != 0 {
			return rc
		}
		return a.runDaemonStart()
	case "--help", "-h", "help":
		a.printDaemonUsage()
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool daemon: unknown subcommand %q\n", args[0])
		a.printDaemonUsage()
		return 2
	}
}

func (a *App) runDaemonStart() int {
	st, err := daemon.Ensure(context.Background())
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool daemon start: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ daemon ready at %s (pid %d)\n", st.URL(), st.PID)
	fmt.Fprintf(a.Stdout, "  token-file: %s\n", st.TokenFile)
	fmt.Fprintf(a.Stdout, "  log-file:   %s\n", st.LogFile)
	return 0
}

func (a *App) runDaemonStop() int {
	if err := daemon.Stop(); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool daemon stop: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, "✓ daemon stopped")
	return 0
}

func (a *App) runDaemonStatus() int {
	st, err := daemon.ReadState()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool daemon status: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, daemon.FormatStatus(st))
	if st != nil && !daemon.IsRunning(st) {
		return 2 // stale
	}
	return 0
}

func (a *App) runDaemonPath() int {
	fmt.Fprintln(a.Stdout, daemon.StatePath())
	return 0
}

func (a *App) runDaemonURL() int {
	st, err := daemon.ReadState()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool daemon url: %v\n", err)
		return 1
	}
	if st == nil {
		fmt.Fprintln(a.Stderr, "clawtool daemon url: no daemon recorded — run `clawtool daemon start`")
		return 1
	}
	fmt.Fprintln(a.Stdout, st.URL())
	return 0
}

func (a *App) printDaemonUsage() {
	fmt.Fprint(a.Stderr, `Usage: clawtool daemon <subcommand>

Subcommands:
  start      Start the persistent shared MCP server (idempotent — no-op if already healthy).
  stop       SIGTERM the daemon, wait, then SIGKILL if needed; clears state file.
  restart    stop + start.
  status     Report pid / port / health / token / log file.
  path       Print the state-file path.
  url        Print the daemon's MCP URL (http://127.0.0.1:<port>/mcp).

The daemon is the single backend every host (Codex / OpenCode / Gemini /
Claude Code) fans into. One daemon = one BIAM identity = cross-host
notify works. The adapters (clawtool agents claim <host>) call Ensure
under the hood, so explicit start is rarely needed.
`)
}
