// Package agents — Policy is the seam ADR-014 Phase 4 plugs dispatch
// modes into. The supervisor runs every prompt through `Policy.Pick`
// to choose an instance + a fallback chain. The same `Send` call site
// then iterates through that chain, retrying on transient errors.
//
// Today's modes:
//
//	explicit     — single-instance routing per Phase 1 (default).
//	round-robin  — rotate across same-family callable instances when
//	               the caller asks for a bare family or no instance.
//	failover     — try primary, then cascade through AgentConfig.FailoverTo
//	               on Send error.
//	tag-routed   — pick any healthy instance whose tags include the
//	               caller-supplied label.
//
// Adding a new mode means: implement Policy, register it in
// pickPolicy, document the mode in ADR-014. The Send call site
// doesn't change.

package agents

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Policy chooses an Agent for a dispatch and (optionally) provides a
// fallback chain. The supervisor invokes Pick once per Send.
//
// `requested` is the caller's --agent flag value (empty when unset).
// `tag` is the caller's --tag value (empty when unset). `all` is the
// supervisor's full registry snapshot.
//
// Returns: the Agent to try first, plus an ordered slice of fallback
// instances (zero-length means no fallback). An empty primary +
// non-nil error stops the dispatch.
type Policy interface {
	Pick(requested, tag string, all []Agent) (Agent, []Agent, error)
}

// roundRobinState is the in-memory rotation counter. Keyed by family
// so each family rotates independently. atomic.Uint64 keeps the load
// path lock-free; the mutex only guards key creation.
type roundRobinState struct {
	mu       sync.Mutex
	counters map[string]*atomic.Uint64
}

func (r *roundRobinState) next(family string, modulus int) int {
	if modulus <= 0 {
		return 0
	}
	r.mu.Lock()
	c, ok := r.counters[family]
	if !ok {
		c = new(atomic.Uint64)
		if r.counters == nil {
			r.counters = map[string]*atomic.Uint64{}
		}
		r.counters[family] = c
	}
	r.mu.Unlock()
	return int(c.Add(1)-1) % modulus
}

// explicitPolicy is the Phase 1 default: caller pins the instance, we
// route there, no fallback. Bare family + sole-instance shortcut still
// works because Resolve picks before Pick is consulted.
type explicitPolicy struct{}

func (explicitPolicy) Pick(requested, _ string, all []Agent) (Agent, []Agent, error) {
	if requested == "" {
		return Agent{}, nil, errors.New("explicit dispatch requires --agent")
	}
	if a, ok := findInstance(all, requested); ok {
		return a, nil, nil
	}
	if a, ok := findSoleByFamily(all, requested); ok {
		return a, nil, nil
	}
	return Agent{}, nil, fmt.Errorf("agent %q not found (registered: %s)", requested, listInstanceNames(all))
}

// roundRobinPolicy rotates across same-family callable instances when
// the caller passed a bare family name. An explicit instance still
// wins (no rotation when the caller pinned a target). With a single
// callable instance the policy reduces to explicit dispatch.
type roundRobinPolicy struct {
	state *roundRobinState
}

func (p *roundRobinPolicy) Pick(requested, _ string, all []Agent) (Agent, []Agent, error) {
	if requested == "" {
		return Agent{}, nil, errors.New("round-robin dispatch requires --agent <family>")
	}
	// Pinned instance? Honour it.
	if a, ok := findInstance(all, requested); ok {
		return a, nil, nil
	}
	// Otherwise treat `requested` as a family name; collect all
	// callable instances of that family and rotate through them.
	candidates := callableByFamily(all, requested)
	if len(candidates) == 0 {
		return Agent{}, nil, fmt.Errorf("no callable instances for family %q", requested)
	}
	idx := p.state.next(requested, len(candidates))
	return candidates[idx], nil, nil
}

// failoverPolicy routes to the primary instance and exposes its
// AgentConfig.FailoverTo chain so the supervisor's Send can cascade
// on Transport error. Each fallback must itself be callable; missing
// or non-callable entries are silently skipped (logged at debug).
type failoverPolicy struct{}

func (failoverPolicy) Pick(requested, _ string, all []Agent) (Agent, []Agent, error) {
	if requested == "" {
		return Agent{}, nil, errors.New("failover dispatch requires --agent <instance>")
	}
	primary, ok := findInstance(all, requested)
	if !ok {
		// Bare-family shortcut (single instance) acceptable.
		if a, ok := findSoleByFamily(all, requested); ok {
			primary = a
		} else {
			return Agent{}, nil, fmt.Errorf("agent %q not found (registered: %s)", requested, listInstanceNames(all))
		}
	}
	chain := make([]Agent, 0, len(primary.FailoverTo))
	for _, name := range primary.FailoverTo {
		if a, ok := findInstance(all, name); ok && a.Callable {
			chain = append(chain, a)
		}
	}
	return primary, chain, nil
}

// tagRoutedPolicy ignores `requested`; it scans for any healthy
// instance whose tags include `tag`. When multiple match, picks
// deterministically (sorted by instance name) so the same tag yields
// a stable choice — round-robin across tagged instances is layered as
// a separate mode if needed.
type tagRoutedPolicy struct{}

func (tagRoutedPolicy) Pick(_, tag string, all []Agent) (Agent, []Agent, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return Agent{}, nil, errors.New("tag-routed dispatch requires --tag")
	}
	for _, a := range all {
		if !a.Callable {
			continue
		}
		for _, t := range a.Tags {
			if strings.EqualFold(t, tag) {
				return a, nil, nil
			}
		}
	}
	return Agent{}, nil, fmt.Errorf("no callable instance carries tag %q", tag)
}

// pickPolicy resolves the configured dispatch mode (or a per-call
// override) into a Policy implementation. Empty mode → explicit.
func pickPolicy(mode string, rr *roundRobinState) Policy {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "round-robin", "round_robin", "rr":
		return &roundRobinPolicy{state: rr}
	case "failover":
		return failoverPolicy{}
	case "tag-routed", "tag_routed", "tag":
		return tagRoutedPolicy{}
	default:
		return explicitPolicy{}
	}
}

// callableByFamily returns the subset of registered instances that
// belong to the given family AND are reachable. Sorted by instance
// name so round-robin order is deterministic.
func callableByFamily(all []Agent, family string) []Agent {
	out := make([]Agent, 0, len(all))
	for _, a := range all {
		if a.Family == family && a.Callable {
			out = append(out, a)
		}
	}
	return out
}
