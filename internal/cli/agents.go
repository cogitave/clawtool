package cli

import (
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/agents"
)

// runAgents dispatches `clawtool agents …` subcommands.
func (a *App) runAgents(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, agentsUsage)
		return 2
	}
	switch argv[0] {
	case "claim":
		return a.runAgentsClaim(argv[1:])
	case "release":
		return a.runAgentsRelease(argv[1:])
	case "status":
		return a.runAgentsStatus(argv[1:])
	case "list":
		return a.runAgentsList()
	default:
		fmt.Fprintf(a.Stderr, "clawtool agents: unknown subcommand %q\n\n%s", argv[0], agentsUsage)
		return 2
	}
}

func (a *App) runAgentsClaim(argv []string) int {
	fs := flag.NewFlagSet("agents claim", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	dryRun := fs.Bool("dry-run", false, "Print the diff without writing.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool agents claim <agent> [--dry-run]\n")
		return 2
	}
	adapter, err := agents.Find(rest[0])
	if err != nil {
		return a.agentNotFound(rest[0])
	}
	plan, err := adapter.Claim(agents.Options{DryRun: *dryRun})
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool agents claim: %v\n", err)
		return 1
	}
	a.renderPlan(plan)
	return 0
}

func (a *App) runAgentsRelease(argv []string) int {
	fs := flag.NewFlagSet("agents release", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	dryRun := fs.Bool("dry-run", false, "Print the diff without writing.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool agents release <agent> [--dry-run]\n")
		return 2
	}
	adapter, err := agents.Find(rest[0])
	if err != nil {
		return a.agentNotFound(rest[0])
	}
	plan, err := adapter.Release(agents.Options{DryRun: *dryRun})
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool agents release: %v\n", err)
		return 1
	}
	a.renderPlan(plan)
	return 0
}

func (a *App) runAgentsStatus(argv []string) int {
	if len(argv) == 0 {
		// Status across every registered adapter.
		fmt.Fprintln(a.Stdout, "AGENT          DETECTED  CLAIMED  TOOLS DISABLED BY CLAWTOOL")
		for _, adp := range agents.Registry {
			s, err := adp.Status()
			if err != nil {
				fmt.Fprintf(a.Stderr, "agents status %s: %v\n", adp.Name(), err)
				continue
			}
			a.renderStatusRow(s)
		}
		return 0
	}
	if len(argv) > 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool agents status [<agent>]\n")
		return 2
	}
	adapter, err := agents.Find(argv[0])
	if err != nil {
		return a.agentNotFound(argv[0])
	}
	s, err := adapter.Status()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool agents status: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, "AGENT          DETECTED  CLAIMED  TOOLS DISABLED BY CLAWTOOL")
	a.renderStatusRow(s)
	return 0
}

func (a *App) runAgentsList() int {
	if len(agents.Registry) == 0 {
		fmt.Fprintln(a.Stdout, "(no agent adapters registered)")
		return 0
	}
	fmt.Fprintln(a.Stdout, "Known agent adapters:")
	names := make([]string, 0, len(agents.Registry))
	for _, adp := range agents.Registry {
		names = append(names, adp.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(a.Stdout, "  %s\n", n)
	}
	return 0
}

func (a *App) agentNotFound(name string) int {
	fmt.Fprintf(a.Stderr, "clawtool agents: unknown agent %q\n", name)
	fmt.Fprintln(a.Stderr, "  known adapters:")
	for _, adp := range agents.Registry {
		fmt.Fprintf(a.Stderr, "    %s\n", adp.Name())
	}
	return 1
}

func (a *App) renderPlan(p agents.Plan) {
	prefix := "✓"
	if p.DryRun {
		prefix = "(dry-run)"
	}

	if p.WasNoop {
		fmt.Fprintf(a.Stdout, "%s no changes needed for %s (%s)\n", prefix, p.Adapter, p.Action)
		return
	}

	pastTense := map[string]string{
		"claim":   "claimed",
		"release": "released",
	}[p.Action]
	if pastTense == "" {
		pastTense = p.Action
	}
	fmt.Fprintf(a.Stdout, "%s %s %s\n", prefix, pastTense, p.Adapter)
	fmt.Fprintf(a.Stdout, "  settings: %s\n", p.SettingsPath)
	fmt.Fprintf(a.Stdout, "  marker:   %s\n", p.MarkerPath)
	if len(p.ToolsAdded) > 0 {
		fmt.Fprintf(a.Stdout, "  + disabled in settings: %s\n", strings.Join(p.ToolsAdded, ", "))
	}
	if len(p.ToolsRemoved) > 0 {
		fmt.Fprintf(a.Stdout, "  - re-enabled in settings: %s\n", strings.Join(p.ToolsRemoved, ", "))
	}
	if !p.DryRun {
		switch p.Action {
		case "claim":
			fmt.Fprintf(a.Stdout, "  undo: clawtool agents release %s\n", p.Adapter)
		case "release":
			fmt.Fprintf(a.Stdout, "  redo: clawtool agents claim %s\n", p.Adapter)
		}
	}
}

func (a *App) renderStatusRow(s agents.Status) {
	det := "no"
	if s.Detected {
		det = "yes"
	}
	cl := "no"
	if s.Claimed {
		cl = "yes"
	}
	tools := strings.Join(s.DisabledByUs, ", ")
	if tools == "" {
		tools = "(none)"
	}
	fmt.Fprintf(a.Stdout, "%-14s %-9s %-8s %s\n", s.Adapter, det, cl, tools)
	if s.Notes != "" {
		fmt.Fprintf(a.Stdout, "  note: %s\n", s.Notes)
	}
}

const agentsUsage = `Usage:
  clawtool agents claim <agent> [--dry-run]
                              Disable the host agent's native tools that
                              clawtool replaces (Bash, Read, Edit, Write,
                              Grep, Glob, WebFetch, WebSearch). After
                              this, the agent only sees mcp__clawtool__*
                              equivalents. Reversible with 'release'.

  clawtool agents release <agent> [--dry-run]
                              Re-enable everything 'claim' previously
                              disabled. Idempotent; safe to run twice.

  clawtool agents status [<agent>]
                              Show what's claimed across every adapter,
                              or a single adapter when <agent> is given.

  clawtool agents list        Print known agent adapters.

Known agents (v0.8.4): claude-code.
`
