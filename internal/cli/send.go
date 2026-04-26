package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/cogitave/clawtool/internal/agents"
)

const sendUsage = `Usage:
  clawtool send [--agent <instance>] [--session <sid>] [--model <m>] [--format <f>] "<prompt>"
                                Stream a prompt to the resolved agent's
                                upstream CLI. Output streams to stdout
                                verbatim — wire format depends on the
                                upstream (stream-json, ACP frames, etc.).
  clawtool send --list          Print the supervisor's agent registry.

Resolution precedence: --agent flag > CLAWTOOL_AGENT env > sticky default
(set via 'clawtool agent use <i>') > single-instance fallback. Bare
'--agent claude' resolves if exactly one instance of that family exists.
`

// runSend is the dispatcher hooked into Run().
func (a *App) runSend(argv []string) int {
	args, err := parseSendArgs(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool send: %v\n\n%s", err, sendUsage)
		return 2
	}
	if args.list {
		if err := a.SendList(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool send --list: %v\n", err)
			return 1
		}
		return 0
	}
	if args.prompt == "" {
		fmt.Fprint(a.Stderr, "clawtool send: missing prompt\n\n"+sendUsage)
		return 2
	}
	if err := a.Send(args); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool send: %v\n", err)
		return 1
	}
	return 0
}

type sendArgs struct {
	agent   string
	session string
	model   string
	format  string
	prompt  string
	list    bool
}

func parseSendArgs(argv []string) (sendArgs, error) {
	out := sendArgs{}
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--list":
			out.list = true
		case "--agent":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--agent requires a value")
			}
			out.agent = argv[i+1]
			i++
		case "--session":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--session requires a value")
			}
			out.session = argv[i+1]
			i++
		case "--model":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--model requires a value")
			}
			out.model = argv[i+1]
			i++
		case "--format":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--format requires a value")
			}
			out.format = argv[i+1]
			i++
		case "--help", "-h":
			out.list = false
			out.prompt = ""
			return out, fmt.Errorf("help requested")
		default:
			// First positional is the prompt; trailing positionals are
			// joined with a space (so `clawtool send "fix" "this"`
			// reads as `fix this`).
			if out.prompt == "" {
				out.prompt = v
			} else {
				out.prompt += " " + v
			}
		}
	}
	return out, nil
}

// Send routes through Supervisor.Send and streams stdout.
func (a *App) Send(args sendArgs) error {
	sup := agents.NewSupervisor()
	opts := map[string]any{}
	if args.session != "" {
		opts["session_id"] = args.session
	}
	if args.model != "" {
		opts["model"] = args.model
	}
	if args.format != "" {
		opts["format"] = args.format
	}
	rc, err := sup.Send(context.Background(), args.agent, args.prompt, opts)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(a.Stdout, rc)
	return err
}

// SendList prints the supervisor's agent registry — same shape as the
// MCP `AgentList` response and the HTTP `GET /v1/agents` body.
func (a *App) SendList() error {
	sup := agents.NewSupervisor()
	all, err := sup.Agents(context.Background())
	if err != nil {
		return err
	}
	w := a.Stdout
	if len(all) == 0 {
		fmt.Fprintln(w, "(no agents registered — run `clawtool bridge add <family>` to install one)")
		return nil
	}
	fmt.Fprintf(w, "%-22s %-10s %-10s %-14s %s\n", "INSTANCE", "FAMILY", "CALLABLE", "STATUS", "AUTH SCOPE")
	for _, ag := range all {
		callable := "no"
		if ag.Callable {
			callable = "yes"
		}
		fmt.Fprintf(w, "%-22s %-10s %-10s %-14s %s\n", ag.Instance, ag.Family, callable, ag.Status, ag.AuthScope)
	}
	return nil
}
