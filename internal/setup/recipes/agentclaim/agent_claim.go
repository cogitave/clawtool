// Package agentclaim hosts the AI-agent integration recipe — the
// `agents` category in clawtool's setup taxonomy. The single recipe
// here, `agent-claim`, wraps the internal/agents adapter registry
// so init flows can claim native tools per agent without leaving
// the wizard. It's a thin shim — the heavy lifting lives in
// internal/agents/claudecode.go.
//
// Per-repo state: the recipe is global by nature (it mutates the
// agent's user-scope settings.json), but Apply still records its
// run in .clawtool.toml so `clawtool recipe status agent-claim`
// reflects what's been claimed in this project.
package agentclaim

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/setup"
)

type agentClaimRecipe struct{}

func (agentClaimRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "agent-claim",
		Category:    setup.CategoryAgents,
		Description: "Claim native Bash/Read/Edit/etc. on the named AI agents so only mcp__clawtool__* is exposed to the model.",
		Upstream:    "https://github.com/cogitave/clawtool/blob/main/internal/agents",
		Stability:   setup.StabilityStable,
		// Core: claiming agents is the headline value-prop —
		// every default install should wire it.
		Core: true,
	}
}

// Detect inspects every requested agent's claimed state. The recipe
// reports Applied iff every requested agent is currently claimed,
// Partial if any subset is claimed, Absent if none are.
//
// Without an explicit list of agents the recipe defaults to "every
// detected adapter on this host". Run-time UX (wizard / CLI) will
// surface a single line per agent state.
func (agentClaimRecipe) Detect(_ context.Context, _ string) (setup.Status, string, error) {
	statuses, err := allAgentStatuses()
	if err != nil {
		return setup.StatusError, "", err
	}
	if len(statuses) == 0 {
		return setup.StatusAbsent, "no agent adapters detected on this host", nil
	}
	claimed, total := 0, 0
	var detail strings.Builder
	for _, s := range statuses {
		if !s.Detected {
			continue
		}
		total++
		mark := "○"
		if s.Claimed {
			claimed++
			mark = "●"
		}
		fmt.Fprintf(&detail, "%s %s ", mark, s.Adapter)
	}
	d := strings.TrimSpace(detail.String())
	switch {
	case total == 0:
		return setup.StatusAbsent, "no agent adapters detected", nil
	case claimed == 0:
		return setup.StatusAbsent, d, nil
	case claimed == total:
		return setup.StatusApplied, d, nil
	default:
		return setup.StatusPartial, d, nil
	}
}

func (agentClaimRecipe) Prereqs() []setup.Prereq { return nil }

// Apply claims the agents named in opts["agents"] (a string slice).
// Empty / absent → claim every detected agent. Re-claiming an
// already-claimed agent is a no-op per the adapter contract, so
// Apply is idempotent.
func (agentClaimRecipe) Apply(_ context.Context, _ string, opts setup.Options) error {
	requested := stringSliceOption(opts, "agents")
	if len(requested) == 0 {
		// Default: claim every adapter that's detected on this host.
		statuses, err := allAgentStatuses()
		if err != nil {
			return err
		}
		for _, s := range statuses {
			if s.Detected && !s.Claimed {
				requested = append(requested, s.Adapter)
			}
		}
		if len(requested) == 0 {
			// Already claimed everything detected. Apply is a no-op
			// in this branch but we don't error: Verify will pass.
			return nil
		}
	}
	sort.Strings(requested)
	requested = dedup(requested)

	var failures []string
	for _, name := range requested {
		ad, err := agents.Find(name)
		if err != nil {
			if errors.Is(err, agents.ErrUnknownAgent) {
				failures = append(failures, fmt.Sprintf("%s: unknown agent", name))
				continue
			}
			return fmt.Errorf("lookup %q: %w", name, err)
		}
		if _, err := ad.Claim(agents.Options{}); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("agent-claim: %d failure(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func (agentClaimRecipe) Verify(_ context.Context, _ string) error {
	statuses, err := allAgentStatuses()
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		return errors.New("verify: no agent adapters detected")
	}
	claimed, detected := 0, 0
	for _, s := range statuses {
		if !s.Detected {
			continue
		}
		detected++
		if s.Claimed {
			claimed++
		}
	}
	if detected == 0 {
		return errors.New("verify: no detected agents to claim")
	}
	if claimed == 0 {
		return errors.New("verify: no agent claims persisted")
	}
	return nil
}

// allAgentStatuses snapshots every registered adapter's status by
// walking agents.Registry directly. internal/agents doesn't expose
// a higher-level helper today; iterating the slice is fine because
// adapter registration happens at init() time and is read-only after.
func allAgentStatuses() ([]agents.Status, error) {
	out := make([]agents.Status, 0, len(agents.Registry))
	for _, ad := range agents.Registry {
		s, err := ad.Status()
		if err != nil {
			return nil, fmt.Errorf("status %q: %w", ad.Name(), err)
		}
		out = append(out, s)
	}
	return out, nil
}

func stringSliceOption(opts setup.Options, key string) []string {
	if v, ok := setup.GetOption[[]string](opts, key); ok {
		return v
	}
	if v, ok := setup.GetOption[[]any](opts, key); ok {
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func dedup(xs []string) []string {
	if len(xs) <= 1 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

func init() { setup.Register(agentClaimRecipe{}) }
