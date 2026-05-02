package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/agents/worktree"
	"github.com/cogitave/clawtool/internal/unattended"
)

// ExitIsolatedPortalConflict is the exit code emitted when
// `clawtool send --isolated` detects a portal call in the resolved
// tool plan. Post-ADR-018 the obscura serve pool lives on the
// daemon side of the isolation boundary, so an isolated subprocess
// cannot see portal sessions and would silently fail. Fail-closed
// here with a distinct code so CI / wrappers can pattern-match the
// gate instead of conflating it with a generic dispatch error.
const ExitIsolatedPortalConflict = 4

// portalCallSignals enumerates the literal strings we treat as a
// "portal call" in the prompt body. Conservative on purpose: we
// match exact tool aliases the agent layer would emit, not natural
// language. Adding a new signal here is a deliberate, reviewed
// change — false positives are loud (a power user can override via
// --allow-portal-in-isolated), false negatives let the dispatch
// reach the daemon-less subprocess and silently fail.
var portalCallSignals = []string{
	"mcp__clawtool__PortalAsk",
	"clawtool portal ask",
}

// promptReferencesPortalCall reports whether the rendered prompt
// (or the explicit --portal flag) names a portal-driven tool call.
// Pure string match — no parsing, no heuristics. See
// portalCallSignals for the allow-list.
func promptReferencesPortalCall(prompt, portalFlag string) bool {
	if strings.TrimSpace(portalFlag) != "" {
		return true
	}
	for _, sig := range portalCallSignals {
		if strings.Contains(prompt, sig) {
			return true
		}
	}
	return false
}

