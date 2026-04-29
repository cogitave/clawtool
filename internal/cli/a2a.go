// Package cli — `clawtool a2a` subcommand. Phase 1 surface for
// ADR-024 (A2A networking): emits the agent's A2A Agent Card to
// stdout, lists registered peers from the daemon's local
// registry. mDNS announce, cross-host transport, and capability
// tier enforcement land in Phase 2+.
package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/cli/listfmt"
)

const a2aUsage = `Usage:
  clawtool a2a card [--name <override>]
                                          Emit this instance's A2A Agent Card
                                          (Schema v0.2.x — github.com/a2aproject/A2A)
                                          as indented JSON.
  clawtool a2a peers [--status <s>] [--backend <b>] [--circle <c>] [--format <f>]
                                          List every running clawtool /
                                          claude-code / codex / gemini /
                                          opencode session this host's daemon
                                          knows about. Filters: status =
                                          online|busy|offline; backend = the
                                          runtime family; circle = group name.
                                          --format = table|tsv|json (default
                                          table).

A2A is the Agent2Agent protocol (Linux Foundation / Google). The card
describes what this agent does (capabilities + skills + auth) — NOT
every internal tool. Per A2A's opacity model, peers see the agent's
contract, not its private surface.

Peer discovery: when claude-code / codex / gemini / opencode run hooks
that POST to the daemon's /v1/peers/register endpoint, those sessions
show up here. Same-host first; cross-host (mDNS + Tailscale) is
Phase 2.
`

func (a *App) runA2A(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, a2aUsage)
		return 2
	}
	switch argv[0] {
	case "card":
		return a.runA2ACard(argv[1:])
	case "peers":
		return a.runA2APeers(argv[1:])
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

// runA2APeers lists peers registered on the local daemon. We dial
// the daemon's /v1/peers HTTP endpoint instead of reading
// a2a.GetGlobal() because this CLI invocation is a separate
// process from the daemon — the in-memory registry lives in the
// daemon, not in this CLI binary.
func (a *App) runA2APeers(argv []string) int {
	format, rest, err := listfmt.ExtractFlag(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool a2a peers: %v\n", err)
		return 2
	}
	q := url.Values{}
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--status", "--backend", "--circle", "--path":
			if i+1 >= len(rest) {
				fmt.Fprintf(a.Stderr, "clawtool a2a peers: %s requires a value\n", rest[i])
				return 2
			}
			q.Set(strings.TrimPrefix(rest[i], "--"), rest[i+1])
			i++
		default:
			fmt.Fprintf(a.Stderr, "clawtool a2a peers: unknown flag %q\n\n%s", rest[i], a2aUsage)
			return 2
		}
	}

	path := "/v1/peers"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var body struct {
		Peers []a2a.Peer `json:"peers"`
		Count int        `json:"count"`
	}
	if err := daemonRequest(http.MethodGet, path, nil, &body); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool a2a peers: %v\n", err)
		return 1
	}
	if body.Count == 0 {
		fmt.Fprintln(a.Stdout, "(no peers registered — runtimes need their hook installed via `clawtool hooks install <runtime>`)")
		return 0
	}

	cols := listfmt.Cols{
		Header: []string{"PEER_ID", "NAME", "BACKEND", "STATUS", "CIRCLE", "PATH", "AGE"},
	}
	now := time.Now().UTC()
	for _, p := range body.Peers {
		short := p.PeerID
		if len(short) > 8 {
			short = short[:8]
		}
		age := now.Sub(p.LastSeen).Round(time.Second)
		cols.Rows = append(cols.Rows, []string{
			short,
			p.DisplayName,
			p.Backend,
			string(p.Status),
			p.Circle,
			shortenPath(p.Path, 40),
			age.String(),
		})
	}
	if err := listfmt.Render(a.Stdout, format, cols); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool a2a peers: render: %v\n", err)
		return 1
	}
	return 0
}

// shortenPath compresses long paths so the table renderer doesn't
// blow the terminal width. Keeps head + tail (operator typically
// cares about both the /home/<user> prefix and the repo name).
// Distinct from task_watch.go's truncate, which only keeps the head.
func shortenPath(s string, maxLen int) string {
	if maxLen <= 3 || len(s) <= maxLen {
		return s
	}
	keepHead := maxLen / 2
	keepTail := maxLen - keepHead - 1
	return s[:keepHead] + "…" + s[len(s)-keepTail:]
}
