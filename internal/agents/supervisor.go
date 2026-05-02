// Supervisor — single dispatcher for the relay surface (ADR-014).
//
// Owns the live registry of agent instances and routes every prompt
// dispatch (CLI / MCP / HTTP). Reads from the user's config + the
// installed-bridge state; resolves multi-account selection per the
// ADR-014 precedence (--agent flag > CLAWTOOL_AGENT env > sticky
// default > single-instance fallback > ambiguity error).
//
// Phase 1 ships the trivial routing rule (explicit instance or
// single-default). Phase 4 (v0.13+) layers dispatch policies on top
// of the same `Send` call site without changing this file's surface.

package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/hooks"
	"github.com/cogitave/clawtool/internal/observability"
	"github.com/cogitave/clawtool/internal/rules"
	"github.com/cogitave/clawtool/internal/xdg"
	"go.opentelemetry.io/otel/attribute"
)

// Agent is one row in the supervisor's registry. Same shape across
// CLI `--list`, MCP `AgentList`, and HTTP `GET /v1/agents`. Tags and
// FailoverTo drive Phase 4's dispatch policies.
type Agent struct {
	Instance   string   `json:"instance"`              // user-chosen kebab-case name (claude-personal, claude-work, codex1, …)
	Family     string   `json:"family"`                // upstream CLI family (claude / codex / opencode / gemini / hermes)
	Bridge     string   `json:"bridge,omitempty"`      // installed bridge name ("codex-bridge", "opencode-bridge", "gemini-bridge", "hermes-bridge"); empty when family lacks a bridge concept (claude self)
	Status     string   `json:"status"`                // "callable", "bridge-missing", "binary-missing", "disabled"
	Callable   bool     `json:"callable"`              // derived: status == "callable"
	AuthScope  string   `json:"auth_scope,omitempty"`  // [secrets.X] section to resolve env from
	Tags       []string `json:"tags,omitempty"`        // labels for tag-routed dispatch (Phase 4)
	FailoverTo []string `json:"failover_to,omitempty"` // ordered fallback chain of instance names (Phase 4)
	Sandbox    string   `json:"sandbox,omitempty"`     // ADR-020 / #163: name of a [sandboxes.<name>] profile to wrap every dispatch in. Empty = no sandbox. Resolved per-call in dispatch().
}

// Supervisor is the single dispatch entry point for prompt routing.
// One per `clawtool` process.
type Supervisor interface {
	Agents(ctx context.Context) ([]Agent, error)
	Send(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error)
	Resolve(ctx context.Context, requested string) (Agent, error)

	// SubmitAsync persists the prompt + spawns a background dispatch,
	// returning a task_id immediately. Callers poll / wait via the
	// BIAM TaskGet / TaskWait surfaces. Errors out when the BIAM
	// runner isn't wired (e.g. a test or server boot that skipped
	// BIAM init).
	SubmitAsync(ctx context.Context, instance, prompt string, opts map[string]any) (string, error)
}

// supervisor is the default Supervisor implementation. Composed of:
//   - a Config snapshot (loaded once, refreshed per-call via the loader)
//   - a transports map keyed by family
//   - a sticky-default reader (~/.config/clawtool/active_agent)
type supervisor struct {
	loadConfig func() (config.Config, error)
	transports map[string]Transport
	stickyPath string                  // override for tests; default is computed
	rrState    *roundRobinState        // round-robin counters; one supervisor = one rotation state
	observer   *observability.Observer // optional; nil → no instrumentation
	biam       BiamRunner              // optional; nil → SubmitAsync errors out
	limiter    *dispatchLimiter        // built lazily from config.Dispatch.Limits; nil when disabled

	// peerRouter is the live BIAM peer registry adapter. nil →
	// peer-prefer falls through to spawn (legacy behavior). Daemon
	// boot wires the global router via SetGlobalPeerRouter; tests
	// inject stubs by setting this field directly.
	peerRouter PeerRouter

	// peerSpawner is the auto-spawn seam — when peer-prefer (or
	// auto-tmux) finds no online peer, the supervisor asks this
	// to bring one to life (typically by opening a new tmux pane
	// and registering the agent in the BIAM peer registry). nil →
	// auto-spawn is disabled and peer-prefer falls through to
	// spawn-fresh-subprocess. Daemon boot wires the global spawner
	// via SetGlobalPeerSpawner; tests inject stubs directly.
	peerSpawner PeerSpawner
}

// globalObserver is the process-wide OTel observer NewSupervisor
// attaches by default. Server boot calls SetGlobalObserver after
// successfully initialising the observer; everything else (CLI,
// MCP tools, HTTP gateway) calls plain NewSupervisor and gets the
// instrumentation attached automatically.
//
// Tests that need a per-call observer use NewSupervisorWithObserver.
var globalObserver *observability.Observer

