// Package cli — `clawtool dashboard` (alias `clawtool tui`).
// Two modes:
//
//	default      Bubble Tea orchestrator TUI in alt-screen (interactive)
//	--plain      one-shot or repeat-print to stdout (Monitor-pair / chat-visible)
//
// The plain mode exists for the case where the operator wants
// runtime state visible inside Claude Code's chat (via the native
// Monitor tool) — the TUI's alt-screen rendering doesn't survive
// that path. Plain mode prints task list + agent list with a
// clean header on a 1 s cadence.
//
// Why these aliases all point at the orchestrator: pre-v0.22 we
// shipped two distinct Bubble Tea programs (a simpler dashboard
// and the richer orchestrator). They drifted, both maintained
// independently, and operators never knew which to launch. The
// orchestrator is the production teammate panel; `dashboard` /
// `tui` / `orch` are now three names for the same window.
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

const dashboardUsage = `Usage:
  clawtool dashboard                Launch the orchestrator TUI (Bubble Tea, alt-screen).
  clawtool dashboard --plain        Print snapshots to stdout instead (no TUI).
                                     Pair with Monitor tool to surface in chat.
  clawtool dashboard --once         One-shot snapshot to stdout, then exit (implies --plain).
  clawtool tui                      Alias.
  clawtool orch                     Alias.

Plain-mode sections:
  1. Dispatches  — BIAM tasks (active first, then recent)
  2. Agents      — supervisor's agent registry
  3. Stats       — totals / done / failed / active

TUI keybindings: see ` + "`clawtool orchestrator --help`" + `.
`

func (a *App) runDashboard(argv []string) int {
	plain, once := false, false
	for _, v := range argv {
		switch v {
		case "--help", "-h":
			fmt.Fprint(a.Stdout, dashboardUsage)
			return 0
		case "--plain":
			plain = true
		case "--once":
			plain = true
			once = true
		}
	}
	if !plain {
		// TUI mode — single canonical implementation lives in
		// internal/tui/orchestrator.go. The legacy `tui.Run`
		// program was deleted in this cut; both `dashboard` and
		// `tui` aliases now route here.
		if err := tui.RunOrchestrator(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool dashboard: %v\n", err)
			return 1
		}
		return 0
	}
	store, err := openBiamStore()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool dashboard: BIAM store unavailable: %v\n", err)
	}
	if store != nil {
		defer store.Close()
	}
	sup := agents.NewSupervisor()
	return runDashboardPlain(a, store, sup, once)
}

// runDashboardPlain prints a snapshot of BIAM tasks + agent
// registry to stdout. With `once=true` it exits after the first
// print; otherwise it loops on a 1 s cadence until SIGINT /
// pipe close. Output is bare ASCII so Monitor-tool pairing
// renders cleanly inside Claude Code's chat.
func runDashboardPlain(a *App, store *biam.Store, sup agents.Supervisor, once bool) int {
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

	// Counters first — one-line "stats" header.
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

	// Dispatches section — only print when there's something.
	if len(tasks) > 0 {
		b.WriteString("  dispatches:\n")
		// Cap to 10 rows so a chat-visible snapshot doesn't
		// flood the operator. The full picture is in `task list`.
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

	// Agents section — same rule, only print when populated.
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
