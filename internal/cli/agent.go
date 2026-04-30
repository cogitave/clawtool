package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/agentgen"
	"github.com/cogitave/clawtool/internal/agents"
)

const agentUsage = `Usage:
  Persona scaffolding (user-defined subagents):
    clawtool agent new <name> --description "..." [options]
                                Scaffold a Claude Code subagent definition
                                under ~/.claude/agents/<name>.md (or
                                ./.claude/agents/<name>.md with --local).
    clawtool agent list         Enumerate installed agents under
                                ~/.claude/agents and ./.claude/agents.
    clawtool agent path [<name>]
                                Print the on-disk path of an agent.

  Sticky-default instance routing (legacy noun — pre-dates the agent
  vs instance rename; kept for backward compat):
    clawtool agent use <instance>
                                Set the sticky default instance for this user.
    clawtool agent which        Show the currently-resolved default instance.
    clawtool agent unset        Clear the sticky default.

Options for 'new':
  --description "..."           Required. One-paragraph description.
  --tools "a, b, c"             Optional. Comma-separated tool whitelist.
                                Frontmatter 'tools:' line.
  --instance <name>             Optional. Default clawtool instance this
                                agent dispatches to via SendMessage.
  --model sonnet|haiku|opus     Optional. Frontmatter 'model:' field.
  --user                        Install under ~/.claude/agents/ (default).
  --local                       Install under ./.claude/agents/ instead.
  --force                       Overwrite an existing agent file.
`

// runAgent (singular) is the new dispatcher for the relay-related
// runtime commands. The pre-existing 'agents' (plural) subcommand
// continues to handle Claim / Release / List per ADR-011 — the two
// remain disjoint nouns, matching ADR-014's two-noun split (bridge =
// install, agent = runtime, agents = adapter ownership for native
// tool replacement).
func (a *App) runAgent(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, agentUsage)
		return 2
	}
	switch argv[0] {
	case "new":
		return a.runAgentNew(argv[1:])
	case "list":
		return a.runAgentList(argv[1:])
	case "path":
		return a.runAgentPath(argv[1:])
	case "use":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool agent use <instance>\n")
			return 2
		}
		if err := a.AgentUse(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool agent use: %v\n", err)
			return 1
		}
	case "which":
		if err := a.AgentWhich(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool agent which: %v\n", err)
			return 1
		}
	case "unset":
		if err := a.AgentUnset(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool agent unset: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool agent: unknown subcommand %q\n\n%s", argv[0], agentUsage)
		return 2
	}
	return 0
}

// agentRoots returns the canonical search roots for installed
// subagent definitions. Project-local takes precedence over user
// global — same convention skill discovery uses.
func agentRoots() []string {
	roots := []string{}
	if _, err := os.Stat(agentgen.LocalAgentsRoot()); err == nil {
		roots = append(roots, agentgen.LocalAgentsRoot())
	}
	roots = append(roots, agentgen.UserAgentsRoot())
	return roots
}