// SetGlobalObserver registers the process-wide observer. Pass nil to
// disable. Idempotent.
func SetGlobalObserver(obs *observability.Observer) { globalObserver = obs }

// globalBiamRunner is the process-wide BIAM runner NewSupervisor wires
// async dispatches through. Server boot calls SetGlobalBiamRunner; the
// CLI/MCP/HTTP send paths pick it up implicitly via the supervisor.
var globalBiamRunner BiamRunner

// BiamRunner is the small subset of *biam.Runner the agents package
// needs. Defining it as an interface here lets us avoid an import
// cycle (biam imports agents indirectly through the runner glue) and
// makes the Supervisor testable without a real SQLite store.
type BiamRunner interface {
	Submit(ctx context.Context, instance, prompt string, opts map[string]any) (string, error)
}

// SetGlobalBiamRunner registers the process-wide async runner. Pass
// nil to disable async submission (callers fall back to streaming).
func SetGlobalBiamRunner(r BiamRunner) { globalBiamRunner = r }

// NewSupervisor wires the default supervisor. Tests inject custom
// loaders / transports.
//
// Round-robin counters and the rate / concurrency limiter are pulled
// from process-wide singletons (sharedDispatchState) so multiple
// callers in the same process — MCP tool handlers, the HTTP gateway,
// the BIAM runner — observe one rotation cursor and one token bucket.
// Building fresh state per call resets both, which silently disables
// rate limits and pins round-robin to the first instance.
func NewSupervisor() Supervisor {
	rr, lim := sharedDispatchState()
	return &supervisor{
		loadConfig: defaultLoadConfig,
		transports: map[string]Transport{
			"claude":   ClaudeTransport(),
			"codex":    CodexTransport(),
			"opencode": OpencodeTransport(),
			"gemini":   GeminiTransport(),
			"hermes":   HermesTransport(),
			"aider":    AiderTransport(),
		},
		rrState:     rr,
		observer:    globalObserver,
		biam:        globalBiamRunner,
		limiter:     lim,
		peerRouter:  GetGlobalPeerRouter(),
		peerSpawner: GetGlobalPeerSpawner(),
	}
}

// sharedDispatchState is a process-wide singleton for the dispatch
// rotation cursor and the token-bucket limiter. Initialised on first
// access; survive across NewSupervisor calls so the round-robin
// position and rate budget actually persist between dispatches.
var (
	sharedDispatchOnce sync.Once
	sharedRR           *roundRobinState
	sharedLimiter      *dispatchLimiter
)

func sharedDispatchState() (*roundRobinState, *dispatchLimiter) {
	sharedDispatchOnce.Do(func() {
		sharedRR = &roundRobinState{}
		sharedLimiter = buildLimiterFromConfig()
	})
	return sharedRR, sharedLimiter
}

// buildLimiterFromConfig reads config.Dispatch.Limits at supervisor
// construction. A bad rate string falls back to a disabled limiter so
// the dispatch path never panics; the parse error is logged once to
// stderr so the operator notices instead of silently losing rate
// enforcement.
func buildLimiterFromConfig() *dispatchLimiter {
	cfg, err := defaultLoadConfig()
	if err != nil {
		return nil
	}
	l, perr := newDispatchLimiter(cfg.Dispatch.Limits.Rate, cfg.Dispatch.Limits.Burst, cfg.Dispatch.Limits.MaxConcurrent)
	if perr != nil {
		fmt.Fprintf(os.Stderr,
			"clawtool: dispatch.limits parse error (%v) — rate limiting disabled until config is fixed\n",
			perr)
	}
	return l
}

// SubmitAsync routes through the global BIAM runner. The runner does
// its own dispatch (which calls back into Supervisor.Send) so the
// caller gets a task_id immediately and the upstream stream is
// persisted to SQLite.
func (s *supervisor) SubmitAsync(ctx context.Context, instance, prompt string, opts map[string]any) (string, error) {
	if s.biam == nil {
		return "", errors.New("biam: async runner not configured (server boot did not init BIAM)")
	}
	return s.biam.Submit(ctx, instance, prompt, opts)
}

func defaultLoadConfig() (config.Config, error) {
	return config.LoadOrDefault(config.DefaultPath())
}

