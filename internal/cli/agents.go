package cli

import (
	"encoding/json"
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
		return a.runAgentsList(argv[1:])
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
	fs := flag.NewFlagSet("agents status", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asJSON := fs.Bool("json", false, "Emit machine-readable JSON instead of the human table.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) > 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool agents status [<agent>] [--json]\n")
		return 2
	}

	// Build the slice of statuses we'll surface — single-adapter
	// when the operator named one, otherwise every registered
	// adapter. Errors are non-fatal; we fall back to a Notes line
	// inside the row so a single broken adapter doesn't sink the
	// whole report.
	var rows []agents.Status
	if len(rest) == 1 {
		adapter, err := agents.Find(rest[0])
		if err != nil {
			return a.agentNotFound(rest[0])
		}
		s, err := adapter.Status()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool agents status: %v\n", err)
			return 1
		}
		rows = []agents.Status{s}
	} else {
		for _, adp := range agents.Registry {
			s, err := adp.Status()
			if err != nil {
				fmt.Fprintf(a.Stderr, "agents status %s: %v\n", adp.Name(), err)
				continue
			}
			rows = append(rows, s)
		}
	}

	if *asJSON {
		// Stable indented JSON so curl|jq scripts can pipe it
		// directly. Trailing newline matches the human path.
		body, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool agents status: marshal: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}

	fmt.Fprintln(a.Stdout, "AGENT          DETECTED  CLAIMED  TOOLS DISABLED BY CLAWTOOL")
	for _, s := range rows {
		a.renderStatusRow(s)
	}
	return 0
}

// agentListEntry is the JSON shape produced by `agents list --json`.
// Kept distinct from agents.Status so list stays light (no settings
// path / claimed-tools array) — operators piping list into a script
// usually only want "what adapters exist + are they on this host?".
type agentListEntry struct {
	Name     string `json:"name"`
	Detected bool   `json:"detected"`
}

func (a *App) runAgentsList(argv []string) int {
	fs := flag.NewFlagSet("agents list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asJSON := fs.Bool("json", false, "Emit machine-readable JSON instead of the human list.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{})); err != nil {
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprint(a.Stderr, "usage: clawtool agents list [--json]\n")
		return 2
	}

	// Build a stable name-sorted slice of every registered adapter
	// + its detection bit. Same data both branches use; only the
	// rendering differs.
	entries := make([]agentListEntry, 0, len(agents.Registry))
	for _, adp := range agents.Registry {
		entries = append(entries, agentListEntry{
			Name:     adp.Name(),
			Detected: adp.Detected(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if *asJSON {
		// Always emit a JSON array (possibly empty) — uniform
		// shape lets `jq '.[].name'` work even when no adapters
		// are registered.
		body, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool agents list: marshal: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}

	if len(entries) == 0 {
		fmt.Fprintln(a.Stdout, "(no agent adapters registered)")
		return 0
	}
	fmt.Fprintln(a.Stdout, "Known agent adapters:")
	for _, e := range entries {
		fmt.Fprintf(a.Stdout, "  %s\n", e.Name)
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

  clawtool agents status [<agent>] [--json]
                              Show what's claimed across every adapter,
                              or a single adapter when <agent> is given.
                              --json emits machine-readable output for
                              shell pipelines (jq, etc.).

  clawtool agents list [--json]
                              Print known agent adapters. --json
                              emits machine-readable output:
                              [{"name":"claude-code","detected":true}].

Known agents (v0.8.4): claude-code.
`