// runAgentNew scaffolds a Claude Code subagent definition file.
func (a *App) runAgentNew(argv []string) int {
	fs := flag.NewFlagSet("agent new", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	desc := fs.String("description", "", "One-paragraph description (required)")
	tools := fs.String("tools", "", "Comma-separated tool whitelist")
	instance := fs.String("instance", "", "Default clawtool instance this agent dispatches to")
	model := fs.String("model", "", "Frontmatter model field (sonnet|haiku|opus)")
	useUser := fs.Bool("user", false, "Install under ~/.claude/agents/ (default)")
	useLocal := fs.Bool("local", false, "Install under ./.claude/agents/ instead")
	force := fs.Bool("force", false, "Overwrite an existing agent file")
	dryRun := fs.Bool("dry-run", false, "Print what would be created without writing.")
	// stdlib flag stops at the first non-flag; reorder so flags can come
	// after the positional <name> (the form users actually type).
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{
		"description": true,
		"tools":       true,
		"instance":    true,
		"model":       true,
	})); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool agent new <name> --description \"...\" [options] [--dry-run]\n")
		return 2
	}
	name := fs.Arg(0)
	if !agentgen.IsValidName(name) {
		fmt.Fprintf(a.Stderr, "agent new: invalid name %q (kebab-case [a-z0-9-]+, no leading/trailing dash)\n", name)
		return 1
	}
	if strings.TrimSpace(*desc) == "" {
		fmt.Fprintln(a.Stderr, "agent new: --description is required")
		return 2
	}
	if *useUser && *useLocal {
		fmt.Fprintln(a.Stderr, "agent new: pass --user OR --local, not both")
		return 2
	}

	root := agentgen.UserAgentsRoot()
	if *useLocal {
		root = agentgen.LocalAgentsRoot()
	}
	path := filepath.Join(root, name+".md")
	exists := false
	if _, err := os.Stat(path); err == nil {
		exists = true
	}
	if exists && !*force {
		fmt.Fprintf(a.Stderr, "agent new: %s already exists (use --force to overwrite)\n", path)
		return 1
	}

	if *dryRun {
		// Symmetric with `skill new --dry-run` (44a9819) and
		// `rules new --dry-run` (5824012): all pre-flight
		// validation has already passed (name format,
		// description present, --user/--local conflict,
		// already-exists check). Print the preview without
		// touching disk.
		verb := "would create"
		if exists && *force {
			verb = "would overwrite"
		}
		fmt.Fprintf(a.Stdout, "(dry-run) %s agent %q at %s\n", verb, name, path)
		fmt.Fprintf(a.Stdout, "  description: %s\n", strings.TrimSpace(*desc))
		if t := agentgen.ParseTools(*tools); len(t) > 0 {
			fmt.Fprintf(a.Stdout, "  tools:       %s\n", strings.Join(t, ", "))
		}
		if inst := strings.TrimSpace(*instance); inst != "" {
			fmt.Fprintf(a.Stdout, "  instance:    %s\n", inst)
		}
		if m := strings.TrimSpace(*model); m != "" {
			fmt.Fprintf(a.Stdout, "  model:       %s\n", m)
		}
		return 0
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "agent new: mkdir: %v\n", err)
		return 1
	}

	body := agentgen.Render(agentgen.RenderArgs{
		Name:        name,
		Description: *desc,
		Tools:       agentgen.ParseTools(*tools),
		Instance:    strings.TrimSpace(*instance),
		Model:       strings.TrimSpace(*model),
	})
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		fmt.Fprintf(a.Stderr, "agent new: write: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ agent → %s\n", path)
	return 0
}

// runAgentList enumerates every Claude Code subagent definition
// found under the search roots. Output: one line per agent —
// `<name>  <root>/<file>`.
func (a *App) runAgentList(_ []string) int {
	type entry struct{ name, path string }
	seen := map[string]string{}
	var list []entry
	for _, root := range agentRoots() {
		matches, _ := filepath.Glob(filepath.Join(root, "*.md"))
		for _, m := range matches {
			name := strings.TrimSuffix(filepath.Base(m), ".md")
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = m
			list = append(list, entry{name: name, path: m})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	if len(list) == 0 {
		fmt.Fprintln(a.Stdout, "(no agents — `clawtool agent new <name>` to scaffold one)")
		return 0
	}
	for _, e := range list {
		fmt.Fprintf(a.Stdout, "%s\t%s\n", e.name, e.path)
	}
	return 0
}

// runAgentPath prints the on-disk path of an agent. Without a name,
// emits the active root (the directory `agent new` would write to).
func (a *App) runAgentPath(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(a.Stdout, agentgen.UserAgentsRoot())
		return 0
	}
	for _, root := range agentRoots() {
		candidate := filepath.Join(root, argv[0]+".md")
		if _, err := os.Stat(candidate); err == nil {
			fmt.Fprintln(a.Stdout, candidate)
			return 0
		}
	}
	fmt.Fprintf(a.Stderr, "agent path: %q not found in %v\n", argv[0], agentRoots())
	return 1
}

// AgentUse persists the sticky default. We validate the instance
// exists in the supervisor's registry up front so the user gets a
// clean error here rather than at the next `clawtool send`.
func (a *App) AgentUse(instance string) error {
	instance = strings.TrimSpace(instance)
	sup := agents.NewSupervisor()
	all, err := sup.Agents(context.Background())
	if err != nil {
		return err
	}
	found := false
	for _, ag := range all {
		if ag.Instance == instance {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("instance %q not in registry — run `clawtool send --list`", instance)
	}
	if err := agents.WriteSticky(instance); err != nil {
		return fmt.Errorf("write sticky: %w", err)
	}
	fmt.Fprintf(a.Stdout, "✓ active agent → %s\n", instance)
	return nil
}

// AgentWhich resolves the empty selector and prints the result. Same
// precedence chain Send uses, exposed read-only for the user to
// inspect what would happen.
func (a *App) AgentWhich() error {
	sup := agents.NewSupervisor()
	ag, err := sup.Resolve(context.Background(), "")
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s (family=%s, status=%s)\n", ag.Instance, ag.Family, ag.Status)
	return nil
}

// AgentUnset clears the sticky default file. Idempotent.
func (a *App) AgentUnset() error {
	if err := agents.ClearSticky(); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "✓ sticky default cleared")
	return nil
}