// Agents returns the live registry. Algorithm:
//   - Start with `[agents.X]` blocks from config (explicit instances).
//   - Add a synthesized default for every installed bridge family
//     that has no explicit instance configured (so the bare
//     `clawtool bridge add codex` flow yields one usable instance
//     without further config).
func (s *supervisor) Agents(_ context.Context) ([]Agent, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		// Don't silently swallow a malformed config and pretend the
		// registry is empty — surface so the operator sees the parse
		// error and fixes ~/.config/clawtool/config.toml.
		return nil, fmt.Errorf("load config: %w", err)
	}
	out := make([]Agent, 0, len(cfg.Agents)+4)
	configuredFamilies := map[string]bool{}

	for instance, ac := range cfg.Agents {
		if !validFamily(ac.Family) {
			continue
		}
		a := s.composeAgent(instance, ac.Family, ac.SecretsScope)
		a.Tags = append([]string(nil), ac.Tags...)
		a.FailoverTo = append([]string(nil), ac.FailoverTo...)
		a.Sandbox = ac.Sandbox
		out = append(out, a)
		configuredFamilies[ac.Family] = true
	}

	// Synthesize default per family for which we have a transport
	// AND no explicit instance was configured. Instance name == family.
	for fam := range s.transports {
		if configuredFamilies[fam] {
			continue
		}
		if !s.familyHasBackend(fam) {
			continue
		}
		out = append(out, s.composeAgent(fam, fam, fam))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}

// composeAgent fills in the Agent struct, including reachability checks.
func (s *supervisor) composeAgent(instance, family, scope string) Agent {
	if scope == "" {
		scope = instance
	}
	a := Agent{
		Instance:  instance,
		Family:    family,
		Bridge:    fmt.Sprintf("%s-bridge", family),
		AuthScope: scope,
	}
	switch {
	case family == "claude":
		// Claude itself doesn't have a bridge plugin (clawtool runs
		// inside it); reachability is "claude binary on PATH".
		a.Bridge = ""
		if s.binaryOnPath("claude") {
			a.Status = "callable"
			a.Callable = true
		} else {
			a.Status = "binary-missing"
		}
	default:
		// Bridge-fronted families: callable when the upstream CLI
		// binary is on PATH (the bridge plugin's own install handles
		// itself; we don't probe Claude Code's plugin list at every
		// dispatch — that's `clawtool bridge list`'s job).
		if s.binaryOnPath(family) {
			a.Status = "callable"
			a.Callable = true
		} else {
			a.Status = "bridge-missing"
		}
	}
	return a
}

// familyHasBackend reports whether the given family has a transport
// registered AND a plausible install path. Used to decide whether to
// synthesise a default instance for a family that the user hasn't
// explicitly listed in config.
func (s *supervisor) familyHasBackend(family string) bool {
	_, ok := s.transports[family]
	return ok
}

// Send routes the prompt through the configured dispatch policy and
// returns the streamed reply. Phase 4: the policy seam picks the
// primary instance + (optional) failover chain; the cascade only
// kicks in when the primary's Transport.Send returns an error before
// any byte was streamed (we don't retry mid-stream — that'd duplicate
// partial output to the caller).
func (s *supervisor) Send(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error) {
	all, err := s.Agents(ctx)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no agents registered — run `clawtool bridge add <family>` first")
	}

	cfg, _ := s.loadConfig()
	tag, _ := opts["tag"].(string)
	tag = strings.TrimSpace(tag)

	// Tag-only dispatch: no --agent, but a tag was supplied. Goes
	// straight to tagRoutedPolicy regardless of dispatch.mode.
	if strings.TrimSpace(instance) == "" && tag != "" {
		primary, fallback, err := tagRoutedPolicy{}.Pick("", tag, all)
		if err != nil {
			return nil, err
		}
		return s.dispatch(ctx, primary, fallback, prompt, opts)
	}

	// Empty `instance` AND empty tag falls back to the Phase 1
	// precedence chain (env / sticky / single-callable). Keeps the
	// pre-Phase-4 UX unchanged for callers that don't configure a
	// dispatch mode.
	if strings.TrimSpace(instance) == "" {
		a, err := s.Resolve(ctx, "")
		if err != nil {
			return nil, err
		}
		return s.dispatch(ctx, a, nil, prompt, opts)
	}

	// Explicit instance: route through the configured policy.
	// `tag != ""` overrides the configured mode (per-call wins).
	policy := pickPolicy(cfg.Dispatch.Mode, s.rrState)
	if tag != "" {
		policy = tagRoutedPolicy{}
	}

	primary, fallback, err := policy.Pick(instance, tag, all)
	if err != nil {
		return nil, err
	}
	return s.dispatch(ctx, primary, fallback, prompt, opts)
}