const sendUsage = `Usage:
  clawtool send [--agent <instance>] [--tag <label>] [--session <sid>] [--model <m>] [--format <f>] [--mode <m>] [--no-auto-close] [--isolated [--keep-on-error]] [--unattended | --yolo] "<prompt>"
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

Routing mode (--mode, optional):
  peer-prefer (default, empty) routes to a registered live BIAM peer
                       when one matches the resolved family; falls back
                       to spawning a fresh subprocess when no peer is
                       online.
  peer-only            fails when no peer matches — guarantees the
                       prompt lands in the operator's open pane.
  auto-tmux            requires the auto-spawn path: brings a fresh
                       tmux pane up under a known agent CLI when no
                       peer matches, so every dispatch is observable.
  spawn-only           skips the peer registry and always spawns a
                       fresh subprocess (legacy behaviour).

Pane lifecycle:
  --no-auto-close      pin the auto-spawned tmux pane open for THIS
                       dispatch. Default = pane is reaped when the
                       task hits a terminal status; pass this flag
                       to keep the pane alive for inspection or
                       follow-up dispatches. No effect on user-attached
                       panes (those are never auto-closed).

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
  --allow-portal-in-isolated
                     — opt-out for the fail-closed gate that
                       refuses --isolated dispatches whose prompt
                       references a portal call (PortalAsk /
                       'clawtool portal ask') OR pass --portal.
                       Default off: portals require the daemon-side
                       obscura pool, which the isolated subprocess
                       cannot see, so the gate prevents a silent
                       no-op. Exit code on refusal: 4.
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
	// ADR-018 fail-closed gate. The obscura serve pool that backs
	// every portal sits on the daemon side of the isolation
	// boundary; an isolated subprocess can't see those sessions
	// and would silently no-op the portal call. Refuse the
	// dispatch up front so CI / wrappers see a distinct exit code
	// (ExitIsolatedPortalConflict) and a directive error message
	// instead of an opaque empty result. --allow-portal-in-isolated
	// is the documented opt-out.
	if args.isolated && !args.allowPortalInIsolated &&
		promptReferencesPortalCall(args.prompt, args.portal) {
		fmt.Fprintln(a.Stderr,
			"error: --isolated forbids portal calls; portals require the daemon-side obscura pool. "+
				"Drop --isolated, OR remove the portal call from your prompt.")
		return ExitIsolatedPortalConflict
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
	mode        string // routing mode: peer-prefer (default) | peer-only | auto-tmux | spawn-only
	prompt      string
	list        bool
	isolated    bool
	keepOnError bool
	async       bool
	wait        bool // --async + --wait blocks until terminal (legacy 10-min behaviour); without --wait, returns task_id immediately
	unattended  bool // ADR-023: --unattended | --yolo flag
	yoloAlias   bool // true when invoked via --yolo (changes banner text)
	// noAutoClose mirrors the SendMessage MCP `auto_close=false` opt
	// (ADR-034 Q3): pin the auto-spawned tmux pane open for this
	// dispatch, even when the master gate is on. Threads through to
	// supervisor opts as `auto_close=false`; the supervisor's
	// tryPeerRoute then skips LinkTaskToPeer so the lifecycle hook
	// finds no row when the task hits terminal status. Default
	// false (current behaviour: pane is reaped).
	noAutoClose bool
	// portal is the reserved field for the future explicit
	// --portal <name> selector. Today it only feeds the
	// --isolated × portal-call gate (ADR-018): when non-empty
	// alongside --isolated, the dispatch fails with
	// ExitIsolatedPortalConflict because the obscura serve pool
	// lives daemon-side and is invisible to the isolated
	// subprocess. The flag is accepted but otherwise unwired —
	// the routing layer that consumes it lands in the portal
	// driver milestone.
	portal string
	// allowPortalInIsolated is the opt-out for the ADR-018 gate.
	// Power users who have wired their own daemon-side bridge
	// (or are deliberately exercising the silent-failure path
	// for testing) can set this to bypass the conflict check.
	// Default off; the gate is fail-closed.
	allowPortalInIsolated bool
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
		case "--mode":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--mode requires a value")
			}
			out.mode = argv[i+1]
			i++
		case "--no-auto-close":
			// ADR-034 Q3: per-task pane preservation. CLI-level
			// alias for the SendMessage MCP `auto_close=false` opt
			// — sets opts["auto_close"]=false in Send() so the
			// supervisor's tryPeerRoute skips LinkTaskToPeer for
			// this dispatch and the lifecycle hook never reaps
			// the auto-spawned pane on terminal status.
			out.noAutoClose = true
		case "--isolated":
			out.isolated = true
		case "--keep-on-error":
			out.keepOnError = true
		case "--portal":
			// Reserved for the future explicit-portal selector
			// — today the only consumer is the ADR-018
			// --isolated × portal-call gate, which treats a
			// non-empty value as a portal-call signal.
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--portal requires a value")
			}
			out.portal = argv[i+1]
			i++
		case "--allow-portal-in-isolated":
			// ADR-018 opt-out. Power users who have wired
			// their own daemon-side bridge bypass the
			// fail-closed gate with this flag. Default off.
			out.allowPortalInIsolated = true
		case "--async":
			out.async = true
		case "--wait":
			out.wait = true
		case "--unattended":
			out.unattended = true
		case "--yolo":
			out.unattended = true
			out.yoloAlias = true
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

// EnvUnattended is the canonical env var that mirrors `--unattended`.
// Set to "1" by `clawtool send` whenever the dispatch is running in
// unattended mode (flag OR env), so any nested `clawtool send` that
// the upstream peer agent runs (codex spawning gemini, claude
// orchestrating opencode, etc.) inherits the trust + audit context
// without re-acquiring per-repo consent.
//
// Compounding-trust clamp (ADR-023): the upstream's own bridge
// MUST NOT use this env to re-elevate to root or skip
// user-attached confirmations. The clamp lives at the
// cross-operator A2A boundary and is enforced by other code paths
// — this propagation is intra-operator only (same UID, same trust
// grant). See ADR-023 §"Q2 resolution" for the boundary rule.
const EnvUnattended = "CLAWTOOL_UNATTENDED"

// envUnattendedActive reports whether CLAWTOOL_UNATTENDED is set to
// a truthy value ("1" / "true") in the current process env. We
// only honour the canonical "1" form; anything else is rejected
// so a stray `CLAWTOOL_UNATTENDED=0` from a prior session can't
// silently re-arm the flag.
func envUnattendedActive() bool {
	return os.Getenv(EnvUnattended) == "1"
}

// resolveUnattendedFromEnv promotes the env-set CLAWTOOL_UNATTENDED=1
// to the parsed sendArgs.unattended flag. This is the inbound side
// of the propagation: a parent `clawtool send --unattended` set the
// env on its way out (see propagateUnattendedToChildren); a nested
// `clawtool send` invocation (without re-passing the flag) reads it
// back here and stays in unattended mode end-to-end.
//
// Mirrors the CLAWTOOL_AGENT precedence pattern in
// supervisor.resolveAgent: explicit flag wins, env is the implicit
// fallback.
func resolveUnattendedFromEnv(args sendArgs) sendArgs {
	if !args.unattended && envUnattendedActive() {
		args.unattended = true
	}
	return args
}

// propagateUnattendedToChildren is the outbound side: when this
// dispatch is unattended, set CLAWTOOL_UNATTENDED=1 on the current
// process env so every spawned upstream CLI (codex / claude /
// gemini / opencode) inherits it via os.Environ(). The transport
// layer's mergeEnv reads os.Environ() directly, so the env
// reaches the child without any per-transport wiring.
//
// Idempotent: re-setting an already-"1" value is a no-op.
func propagateUnattendedToChildren() {
	_ = os.Setenv(EnvUnattended, "1")
}

// buildSendOpts translates parsed CLI args into the supervisor opts
// map. Extracted from Send() so the unit test can assert the
// auto_close / mode / etc. wiring without booting the supervisor /
// daemon side-effects. Keep this in sync with the MCP
// runSendMessage handler — both paths feed the same supervisor.
//
// Side effect: when args.unattended is true (set by --unattended,
// --yolo, or CLAWTOOL_UNATTENDED=1 in the parent env via
// resolveUnattendedFromEnv), this also stamps CLAWTOOL_UNATTENDED=1
// on the current process env so spawned upstream CLIs inherit
// unattended mode. The Send() trust + audit gate runs separately
// and is the authoritative consent check; this hook only carries
// the marker through to nested dispatches.
func buildSendOpts(args sendArgs) map[string]any {
	args = resolveUnattendedFromEnv(args)
	if args.unattended {
		propagateUnattendedToChildren()
	}
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
	if args.mode != "" {
		opts["mode"] = args.mode
	}
	if args.noAutoClose {
		// ADR-034 Q3 per-task override. Threaded as a typed bool
		// (not the string form) so the supervisor's
		// autoCloseFromOpts switch hits the bool branch directly
		// — same shape the MCP SendMessage handler emits when the
		// caller explicitly passed auto_close=false. Missing key
		// continues to mean "default true" everywhere.
		opts["auto_close"] = false
	}
	return opts
}

// Send routes through Supervisor.Send and streams stdout.
func (a *App) Send(args sendArgs) error {
	sup := agents.NewSupervisor()
	// Promote CLAWTOOL_UNATTENDED=1 from the parent env to
	// args.unattended BEFORE the trust gate runs. A nested
	// `clawtool send` invoked from inside an upstream peer (codex
	// → clawtool → claude, etc.) inherits the parent's
	// unattended marker via this env var so the audit context
	// stays continuous; without this hop the child would re-prompt
	// for consent and the operator's --unattended grant would only
	// cover the outermost call.
	args = resolveUnattendedFromEnv(args)
	opts := buildSendOpts(args)

	// ADR-023 unattended mode: enforce trust + open audit session
	// BEFORE we touch the supervisor. Disclosure refusal is a hard
	// stop — return an error rather than silently fall through to
	// permission-prompted dispatch.
	var attendedSession *unattended.SessionState
	if args.unattended {
		repo, _ := os.Getwd()
		trusted, err := unattended.IsTrusted(repo)
		if err != nil {
			return fmt.Errorf("--unattended: %w", err)
		}
		if !trusted {
			fmt.Fprint(a.Stderr, unattended.DisclosurePanel(repo))
			return fmt.Errorf(
				"--unattended: repo %q is not trusted yet. "+
					"Run `clawtool unattended grant` to confirm and re-try.", repo)
		}
		s, err := unattended.Begin(repo, args.yoloAlias)
		if err != nil {
			return fmt.Errorf("--unattended: %w", err)
		}
		attendedSession = s
		defer attendedSession.Close()

		fmt.Fprintln(a.Stderr, attendedSession.Banner())
		// Pass the unattended marker through to the supervisor /
		// transports so they can opt into per-instance flag
		// elevation (--dangerously-skip-permissions, etc.) when
		// the rest of the wiring lands. v1 just records the
		// attempt; full per-flag plumbing is v1.1.
		opts["unattended"] = true
		opts["unattended_session"] = attendedSession.ID
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
		// Dispatch resolution order:
		//
		//  1. Daemon dispatch socket. If `clawtool serve` is up
		//     it owns a Unix socket at $XDG_STATE_HOME/clawtool/
		//     dispatch.sock. We submit through it so the runner
		//     goroutine (and therefore the WatchHub the
		//     orchestrator watches) lives in the daemon. Without
		//     this, frames the upstream agent emits would land
		//     in the CLI process's hub and the orchestrator
		//     stream pane would stay empty.
		//
		//  2. In-process fallback. No daemon → bootstrap a local
		//     runner like before. Tasks still transit SQLite, so
		//     `task list` / dashboard see them, but live frames
		//     don't reach the orchestrator (separate hub). We
		//     warn on stderr so the operator knows.
		taskID, err := dispatchAsyncViaDaemon(a, args.agent, args.prompt, opts)
		if err != nil && err != biam.ErrNoDispatchSocket {
			if cleanup != nil && !args.keepOnError {
				cleanup()
			}
			return err
		}
		if err == biam.ErrNoDispatchSocket {
			fmt.Fprintln(a.Stderr, "clawtool: no daemon dispatch socket — using in-process fallback (live frames won't reach `clawtool orchestrator`; start `clawtool serve` for full streaming)")
			if _, ierr := ensureBIAMRunner(); ierr != nil {
				if cleanup != nil && !args.keepOnError {
					cleanup()
				}
				return fmt.Errorf("--async: %w", ierr)
			}
			// Wire a fresh supervisor that picks up the runner
			// we just installed (NewSupervisor reads
			// globalBiamRunner at construction).
			sup = agents.NewSupervisor()
			taskID, err = sup.SubmitAsync(context.Background(), args.agent, args.prompt, opts)
			if err != nil {
				if cleanup != nil && !args.keepOnError {
					cleanup()
				}
				return err
			}
		}
		fmt.Fprintln(a.Stdout, taskID)

		// Audit fix #204: --async without --wait returns
		// IMMEDIATELY. The runner goroutine owns its lifecycle
		// (its own context, ref by taskID in r.inflight); the
		// CLI exit doesn't kill it because the runner uses
		// context.Background-based runCtx, not the caller's.
		// Operator polls via `clawtool task get <id>` or
		// `clawtool task watch <id>`.
		//
		// --async --wait keeps the legacy "block up to 10m"
		// behaviour for callers (CI scripts, --isolated) that
		// depend on it.
		if !args.wait {
			// --isolated worktree must NOT be reaped — the
			// runner goroutine still owns it. Operator reaps
			// via `clawtool worktree gc` after the task settles.
			if cleanup != nil && args.isolated {
				fmt.Fprintf(a.Stderr,
					"clawtool: worktree at %s is owned by the dispatched task; reap with `clawtool worktree gc` after `clawtool task get %s` reports terminal\n",
					opts["cwd"], taskID)
			}
			return nil
		}

		// CLI process is about to exit; the runner's goroutine
		// needs the upstream dispatch to complete before main
		// returns, otherwise codex/etc. get SIGKILL'd before
		// persisting their result. Block until the task hits a
		// terminal state.
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

	if attendedSession != nil {
		attendedSession.Emit(unattended.AuditEntry{
			Kind:   "dispatch",
			Agent:  args.agent,
			Prompt: truncateForAudit(args.prompt, 256),
		})
	}

	rc, err := sup.Send(context.Background(), args.agent, args.prompt, opts)
	if err != nil {
		if cleanup != nil && !args.keepOnError {
			cleanup()
		}
		if attendedSession != nil {
			attendedSession.Emit(unattended.AuditEntry{
				Kind:  "dispatch_error",
				Agent: args.agent,
				Error: err.Error(),
			})
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
	if attendedSession != nil {
		entry := unattended.AuditEntry{
			Kind:  "result",
			Agent: args.agent,
		}
		if finalErr != nil {
			entry.Error = finalErr.Error()
		}
		attendedSession.Emit(entry)
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

// dispatchAsyncViaDaemon submits an async dispatch through the
// daemon's Unix socket so the runner goroutine lives in the daemon
// process — frames it broadcasts reach every WatchHub subscriber on
// the daemon (including orchestrator socket clients).
//
// Returns biam.ErrNoDispatchSocket when the daemon socket is
// missing. Caller falls back to the in-process runner with a
// stderr warning. Any other error means the daemon was reachable
// but rejected the dispatch — surface it directly.
func dispatchAsyncViaDaemon(a *App, agent, prompt string, opts map[string]any) (string, error) {
	client, err := biam.DialDispatchSocket("")
	if err != nil {
		return "", err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	taskID, err := client.Submit(ctx, agent, prompt, opts)
	if err != nil {
		return "", fmt.Errorf("daemon dispatch: %w", err)
	}
	_ = a // signature parity for future stderr diagnostics
	return taskID, nil
}

// truncateForAudit caps prompt / result bodies stored in the audit
// log so a multi-MB prompt doesn't bloat audit.jsonl. Head bytes
// preserved — usually the diagnostic banner of interest.
func truncateForAudit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
