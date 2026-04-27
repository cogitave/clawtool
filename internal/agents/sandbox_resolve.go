// Package agents — sandbox profile resolution at dispatch time
// (#163, ADR-020 §"Sandbox surface" wired into ADR-014).
//
// withSandboxResolved looks up the agent's configured sandbox
// profile (if any) in the live config snapshot and returns an
// opts map with opts["sandbox"] set to the typed *sandbox.Profile.
// Transports parse this via SendOptions.Sandbox in transport.go;
// startStreamingExecWith calls sandbox.SelectEngine().Wrap before
// cmd.Start.
//
// Per-call override precedence:
//
//	caller-supplied opts["sandbox"] = *sandbox.Profile  → kept verbatim
//	caller-supplied opts["sandbox"] = "<name>"          → resolved against cfg
//	agent.Sandbox config field                          → resolved against cfg
//	otherwise                                            → opts unchanged (no sandbox)
//
// Resolution semantics (Codex c1b00f10 audit fix #202):
//
//   - Per-call override (opts["sandbox"] = "<name>") — fail-CLOSED.
//     If the operator passed --sandbox <name> on send, they made an
//     explicit security choice. A missing or invalid profile MUST
//     refuse the dispatch with ErrSandboxUnresolvable — silently
//     running unsandboxed defeats the entire feature.
//   - Agent-config sandbox (cfg.Agent.Sandbox) — fail-open, log.
//     A misconfigured agent block is a config bug, not an active
//     security request. We log and drop the key so the dispatch
//     still runs; the operator sees the issue via
//     `clawtool sandbox show <name>`.
//   - No sandbox configured — pass through unchanged.
//
// Anti-pattern guard: opts is the caller's map. We MUST NOT
// mutate it — failover chain dispatches reuse the same map, and
// a primary's sandbox must not leak into a fallback's run. The
// helper always returns a shallow clone when it adds a key.

package agents

import (
	"errors"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/sandbox"
)

// ErrSandboxUnresolvable is returned by withSandboxResolved when an
// EXPLICIT per-call sandbox name fails to resolve. Per audit fix
// #202: operator's `--sandbox <name>` is a security choice — refuse
// the dispatch rather than silently fall through to unsandboxed.
var ErrSandboxUnresolvable = errors.New("sandbox profile cannot be resolved (refusing to dispatch unsandboxed)")

// withSandboxResolved returns opts (or a shallow clone) with
// opts["sandbox"] populated as a *sandbox.Profile when applicable.
// loadCfg is the supervisor's snapshot fetcher; we pull the live
// view rather than caching so a `clawtool config reload` mid-
// session picks up new sandbox blocks without restarting.
//
// Returns ErrSandboxUnresolvable when the caller explicitly
// requested a sandbox by name (opts["sandbox"] = "<name>") and
// resolution fails. Per-instance config sandbox failures are
// fail-open (logged, dropped from opts).
func withSandboxResolved(opts map[string]any, agent Agent, loadCfg func() (config.Config, error)) (map[string]any, error) {
	// 1. Per-call override already in opts as a typed Profile? Pass through.
	if _, ok := opts["sandbox"].(*sandbox.Profile); ok {
		return opts, nil
	}

	// 2. Per-call override as a string name? Resolve. Fail-CLOSED:
	//    explicit operator request must succeed or refuse.
	if name, ok := opts["sandbox"].(string); ok && name != "" {
		p := lookupSandbox(name, loadCfg)
		if p == nil {
			return nil, fmt.Errorf("%w: %q (per-call) — check `clawtool sandbox list`", ErrSandboxUnresolvable, name)
		}
		out := cloneOpts(opts)
		out["sandbox"] = p
		return out, nil
	}

	// 3. Agent-config sandbox? Resolve. Fail-open: a misconfigured
	//    agent block is a config bug, not an active security
	//    request, so drop the key + log + run unsandboxed. The
	//    operator surfaces it via `clawtool sandbox show <name>`.
	if agent.Sandbox != "" {
		if p := lookupSandbox(agent.Sandbox, loadCfg); p != nil {
			out := cloneOpts(opts)
			out["sandbox"] = p
			return out, nil
		}
		fmt.Fprintf(os.Stderr,
			"clawtool: sandbox profile %q (instance %q) not found or invalid; dispatching unsandboxed\n",
			agent.Sandbox, agent.Instance)
	}

	// 4. No sandbox configured. Pass through unchanged.
	return opts, nil
}

// lookupSandbox loads the config snapshot and parses the named
// profile. Returns nil on any failure — caller logs + falls back.
func lookupSandbox(name string, loadCfg func() (config.Config, error)) *sandbox.Profile {
	cfg, err := loadCfg()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: sandbox load config: %v\n", err)
		return nil
	}
	raw, ok := cfg.Sandboxes[name]
	if !ok {
		return nil
	}
	p, err := sandbox.ParseProfile(name, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: sandbox parse %q: %v\n", name, err)
		return nil
	}
	return p
}

// cloneOpts makes a shallow copy of an opts map. Values are NOT
// deep-cloned — opts carries pointers (e.g. *sandbox.Profile is
// itself a pointer) and we want them shared. Only the map header
// is duplicated so a write to the new map can't leak into the
// caller's view.
func cloneOpts(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
