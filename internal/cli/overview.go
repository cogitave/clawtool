// `clawtool overview` — one-screen status of the running system
// (UX gap from the #193 smoke pass). Operators wanted a single
// verb that reports daemon + sandbox-worker + agents + bridges
// without remembering five subcommand names.
//
// This deliberately skips diagnostic depth (`clawtool doctor`
// remains the deep checklist). Overview is the at-a-glance
// "is everything wired?" answer.
package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/cogitave/clawtool/internal/sandbox/worker"
	"github.com/cogitave/clawtool/internal/version"
)

const overviewUsage = `Usage: clawtool overview

One-screen status of the running clawtool system: daemon, sandbox
worker, agents, bridges. For diagnostic depth use 'clawtool doctor';
for live tick use 'clawtool dashboard'.
`

func (a *App) runOverview(argv []string) int {
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		fmt.Fprint(a.Stdout, overviewUsage)
		return 0
	}
	w := a.Stdout
	fmt.Fprintf(w, "clawtool %s\n\n", version.Resolved())

	// Daemon
	st, _ := daemon.ReadState()
	switch {
	case st == nil:
		fmt.Fprintln(w, "daemon          ✗  not running       (clawtool daemon start)")
	case daemon.IsRunning(st):
		fmt.Fprintf(w, "daemon          ✓  pid %-7d at %s\n", st.PID, st.URL())
	default:
		fmt.Fprintf(w, "daemon          ⚠  stale state file  (clawtool daemon restart)\n")
	}

	// Sandbox worker
	cfg, _ := config.LoadOrDefault(a.Path())
	mode := cfg.SandboxWorker.Mode
	switch {
	case mode == "" || mode == "off":
		fmt.Fprintln(w, "sandbox-worker  ·  mode=off          (host execution; flip [sandbox_worker] mode to opt in)")
	case cfg.SandboxWorker.URL == "":
		fmt.Fprintf(w, "sandbox-worker  ⚠  mode=%s URL empty\n", mode)
	default:
		ok := pingWorker(cfg)
		if ok {
			fmt.Fprintf(w, "sandbox-worker  ✓  mode=%s url=%s\n", mode, cfg.SandboxWorker.URL)
		} else {
			fmt.Fprintf(w, "sandbox-worker  ⚠  mode=%s url=%s (unreachable)\n", mode, cfg.SandboxWorker.URL)
		}
	}

	fmt.Fprintln(w)

	// Agents — quick row per detected adapter.
	fmt.Fprintln(w, "agents:")
	for _, ad := range agents.Registry {
		s, err := ad.Status()
		if err != nil {
			fmt.Fprintf(w, "  ⚠ %-14s %v\n", ad.Name(), err)
			continue
		}
		switch {
		case !s.Detected:
			fmt.Fprintf(w, "  ·  %-14s not detected\n", ad.Name())
		case s.Detected && s.Claimed:
			label := "claimed"
			if len(s.DisabledByUs) > 0 {
				label = strings.Join(s.DisabledByUs, ",")
			}
			if len(label) > 32 {
				label = label[:29] + "…"
			}
			fmt.Fprintf(w, "  ✓  %-14s %s\n", ad.Name(), label)
		default:
			fmt.Fprintf(w, "  ·  %-14s detected, NOT claimed (clawtool agents claim %s)\n", ad.Name(), ad.Name())
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "(use 'clawtool doctor' for the full diagnostic, 'clawtool dashboard' for a live tick)")
	return 0
}

// pingWorker is a 1.5s probe — short enough to keep `overview`
// fast, long enough to catch local network hiccups.
func pingWorker(cfg config.Config) bool {
	tokenPath := cfg.SandboxWorker.TokenFile
	if tokenPath == "" {
		tokenPath = worker.DefaultTokenPath()
	}
	tok, err := worker.LoadToken(tokenPath)
	if err != nil {
		return false
	}
	c := worker.NewClient(cfg.SandboxWorker.URL, tok)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	return c.Ping(ctx) == nil
}