// dispatch invokes Transport.Send on `primary`; if that errors, it
// walks `fallback` in order. The first successful Send "wins" and its
// io.ReadCloser is returned — failover never runs once bytes have
// started streaming.
//
// Per ADR-014 T1 (observability): every dispatch opens
// `agents.Supervisor.dispatch` span; each Transport.Send call inside
// the failover chain opens an `agents.Transport.Send` child span.
// Spans carry the resolved instance/family/bridge as attributes; on
// fall-through the parent span's status records the last error.
func (s *supervisor) dispatch(ctx context.Context, primary Agent, fallback []Agent, prompt string, opts map[string]any) (io.ReadCloser, error) {
	ctx, end := s.observer.StartSpan(ctx, "agents.Supervisor.dispatch",
		attribute.String("agent.primary", primary.Instance),
		attribute.String("agent.family", primary.Family),
		attribute.Int("agent.fallback_count", len(fallback)),
	)
	defer end()

	// Snapshot config once per dispatch so per-instance unattended
	// mode resolution (ADR-023) reads from the same view across the
	// failover chain. Errors here are non-fatal: a missing config
	// hands back zero-value, and the resolver falls through to the
	// built-in default.
	dispatchCfg, _ := s.loadConfig()

	// Peer-prefer preflight (operator directive: route to a live
	// BIAM peer over spawning a fresh subprocess). Tries the
	// primary's family first; failover chain entries fall through
	// to the spawn path because peer-prefer's natural fit is "one
	// well-known peer per family" rather than per-instance routing.
	mode := resolveSendMode(opts)
	if rc, handled, err := s.tryPeerRoute(ctx, primary, prompt, opts, mode); handled {
		if err != nil {
			s.observer.RecordError(ctx, err)
			return nil, err
		}
		return rc, nil
	}

	chain := append([]Agent{primary}, fallback...)
	var lastErr error
	for _, a := range chain {
		tr, ok := s.transports[a.Family]
		if !ok {
			lastErr = fmt.Errorf("no transport registered for family %q", a.Family)
			continue
		}
		if !a.Callable {
			lastErr = fmt.Errorf("agent %q is not callable: status=%s (run `clawtool bridge add %s`)", a.Instance, a.Status, a.Family)
			continue
		}
		// Audit fix #205: resolve [secrets.<a.AuthScope>] into a
		// typed env map and stash it on opts. Transports merge it
		// onto cmd.Env so each child CLI gets ONLY the keys it
		// needs — parent env stays sticky as the source of truth
		// (resolver never overrides existing keys).

		// Per-instance rate limit (v0.15 F1). The limiter blocks
		// until a token is available + a concurrency slot opens; the
		// release func runs when the dispatch's reader is closed so
		// long-running streams hold their slot for the duration.
		release, lerr := s.limiter.acquire(ctx, a.Instance)
		if lerr != nil {
			lastErr = fmt.Errorf("dispatch %q: %w", a.Instance, lerr)
			continue
		}

		sendCtx, sendEnd := s.observer.StartSpan(ctx, "agents.Transport.Send",
			attribute.String("agent.instance", a.Instance),
			attribute.String("agent.family", a.Family),
			attribute.String("agent.bridge", a.Bridge),
		)
		// pre_send hook (F3): block_on_error entries can veto the
		// dispatch — useful for "no Anthropic calls outside business
		// hours" type policies.
		if mgr := hooks.Get(); mgr != nil {
			if hookErr := mgr.Emit(sendCtx, hooks.EventPreSend, map[string]any{
				"instance": a.Instance,
				"family":   a.Family,
				"prompt":   prompt,
			}); hookErr != nil {
				s.observer.RecordError(sendCtx, hookErr)
				sendEnd()
				release()
				lastErr = fmt.Errorf("pre_send hook blocked dispatch to %q: %w", a.Instance, hookErr)
				continue
			}
		}

		// pre_send rules engine (.clawtool/rules.toml): in-process
		// evaluation of operator-declared invariants — e.g.
		// "code-writing prompts never go to opencode" (memory-rule
		// codified). Loading is best-effort: a missing rules.toml
		// means "no rules" not an error, mirroring the Commit
		// tool's pre_commit invocation site.
		if loaded, _, _, lerr := rules.LoadDefault(); lerr == nil && len(loaded) > 0 {
			args := map[string]string{
				"agent":  a.Instance,
				"family": a.Family,
				"prompt": truncateForRules(prompt),
			}
			if v, ok := opts["unattended"].(bool); ok && v {
				args["unattended"] = "true"
			} else {
				args["unattended"] = "false"
			}
			// ADR-023 §Resolved (2026-05-02): per-instance unattended
			// mode override resolved here so rules.toml conditions can
			// gate on `unattended_mode == "yolo"` etc. Resolution is
			// per-instance override → global → built-in default.
			args["unattended_mode"] = dispatchCfg.ResolveUnattendedMode(a.Instance)
			if sid, ok := opts["session_id"].(string); ok && sid != "" {
				args["session_id"] = sid
			}
			if tag, ok := opts["tag"].(string); ok && tag != "" {
				args["tag"] = tag
			}
			verdict := rules.Evaluate(loaded, rules.Context{
				Event: rules.EventPreSend,
				Args:  args,
				Now:   time.Now(),
			})
			if verdict.IsBlocked() {
				blockErr := fmt.Errorf("rules.toml blocked dispatch (%d rule(s) failed): %s",
					len(verdict.Blocked), formatBlockedRules(verdict.Blocked))
				s.observer.RecordError(sendCtx, blockErr)
				sendEnd()
				release()
				lastErr = fmt.Errorf("pre_send hook blocked dispatch to %q: %w", a.Instance, blockErr)
				continue
			}
		}

		// ADR-020 §Resolved (2026-05-02) — danger-full-access ×
		// --unsafe-yes confirmation gate. The reserved profile
		// name is the explicit no-isolation escape hatch; we
		// refuse to dispatch under it without a separate
		// confirmation flag so a stale `[agents.X] sandbox =
		// "danger-full-access"` config block (or a typo) can't
		// silently nuke the sandbox surface. Fires BEFORE
		// withSandboxResolved so the refusal lands without ever
		// touching the transport, limiter, or audit log.
		if dangerErr := checkDangerSandboxGate(opts, a); dangerErr != nil {
			s.observer.RecordError(sendCtx, dangerErr)
			sendEnd()
			release()
			lastErr = dangerErr
			continue
		}

		// Sandbox resolution per-iteration: when the agent has a
		// sandbox name configured (AgentConfig.Sandbox), look the
		// profile up in cfg.Sandboxes and stash it on a per-call
		// opts copy. Failover chain agents resolve their OWN
		// sandbox separately — primary's profile must NOT leak
		// into a fallback that wasn't configured for one.
		//
		// Audit fix #202: explicit per-call --sandbox names that
		// can't be resolved fail-closed here. The dispatch is
		// refused for THIS chain entry; if the operator wants a
		// fallback, they configure it explicitly.
		callOpts, sandboxErr := withSandboxResolved(opts, a, s.loadConfig)
		if sandboxErr != nil {
			s.observer.RecordError(sendCtx, sandboxErr)
			sendEnd()
			release()
			lastErr = fmt.Errorf("dispatch %q: %w", a.Instance, sandboxErr)
			continue
		}
		// Layer secrets-store env on top so children pick up
		// ANTHROPIC_API_KEY / OPENAI_API_KEY / etc from
		// [secrets.<scope>]. No-op when no matching keys exist.
		callOpts = withSecretsResolved(callOpts, a, configLoadSecrets)

		rc, err := tr.Send(sendCtx, prompt, callOpts)
		if err == nil {
			// Don't end the child span here — let the caller end it
			// when the stream closes. The release func also fires on
			// Close so the concurrency slot is held for the full
			// stream duration. post_send hook fires on Close so the
			// hook script sees the full lifetime.
			return &observedReadCloser{ReadCloser: rc, end: func() {
				sendEnd()
				release()
				if mgr := hooks.Get(); mgr != nil {
					_ = mgr.Emit(context.Background(), hooks.EventPostSend, map[string]any{
						"instance": a.Instance,
						"family":   a.Family,
					})
				}
			}}, nil
		}
		s.observer.RecordError(sendCtx, err)
		sendEnd()
		release()
		lastErr = fmt.Errorf("send to %q (%s): %w", a.Instance, a.Family, err)
	}
	if lastErr == nil {
		lastErr = errors.New("dispatch failed: no callable agent")
	}
	s.observer.RecordError(ctx, lastErr)
	return nil, lastErr
}

