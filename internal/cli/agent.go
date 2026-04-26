package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/cogitave/clawtool/internal/agents"
)

const agentUsage = `Usage:
  clawtool agent use <instance>      Set the sticky default agent for this user.
                                       Subsequent 'clawtool send' calls without
                                       --agent / CLAWTOOL_AGENT resolve here.
  clawtool agent which               Show the currently-resolved default agent.
  clawtool agent unset               Clear the sticky default.
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
