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
// Resolution failures (profile name doesn't exist in cfg, or
// ParseProfile rejects it) DROP the sandbox silently — the
// dispatch proceeds unsandboxed. We log via stderr but don't
// block the prompt; the operator sees the issue when
// `clawtool sandbox show <name>` reports the parse error.
//
// Anti-pattern guard: opts is the caller's map. We MUST NOT
// mutate it — failover chain dispatches reuse the same map, and
// a primary's sandbox must not leak into a fallback's run. The
// helper always returns a shallow clone when it adds a key.

package agents

import (
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/sandbox"
)

// withSandboxResolved returns opts (or a shallow clone) with
// opts["sandbox"] populated as a *sandbox.Profile when applicable.
// loadCfg is the supervisor's snapshot fetcher; we pull the live
// view rather than caching so a `clawtool config reload` mid-
// session picks up new sandbox blocks without restarting.
func withSandboxResolved(opts map[string]any, agent Agent, loadCfg func() (config.Config, error)) map[string]any {
	// 1. Per-call override already in opts as a typed Profile? Pass through.
	if _, ok := opts["sandbox"].(*sandbox.Profile); ok {
		return opts
	}

	// 2. Per-call override as a string name? Resolve.
	if name, ok := opts["sandbox"].(string); ok && name != "" {
		if p := lookupSandbox(name, loadCfg); p != nil {
			out := cloneOpts(opts)
			out["sandbox"] = p
			return out
		}
		// Resolution failed — log and drop the sandbox key so
		// the transport's nil-Sandbox fast path runs.
		fmt.Fprintf(os.Stderr,
			"clawtool: sandbox profile %q (per-call) not found or invalid; dispatching unsandboxed\n", name)
		out := cloneOpts(opts)
		delete(out, "sandbox")
		return out
	}

	// 3. Agent-config sandbox? Resolve.
	if agent.Sandbox != "" {
		if p := lookupSandbox(agent.Sandbox, loadCfg); p != nil {
			out := cloneOpts(opts)
			out["sandbox"] = p
			return out
		}
		fmt.Fprintf(os.Stderr,
			"clawtool: sandbox profile %q (instance %q) not found or invalid; dispatching unsandboxed\n",
			agent.Sandbox, agent.Instance)
	}

	// 4. No sandbox configured. Pass through unchanged.
	return opts
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
