package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/agents/worktree"
)

const sendUsage = `Usage:
  clawtool send [--agent <instance>] [--tag <label>] [--session <sid>] [--model <m>] [--format <f>] [--isolated [--keep-on-error]] "<prompt>"
                                Stream a prompt to the resolved agent's
                                upstream CLI. Output streams to stdout
                                verbatim — wire format depends on the
                                upstream (stream-json, ACP frames, etc.).
  clawtool send --list          Print the supervisor's agent registry.

Resolution precedence: --agent flag > CLAWTOOL_AGENT env > sticky default
(set via 'clawtool agent use <i>') > single-instance fallback. Bare
'--agent claude' resolves if exactly one instance of that family exists.

Phase 4 dispatch policies (configured via [dispatch].mode in config.toml):
  explicit (default) — pin an instance via --agent.
  round-robin        — '--agent <family>' rotates across same-family
                       callable instances.
  failover           — primary errors cascade through AgentConfig.failover_to.
  tag-routed         — '--tag <label>' picks any callable instance whose
                       tags include the label (per-call --tag overrides
                       the configured mode).

Isolation:
  --isolated         — create an ephemeral git worktree under
                       ~/.cache/clawtool/worktrees/, dispatch the
                       upstream CLI with that as cwd, and clean up
                       on completion. Safe parallel multi-agent
                       fan-out without stepping on the operator's
                       working tree.
  --keep-on-error    — only meaningful with --isolated. Preserves
                       the worktree when the dispatch fails so the
                       operator can inspect it via 'clawtool
                       worktree show <taskID>'.
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
	agent       string
	session     string
	model       string
	format      string
	tag         string
	prompt      string
	list        bool
	isolated    bool
	keepOnError bool
	async       bool
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
		case "--tag":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--tag requires a value")
			}
			out.tag = argv[i+1]
			i++
		case "--isolated":
			out.isolated = true
		case "--keep-on-error":
			out.keepOnError = true
		case "--async":
			out.async = true
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
	if args.tag != "" {
		opts["tag"] = args.tag
	}

	// Worktree isolation per ADR-014 T5: when --isolated is set, we
	// create an ephemeral git worktree, point the upstream CLI at it
	// via opts["cwd"], dispatch, and clean up on success. With
	// --keep-on-error the worktree survives a failure for inspection.
	var cleanup func()
	if args.isolated {
		repoPath, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("--isolated: %w", err)
		}
		taskID := fmt.Sprintf("send-%d", time.Now().UnixNano())
		mgr := worktree.New()
		workdir, c, err := mgr.Create(context.Background(), repoPath, taskID, args.agent)
		if err != nil {
			return fmt.Errorf("--isolated: %w", err)
		}
		opts["cwd"] = workdir
		cleanup = c
		fmt.Fprintf(a.Stderr, "clawtool: isolated worktree at %s\n", workdir)
	}

	if args.async {
		// CLI is a separate process from `clawtool serve`; bootstrap
		// the BIAM identity + SQLite store here so SubmitAsync has a
		// runner attached. The store survives the process exit (it's
		// just a file on disk) and the goroutine inside the runner
		// drains the upstream stream before we return — but only
		// after we've persisted the prompt envelope and the result
		// envelope. We block briefly on a Goroutine wait via TaskWait
		// elsewhere; here we just want the task_id.
		if _, err := ensureBIAMRunner(); err != nil {
			if cleanup != nil && !args.keepOnError {
				cleanup()
			}
			return fmt.Errorf("--async: %w", err)
		}
		// Wire a fresh supervisor that picks up the runner we just
		// installed (NewSupervisor reads globalBiamRunner at construction).
		sup = agents.NewSupervisor()
		taskID, err := sup.SubmitAsync(context.Background(), args.agent, args.prompt, opts)
		if err != nil {
			if cleanup != nil && !args.keepOnError {
				cleanup()
			}
			return err
		}
		fmt.Fprintln(a.Stdout, taskID)

		// CLI process is about to exit; the runner's goroutine needs
		// the upstream dispatch to complete before main returns,
		// otherwise codex/etc. get SIGKILL'd before persisting their
		// result. Block until the task hits a terminal state.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		store, _ := ensureBIAMRunner()
		var task *biam.Task
		if store != nil {
			task, _ = store.WaitForTerminal(ctx, taskID, 250*time.Millisecond)
		}
		// --async + --isolated: the runner goroutine kept the
		// worktree busy until WaitForTerminal returned. Now reap
		// it (or keep on error per the flag) so we don't leak
		// ephemeral worktrees on every async dispatch.
		if cleanup != nil {
			failed := task != nil && task.Status != biam.TaskDone
			if failed && args.keepOnError {
				fmt.Fprintf(a.Stderr, "clawtool: keeping worktree at %s (use `clawtool worktree show` to inspect)\n", opts["cwd"])
			} else {
				cleanup()
			}
		}
		return nil
	}

	rc, err := sup.Send(context.Background(), args.agent, args.prompt, opts)
	if err != nil {
		if cleanup != nil && !args.keepOnError {
			cleanup()
		}
		return err
	}
	_, copyErr := io.Copy(a.Stdout, rc)
	// Capture upstream non-zero exit instead of dropping it via
	// defer. A swallowed ExitError used to make a crashed codex
	// run look like an empty success.
	closeErr := rc.Close()
	finalErr := copyErr
	if finalErr == nil {
		finalErr = closeErr
	}
	if cleanup != nil {
		if finalErr != nil && args.keepOnError {
			fmt.Fprintf(a.Stderr, "clawtool: keeping worktree at %s (use `clawtool worktree show` to inspect)\n", opts["cwd"])
		} else {
			cleanup()
		}
	}
	return finalErr
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