// tryPeerRoute is the peer-prefer preflight (operator directive: route
// to a registered live BIAM peer over spawning a fresh subprocess).
//
// Returns (rc, true, nil)  → peer route succeeded; caller returns rc.
// Returns (nil, true, err) → peer-only / auto-tmux refused; caller
//
//	returns the typed error verbatim (ErrNoLivePeer / ErrTmuxUnavailable).
//
// Returns (nil, false, nil) → fall through to the spawn path.
//
// Decision flowchart:
//
//	mode == spawn-only → fall through.
//	router == nil      → fall through (daemon never wired one).
//	FindOnlinePeer hit AND peer != caller → enqueue + ack stream.
//	FindOnlinePeer miss + peer-only       → ErrNoLivePeer.
//	FindOnlinePeer miss + auto-tmux       → spawn-tmux-or-ErrTmuxUnavailable.
//	FindOnlinePeer miss + peer-prefer     → try auto-spawn-in-tmux,
//	                                        else fall through.
//	Found-peer == caller (self-dispatch)  → fall through.
func (s *supervisor) tryPeerRoute(ctx context.Context, primary Agent, prompt string, opts map[string]any, mode SendMode) (io.ReadCloser, bool, error) {
	if mode == SendModeSpawnOnly {
		return nil, false, nil
	}
	router := s.peerRouter
	if router == nil {
		// peer-only / auto-tmux with no router is a fail-closed
		// configuration error: the operator asked for peer routing
		// but the daemon never wired one.
		if mode == SendModePeerOnly {
			return nil, true, fmt.Errorf("%w: peer registry not initialised", ErrNoLivePeer)
		}
		if mode == SendModeAutoTmux {
			return nil, true, fmt.Errorf("%w: peer registry not initialised", ErrTmuxUnavailable)
		}
		return nil, false, nil
	}
	caller := callerPeerID(opts)
	peerID, displayName, ok := router.FindOnlinePeer(primary.Family, caller)
	if !ok {
		// No live peer. peer-only refuses; everyone else gets a
		// crack at auto-spawn (peer-prefer falls through on no-tmux,
		// auto-tmux fails-closed on no-tmux).
		if mode == SendModePeerOnly {
			return nil, true, fmt.Errorf("%w: family=%s", ErrNoLivePeer, primary.Family)
		}
		spawned, sErr := s.tryAutoSpawn(primary.Family, caller, mode)
		if sErr != nil {
			return nil, true, sErr
		}
		if !spawned {
			// peer-prefer + no tmux → legacy fall-through.
			return nil, false, nil
		}
		// Auto-spawn fired; re-resolve so we route to the
		// freshly registered peer rather than the stale miss.
		peerID, displayName, ok = router.FindOnlinePeer(primary.Family, caller)
		if !ok {
			// Spawner returned success but registry didn't see
			// the peer yet — race the caller can recover from
			// by retrying. peer-prefer falls through; auto-tmux
			// surfaces a typed error so the caller knows the
			// pane is up but inbox isn't ready.
			if mode == SendModeAutoTmux {
				return nil, true, fmt.Errorf("%w: spawn fired but peer not yet visible in registry", ErrTmuxUnavailable)
			}
			return nil, false, nil
		}
	}
	msgID, err := router.EnqueueToPeer(peerID, caller, prompt)
	if err != nil {
		// Enqueue failed — peer-only / auto-tmux surface the error;
		// otherwise fall through to spawn so the operator's prompt
		// still lands somewhere.
		if mode == SendModePeerOnly {
			return nil, true, fmt.Errorf("peer-only: enqueue failed: %w", err)
		}
		if mode == SendModeAutoTmux {
			return nil, true, fmt.Errorf("auto-tmux: enqueue failed: %w", err)
		}
		return nil, false, nil
	}
	// Lifecycle link: when the resolved peer is one that SendMessage
	// auto-spawned itself (metadata flag MetaAutoSpawned=true), record
	// the taskID → peerID mapping so the BIAM terminal-status hook
	// can close the pane on completion. We never link user-attached
	// peers — that path leaves auto_spawned absent and the hook
	// short-circuits at the unlinkTask check, so an operator's
	// manually-opened pane is safe from auto-close.
	//
	// Per-task override (ADR-034 Q3): a SendMessage caller can pin
	// the pane for this specific dispatch by passing
	// opts["auto_close"]=false; we then skip LinkTaskToPeer so the
	// terminal-status hook never finds a row to close. Default
	// (unset / true) preserves the auto-close behaviour.
	if router.IsAutoSpawnedPeer(peerID) && autoCloseFromOpts(opts) {
		if taskID := taskIDFromOpts(opts); taskID != "" {
			LinkTaskToPeer(taskID, peerID)
		}
	}
	_ = ctx // span attribution piggybacks on the parent dispatch span
	return newPeerAckStream(peerID, displayName, msgID), true, nil
}

