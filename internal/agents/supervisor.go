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

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/observability"
	"go.opentelemetry.io/otel/attribute"
)

// Agent is one row in the supervisor's registry. Same shape across
// CLI `--list`, MCP `AgentList`, and HTTP `GET /v1/agents`. Tags and
// FailoverTo drive Phase 4's dispatch policies.
type Agent struct {
	Instance   string   `json:"instance"`              // user-chosen kebab-case name (claude-personal, claude-work, codex1, …)
	Family     string   `json:"family"`                // upstream CLI family (claude / codex / opencode / gemini)
	Bridge     string   `json:"bridge,omitempty"`      // installed bridge name ("codex-bridge", "opencode-bridge", "gemini-bridge"); empty when family lacks a bridge concept (claude self)
	Status     string   `json:"status"`                // "callable", "bridge-missing", "binary-missing", "disabled"
	Callable   bool     `json:"callable"`              // derived: status == "callable"
	AuthScope  string   `json:"auth_scope,omitempty"`  // [secrets.X] section to resolve env from
	Tags       []string `json:"tags,omitempty"`        // labels for tag-routed dispatch (Phase 4)
	FailoverTo []string `json:"failover_to,omitempty"` // ordered fallback chain of instance names (Phase 4)
}

// Supervisor is the single dispatch entry point for prompt routing.
// One per `clawtool` process.
type Supervisor interface {
	Agents(ctx context.Context) ([]Agent, error)
	Send(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error)
	Resolve(ctx context.Context, requested string) (Agent, error)
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

// NewSupervisor wires the default supervisor. Tests inject custom
// loaders / transports.
func NewSupervisor() Supervisor {
	return &supervisor{
		loadConfig: defaultLoadConfig,
		transports: map[string]Transport{
			"claude":   ClaudeTransport(),
			"codex":    CodexTransport(),
			"opencode": OpencodeTransport(),
			"gemini":   GeminiTransport(),
		},
		rrState:  &roundRobinState{},
		observer: globalObserver,
	}
}

// NewSupervisorWithObserver wires the default supervisor and attaches
// the given observer. Used by tests to inject in-memory exporters
// without touching the global.
func NewSupervisorWithObserver(obs *observability.Observer) Supervisor {
	s := NewSupervisor().(*supervisor)
	s.observer = obs
	return s
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
	cfg, _ := s.loadConfig()
	out := make([]Agent, 0, len(cfg.Agents)+4)
	configuredFamilies := map[string]bool{}

	for instance, ac := range cfg.Agents {
		if !validFamily(ac.Family) {
			continue
		}
		a := s.composeAgent(instance, ac.Family, ac.SecretsScope)
		a.Tags = append([]string(nil), ac.Tags...)
		a.FailoverTo = append([]string(nil), ac.FailoverTo...)
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
		// TODO(v0.10.x): apply [secrets.<a.AuthScope>] resolution to set
		// per-instance env (ANTHROPIC_API_KEY, OPENAI_API_KEY, …). For
		// Phase 1 the upstream CLI inherits the parent process env
		// unchanged.
		sendCtx, sendEnd := s.observer.StartSpan(ctx, "agents.Transport.Send",
			attribute.String("agent.instance", a.Instance),
			attribute.String("agent.family", a.Family),
			attribute.String("agent.bridge", a.Bridge),
		)
		rc, err := tr.Send(sendCtx, prompt, opts)
		if err == nil {
			// Don't end the child span here — let the caller end it
			// when the stream closes. We attach the close to the
			// returned reader by wrapping it.
			return &observedReadCloser{ReadCloser: rc, end: sendEnd}, nil
		}
		s.observer.RecordError(sendCtx, err)
		sendEnd()
		lastErr = fmt.Errorf("send to %q (%s): %w", a.Instance, a.Family, err)
	}
	if lastErr == nil {
		lastErr = errors.New("dispatch failed: no callable agent")
	}
	s.observer.RecordError(ctx, lastErr)
	return nil, lastErr
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

	return Agent{}, fmt.Errorf(
		"agent ambiguous (%s) — pass --agent, set CLAWTOOL_AGENT, or run `clawtool agent use <instance>`",
		listInstanceNames(callable),
	)
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
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "clawtool", "active_agent")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "active_agent"
	}
	return filepath.Join(home, ".config", "clawtool", "active_agent")
}

// WriteSticky persists the active-agent name. Used by `clawtool agent use`.
// Atomic temp+rename so a crash mid-write doesn't corrupt the file.
func WriteSticky(instance string) error {
	s := &supervisor{}
	path := s.stickyFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(instance)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
