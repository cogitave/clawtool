// Package cli — `clawtool orchestrator` (aliases: dashboard, tui,
// orch). One Bubble Tea program — the orchestrator — fronted by
// four interchangeable verbs because operators reach for whichever
// name they remember. All four routes call this single handler.
//
// Two modes:
//
//	default                interactive Bubble Tea TUI in alt-screen
//	--plain / --once       stdout snapshot for chat-visible pairing
//	                       with the Monitor tool (no TUI)
//
// Pre-v0.22.36 we shipped two distinct programs (dashboard.go +
// orchestrator.go) that both called tui.RunOrchestrator and got
// maintained independently. They drifted, the docstrings disagreed
// on which "is the real one", and operators had to memorise the
// alias-to-program mapping. The single-handler shape replaces all
// of that.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/tui"
)

const orchestratorUsage = `Usage:
  clawtool orchestrator [--plain] [--once]
                                  (aliases: dashboard, tui, orch)

Default mode: live Bubble Tea TUI with three sidebar tabs —
Active dispatches · Done dispatches · Peers (the a2a registry of
every other claude-code / codex / gemini / opencode session this
host knows about). Subscribes to the daemon's watch socket for
real-time updates; polls /v1/peers every 2 s for the Peers tab.

Plain mode: prints task list + agent registry to stdout on a 1 s
cadence. No TUI — pair with the Monitor tool to surface inside
Claude Code's chat. --once exits after a single snapshot.

TUI keys:
  tab / 1 / 2 / 3   switch tab (Active · Done · Peers)
  ↑ / ↓ / k / j     select row (peers cursor on tab 3)
  i                 peek selected peer's inbox into the detail pane
  pgup / pgdn       scroll the detail viewport
  f                 tail-follow toggle
  r                 reconnect to the watch socket
  q / esc           quit
`

// runOrchestrator is the single entry point for the
// dashboard / tui / orchestrator / orch aliases. cli.go's
// dispatcher routes all four to this handler.
func (a *App) runOrchestrator(argv []string) int {
	plain, once := false, false
	for _, arg := range argv {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(a.Stdout, orchestratorUsage)
			return 0
		case "--plain":
			plain = true
		case "--once":
			plain = true
			once = true
		default:
			if strings.HasPrefix(arg, "--") {
				fmt.Fprintf(a.Stderr, "clawtool orchestrator: unknown flag %q\n%s", arg, orchestratorUsage)
				return 2
			}
		}
	}
	if !plain {
		if err := tui.RunOrchestrator(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool orchestrator: %v\n", err)
			return 1
		}
		return 0
	}

	store, err := openBiamStore()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool orchestrator: BIAM store unavailable: %v\n", err)
	}
	if store != nil {
		defer store.Close()
	}
	sup := agents.NewSupervisor()
	return runOrchestratorPlain(a, store, sup, once)
}

// runOrchestratorPlain prints a snapshot of BIAM tasks + agent
// registry to stdout. With `once=true` it exits after the first
// print; otherwise it loops on a 1 s cadence until SIGINT / pipe
// close. Bare ASCII so Monitor-tool pairing renders cleanly inside
// Claude Code's chat.
func runOrchestratorPlain(a *App, store *biam.Store, sup agents.Supervisor, once bool) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	for {
		var tasks []biam.Task
		var agentList []agents.Agent
		if store != nil {
			lc, lcCancel := context.WithTimeout(ctx, 3*time.Second)
			t, err := store.ListTasks(lc, 50)
			lcCancel()
			if err == nil {
				tasks = t
			}
		}
		if sup != nil {
			lc, lcCancel := context.WithTimeout(ctx, 3*time.Second)
			ags, err := sup.Agents(lc)
			lcCancel()
			if err == nil {
				agentList = ags
			}
		}
		_, _ = a.Stdout.Write([]byte(renderPlainSnapshot(tasks, agentList)))
		if once {
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(1 * time.Second):
		}
	}
}

func renderPlainSnapshot(tasks []biam.Task, ags []agents.Agent) string {
	var b strings.Builder
	ts := time.Now().Local().Format("15:04:05")

	var active, done, failed int
	for _, t := range tasks {
		switch t.Status {
		case biam.TaskActive, biam.TaskPending:
			active++
		case biam.TaskDone:
			done++
		case biam.TaskFailed, biam.TaskCancelled, biam.TaskExpired:
			failed++
		}
	}
	callable := 0
	for _, ag := range ags {
		if ag.Callable {
			callable++
		}
	}
	fmt.Fprintf(&b, "[%s] dispatches=%d (active=%d done=%d failed=%d) · agents callable=%d/%d\n",
		ts, len(tasks), active, done, failed, callable, len(ags))

	if len(tasks) > 0 {
		b.WriteString("  dispatches:\n")
		max := len(tasks)
		if max > 10 {
			max = 10
		}
		for i := 0; i < max; i++ {
			t := tasks[i]
			short := t.TaskID
			if len(short) > 8 {
				short = short[:8]
			}
			last := strings.ReplaceAll(t.LastMessage, "\n", " ")
			if len(last) > 50 {
				last = last[:50] + "…"
			}
			fmt.Fprintf(&b, "    %-9s %-10s %s · %s\n",
				string(t.Status), short, t.Agent, last)
		}
		if len(tasks) > 10 {
			fmt.Fprintf(&b, "    (…%d more — `clawtool task list` for the full list)\n", len(tasks)-10)
		}
	}

	if len(ags) > 0 {
		b.WriteString("  agents:\n")
		for _, ag := range ags {
			callableMark := "✗"
			if ag.Callable {
				callableMark = "✓"
			}
			sb := ag.Sandbox
			if sb == "" {
				sb = "—"
			}
			fmt.Fprintf(&b, "    %s %-15s %-10s sandbox=%s\n",
				callableMark, ag.Instance, ag.Family, sb)
		}
	}
	return b.String()
}