// taskIDFromOpts pulls CLAWTOOL_TASK_ID out of the BIAM-injected
// opts["env"] map so the lifecycle hook has a key to dial. The
// BIAM runner injects this via injectFanInEnv before calling
// Supervisor.Send, so any in-process async dispatch carries it.
// Returns empty string when the caller wasn't a BIAM-tracked
// dispatch (synchronous CLI peer send, etc.) — the lifecycle
// link is then skipped, which is correct: there's no terminal
// status to wait on.
func taskIDFromOpts(opts map[string]any) string {
	if opts == nil {
		return ""
	}
	env, _ := opts["env"].(map[string]string)
	if env == nil {
		return ""
	}
	return env["CLAWTOOL_TASK_ID"]
}

// autoCloseFromOpts returns the per-task auto-close preference
// (ADR-034 Q3). Default = true (current behaviour: lifecycle hook
// closes the auto-spawned pane on terminal status). Setting
// opts["auto_close"]=false at the SendMessage call site pins the
// pane for that specific task — the hook never finds the link in
// the table and skips the close. Useful for an operator who wants
// to inspect the pane after the task settles, or for a long-lived
// "scratch" pane used across multiple unrelated dispatches.
//
// Recognised opts shapes:
//   - bool: opts["auto_close"] = false
//   - string: opts["auto_close"] = "false" | "0" (CLI / MCP path
//     where everything serialises through string form). "true" /
//     "1" / "yes" / empty string all map to true (default).
//
// Anything else (missing key, wrong type) → true so the legacy
// auto-close behaviour stays the default.
func autoCloseFromOpts(opts map[string]any) bool {
	if opts == nil {
		return true
	}
	v, ok := opts["auto_close"]
	if !ok {
		return true
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "false", "0", "no", "off":
			return false
		default:
			return true
		}
	}
	return true
}

