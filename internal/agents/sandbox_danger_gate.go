// Package agents — danger-full-access × --unsafe-yes confirmation gate.
//
// ADR-020 §Resolved (2026-05-02) decided:
//
//	"YES, allow but require --unsafe-yes confirmation flag."
//
// The `danger-full-access` sandbox profile is the explicit
// no-isolation escape hatch — paths/network/limits are all wide
// open. Letting it dispatch on a bare `--sandbox danger-full-access`
// would defeat the entire sandbox surface, so we require an
// independent `--unsafe-yes` flag (or `opts["unsafe_yes"] = true`
// from the MCP / async submit paths) as a deliberate
// "I-know-what-I'm-doing" confirmation.
//
// The check fires on every dispatch — both the per-call
// `opts["sandbox"] = "danger-full-access"` form and the
// agent-config `[agents.X] sandbox = "danger-full-access"` form
// hit the same gate so an operator can't sneak past by stashing
// the profile name in config.toml.
//
// Wired into supervisor.dispatch right before withSandboxResolved
// so the refusal lands BEFORE we touch the transport, the limiter,
// the rules engine, or the audit log dispatch line.

package agents

import (
	"errors"
	"fmt"
	"strings"
)

// dangerFullAccessProfile is the reserved profile name that names
// the no-isolation escape hatch. Treated case-insensitively so
// `Danger-Full-Access` in config.toml is caught the same way as
// the canonical lowercase form.
const dangerFullAccessProfile = "danger-full-access"

// ErrDangerSandboxRequiresUnsafeYes is returned when a dispatch
// resolves to the danger-full-access profile without the operator
// having passed --unsafe-yes (CLI) / opts["unsafe_yes"]=true (MCP).
//
// The error wraps a stable sentinel so CLI / MCP callers can match
// on it for distinct exit codes if they want, while the wrapped
// message stays human-readable for stderr.
var ErrDangerSandboxRequiresUnsafeYes = errors.New(
	"--sandbox danger-full-access requires --unsafe-yes confirmation. " +
		"This profile bypasses all sandbox restrictions.")

// resolvedSandboxName returns the name of the sandbox profile this
// dispatch will run under, if any. Looks at the per-call opts
// first (string form only — typed *sandbox.Profile pre-resolved
// values bypass the gate by design: they're constructed in code,
// not by an operator typing the name) and falls back to the
// agent-config form. Empty string means no sandbox is in play.
func resolvedSandboxName(opts map[string]any, agent Agent) string {
	if opts != nil {
		if name, ok := opts["sandbox"].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return strings.TrimSpace(agent.Sandbox)
}

// unsafeYesFromOpts extracts the operator's confirmation. Mirrors
// autoCloseFromOpts' tolerant decoding: bool wins, string "true" /
// "1" / "yes" also accepted (CLI / MCP serialise through string
// form). Anything else (missing, wrong type) → false so the gate
// stays fail-closed.
func unsafeYesFromOpts(opts map[string]any) bool {
	if opts == nil {
		return false
	}
	v, ok := opts["unsafe_yes"]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

// checkDangerSandboxGate is the ADR-020 §Resolved gate. Returns
// nil when the dispatch is allowed; ErrDangerSandboxRequiresUnsafeYes
// (wrapped with the agent instance for the operator's stderr) when
// the danger profile is in play without confirmation.
//
// Pure function — no I/O, no logging, no mutation. Caller (the
// supervisor's dispatch loop) decides what to do with the error;
// today that's record-on-span + set lastErr + continue to the
// next failover entry, same shape as the other dispatch refusals.
func checkDangerSandboxGate(opts map[string]any, agent Agent) error {
	name := resolvedSandboxName(opts, agent)
	if !strings.EqualFold(name, dangerFullAccessProfile) {
		return nil
	}
	if unsafeYesFromOpts(opts) {
		return nil
	}
	return fmt.Errorf("dispatch %q: %w", agent.Instance, ErrDangerSandboxRequiresUnsafeYes)
}
