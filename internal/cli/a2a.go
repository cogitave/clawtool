// Package cli — `clawtool a2a` subcommand. Phase 1 surface for
// ADR-024 (A2A networking): emits the agent's A2A Agent Card to
// stdout. mDNS announce, HTTP server, peer discovery, and
// capability tier enforcement land in phase 2+; today this is
// just "tell me what this clawtool instance would advertise".
//
// Once phase 2 lands the HTTP server, the same card produced
// here will be served at /.well-known/agent-card.json. Keeping
// the CLI thin around the same renderer lets operators inspect
// the card BEFORE wiring up the network surface.
package cli

import (
	"fmt"

	"github.com/cogitave/clawtool/internal/a2a"
)

const a2aUsage = `Usage:
  clawtool a2a card [--name <override>]   Emit this instance's A2A Agent Card
                                            (Schema v0.2.x — github.com/a2aproject/A2A)
                                            as indented JSON. Phase 1: card-only mode,
                                            no live JSON-RPC endpoint advertised.

A2A is the Agent2Agent protocol (Linux Foundation / Google). Phase 1
ships only the Card serializer so operators can inspect what their
clawtool instance WILL advertise once phase 2 wires the HTTP
endpoint + mDNS announce.

The card describes what this agent does (capabilities + skills +
auth) — NOT every internal tool. Per A2A's opacity model, peers
see the agent's contract, not its private surface.
`

func (a *App) runA2A(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, a2aUsage)
		return 2
	}
	switch argv[0] {
	case "card":
		return a.runA2ACard(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool a2a: unknown subcommand %q\n\n%s",
			argv[0], a2aUsage)
		return 2
	}
}

func (a *App) runA2ACard(argv []string) int {
	var nameOverride string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--name":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool a2a card: --name requires a value")
				return 2
			}
			nameOverride = argv[i+1]
			i++
		default:
			fmt.Fprintf(a.Stderr, "clawtool a2a card: unknown flag %q\n\n%s",
				argv[i], a2aUsage)
			return 2
		}
	}

	card := a2a.NewCard(a2a.CardOptions{Name: nameOverride})
	body, err := card.MarshalIndented()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool a2a card: marshal: %v\n", err)
		return 1
	}
	if _, err := a.Stdout.Write(body); err != nil {
		return 1
	}
	fmt.Fprintln(a.Stdout)
	return 0
}