// tryAutoSpawn attempts to bring an agent of `family` to life when no
// online peer matched. Returns (spawned, err):
//
//	(true, nil)  → spawn fired (or recently fired & cooldown reused);
//	               caller re-resolves the peer and routes to it.
//	(false, nil) → no-op fall-through (peer-prefer + no tmux / no
//	               spawner): caller falls through to the legacy
//	               spawn-fresh-subprocess path.
//	(false, err) → auto-tmux refused because the host has no tmux:
//	               the caller surfaces ErrTmuxUnavailable verbatim.
//
// Idempotency: shouldAutoSpawn debounces per-family within
// autoSpawnCooldown so five SendMessage calls in a second produce one
// pane, not five.
func (s *supervisor) tryAutoSpawn(family, fromPeerID string, mode SendMode) (bool, error) {
	spawner := s.peerSpawner
	if spawner == nil {
		// No spawner wired — auto-tmux fails closed; peer-prefer
		// falls through to the legacy spawn path.
		if mode == SendModeAutoTmux {
			return false, fmt.Errorf("%w: spawner not configured", ErrTmuxUnavailable)
		}
		return false, nil
	}
	if !spawner.TmuxAvailable() {
		if mode == SendModeAutoTmux {
			return false, fmt.Errorf("%w: tmux not detected (start a tmux session or pass mode=peer-prefer)", ErrTmuxUnavailable)
		}
		return false, nil
	}
	// Cooldown: skip the spawn call if we already fired one for
	// this family recently. The caller re-resolves the registry
	// and finds the just-spawned peer.
	if !shouldAutoSpawn(family) {
		return true, nil
	}
	if _, _, _, err := spawner.EnsurePeer(family, fromPeerID); err != nil {
		// Spawner failed — auto-tmux surfaces; peer-prefer falls
		// through. We deliberately don't reset the cooldown on
		// failure: a misbehaving spawner should not be hammered.
		if mode == SendModeAutoTmux {
			return false, fmt.Errorf("auto-tmux: spawn failed: %w", err)
		}
		return false, nil
	}
	return true, nil
}

// observedReadCloser ends the per-dispatch span when the caller closes
// the stream. Without this, an attached span would be leaked because
// Transport.Send returns control before the upstream finishes
// streaming.
type observedReadCloser struct {
	io.ReadCloser
	end observability.EndFunc
}

func (o *observedReadCloser) Close() error {
	err := o.ReadCloser.Close()
	o.end()
	return err
}

// truncateForRules clamps the prompt before passing it through
// rules.Context.Args["prompt"]. The rules engine condition DSL only
// uses arg() for `==` / `!=` string compare, so a long prompt would
// just bloat the verdict logs without improving matchability.
const rulesPromptMaxLen = 512

func truncateForRules(prompt string) string {
	if len(prompt) <= rulesPromptMaxLen {
		return prompt
	}
	return prompt[:rulesPromptMaxLen] + "…[truncated]"
}

// formatBlockedRules renders a verdict's blocked Results into a
// single line. Each entry is "<rule-name>: <reason> — <hint>" so the
// operator (and the agent reading the error) sees both WHY the rule
// fired and what to do about it.
func formatBlockedRules(blocked []rules.Result) string {
	parts := make([]string, 0, len(blocked))
	for _, r := range blocked {
		entry := r.Rule
		if r.Reason != "" {
			entry += ": " + r.Reason
		}
		if r.Hint != "" {
			entry += " — " + r.Hint
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, "; ")
}

// Resolve applies the ADR-014 precedence chain to pick an Agent for
// the given requested instance string. Empty `requested` triggers the
// env / sticky / single-default cascade.
func (s *supervisor) Resolve(ctx context.Context, requested string) (Agent, error) {
	requested = strings.TrimSpace(requested)
	all, err := s.Agents(ctx)
	if err != nil {
		return Agent{}, err
	}
	if len(all) == 0 {
		return Agent{}, fmt.Errorf("no agents registered — run `clawtool bridge add <family>` first")
	}

	// 1. Per-call value wins.
	if requested != "" {
		if a, ok := findInstance(all, requested); ok {
			return a, nil
		}
		// Bare family-name shortcut: `--agent claude` resolves if
		// exactly one instance of that family exists.
		if a, ok := findSoleByFamily(all, requested); ok {
			return a, nil
		}
		return Agent{}, fmt.Errorf("agent %q not found (registered: %s)", requested, listInstanceNames(all))
	}

	// 2. Env override.
	if env := strings.TrimSpace(os.Getenv("CLAWTOOL_AGENT")); env != "" {
		if a, ok := findInstance(all, env); ok {
			return a, nil
		}
		return Agent{}, fmt.Errorf("CLAWTOOL_AGENT=%q not in registry (%s)", env, listInstanceNames(all))
	}

	// 3. Sticky default.
	if name := s.readSticky(); name != "" {
		if a, ok := findInstance(all, name); ok {
			return a, nil
		}
		// Stale sticky: error out rather than silently falling through.
		return Agent{}, fmt.Errorf("sticky default %q (%s) is not in registry; run `clawtool agent use <instance>` to refresh", name, s.stickyFile())
	}

	// 4. Single-callable-instance fallback. Non-callable entries
	// (binary missing, bridge not installed) are visible in the
	// registry but don't count toward the implicit default — the
	// user wouldn't be able to dispatch to them anyway.
	callable := make([]Agent, 0, len(all))
	for _, a := range all {
		if a.Callable {
			callable = append(callable, a)
		}
	}
	if len(callable) == 1 {
		return callable[0], nil
	}
	if len(callable) == 0 {
		return Agent{}, fmt.Errorf(
			"no callable agents (registry: %s) — install a bridge with `clawtool bridge add <family>`",
			listInstanceNames(all),
		)
	}

	// More than one callable — report the families and a guided
	// next step. The original message dumped raw instance names;
	// this version walks the operator through the three resolution
	// paths (per-call > env > sticky) so they pick the one that
	// fits their workflow.
	families := familyCounts(callable)
	first := callable[0].Instance
	return Agent{}, fmt.Errorf(
		"agent ambiguous (%d callable: %s). Pick one of:\n"+
			"  • per-call:   --agent %s\n"+
			"  • env-wide:   export CLAWTOOL_AGENT=%s\n"+
			"  • sticky:     clawtool agent use %s\n"+
			"Detected families: %s",
		len(callable), listInstanceNames(callable),
		first, first, first, families,
	)
}

// familyCounts renders "claude×1, codex×1, gemini×1" so the
// ambiguity error tells the operator at a glance which families
// are competing — not just instance names.
func familyCounts(agents []Agent) string {
	counts := map[string]int{}
	order := []string{}
	for _, a := range agents {
		if _, seen := counts[a.Family]; !seen {
			order = append(order, a.Family)
		}
		counts[a.Family]++
	}
	parts := make([]string, 0, len(order))
	for _, fam := range order {
		parts = append(parts, fmt.Sprintf("%s×%d", fam, counts[fam]))
	}
	return strings.Join(parts, ", ")
}

// readSticky reads the active-agent file. Empty string when missing /
// unreadable so the caller falls through to the next precedence layer.
func (s *supervisor) readSticky() string {
	b, err := os.ReadFile(s.stickyFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// stickyFile resolves the sticky-default path. Honors the test-only
// override; otherwise computes from XDG_CONFIG_HOME or HOME.
func (s *supervisor) stickyFile() string {
	if s.stickyPath != "" {
		return s.stickyPath
	}
	return filepath.Join(xdg.ConfigDir(), "active_agent")
}

// WriteSticky persists the active-agent name. Used by `clawtool agent use`.
// Atomic temp+rename so a crash mid-write doesn't corrupt the file.
func WriteSticky(instance string) error {
	s := &supervisor{}
	path := s.stickyFile()
	return atomicfile.WriteFileMkdir(path, []byte(strings.TrimSpace(instance)+"\n"), 0o644, 0o755)
}

// ClearSticky removes the active-agent file (no-op if absent).
func ClearSticky() error {
	s := &supervisor{}
	err := os.Remove(s.stickyFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ── helpers ────────────────────────────────────────────────────────

func findInstance(all []Agent, name string) (Agent, bool) {
	for _, a := range all {
		if a.Instance == name {
			return a, true
		}
	}
	return Agent{}, false
}

func findSoleByFamily(all []Agent, family string) (Agent, bool) {
	var found Agent
	count := 0
	for _, a := range all {
		if a.Family == family {
			found = a
			count++
		}
	}
	if count == 1 {
		return found, true
	}
	return Agent{}, false
}

func listInstanceNames(all []Agent) string {
	names := make([]string, 0, len(all))
	for _, a := range all {
		names = append(names, a.Instance)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func validFamily(f string) bool {
	switch f {
	case "claude", "codex", "opencode", "gemini":
		return true
	}
	return false
}

// binaryOnPath wraps exec.LookPath so tests can shim it.
var binaryOnPath = func(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func (s *supervisor) binaryOnPath(name string) bool { return binaryOnPath(name) }
