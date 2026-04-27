// Package agents — per-dispatch secrets-store env resolution
// (#163, ADR-013-derived; closes audit #205). The supervisor wires
// upstream CLI children with the API keys they need from clawtool's
// secrets store rather than leaking everything in the parent's env.
//
// Resolution order per dispatch:
//
//  1. Look up family-default keys (ANTHROPIC_API_KEY for claude,
//     OPENAI_API_KEY for codex, GOOGLE_API_KEY / GEMINI_API_KEY for
//     gemini, etc.) in store[a.AuthScope] → store[global].
//  2. Each found key is added to opts["env"] as a typed
//     map[string]string. The transport's startStreamingExecWith
//     merges this onto the parent env so the child process sees
//     it as if it were inherited.
//  3. Missing keys are silently dropped — Phase 1 doesn't fail
//     dispatches when the operator hasn't run `clawtool source
//     set-secret`, since CLAUDE_CODE_OAUTH_TOKEN may already be
//     in the parent env from `claude login`.
//
// Authority scope = a.AuthScope (Agent struct), defaulting to the
// family name. So `[secrets.claude]` covers every claude-* instance
// unless an instance overrides AuthScope.

package agents

import (
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
)

// familyEnvKeys maps a CLI family to the env-var names its upstream
// binary reads to pick up API credentials. Conservative defaults —
// each family's published docs is the source of truth.
//
// Operators who need different keys (e.g. project-scoped tokens) set
// them in the secrets file under the agent's AuthScope; the resolver
// looks them up by name. Unknown families fall through to no env.
var familyEnvKeys = map[string][]string{
	"claude": {
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_OAUTH_TOKEN",
	},
	"codex": {
		"OPENAI_API_KEY",
		"CODEX_API_KEY",
	},
	"gemini": {
		"GEMINI_API_KEY",
		"GOOGLE_API_KEY",
		"GOOGLE_GENAI_API_KEY",
	},
	"opencode": {
		"OPENCODE_API_KEY",
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
	},
	"hermes": {
		"OPENROUTER_API_KEY",
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"GOOGLE_API_KEY",
	},
}

// withSecretsResolved layers a "env" map onto opts containing the
// secrets-store values for every familyEnvKeys[a.Family] that has a
// match in store[a.AuthScope] or store["global"].
//
// Returns the (possibly cloned) opts map. Never errors — missing
// keys are tolerated; the operator may have logged the upstream CLI
// in via its own auth path (e.g. `claude login`).
//
// loadStore is the caller-injected secrets fetcher; production wires
// it to secrets.LoadOrEmpty(secrets.DefaultPath()), tests fake it
// with an in-memory Store.
func withSecretsResolved(opts map[string]any, agent Agent, loadStore func() (*secrets.Store, error)) map[string]any {
	keys := familyEnvKeys[agent.Family]
	if len(keys) == 0 {
		return opts
	}
	store, err := loadStore()
	if err != nil || store == nil {
		return opts
	}
	scope := agent.AuthScope
	if scope == "" {
		scope = agent.Family
	}

	resolved := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := store.Get(scope, k); ok && v != "" {
			resolved[k] = v
		}
	}
	if len(resolved) == 0 {
		return opts
	}

	out := cloneOpts(opts)
	// Preserve any env the caller already injected (e.g. opts["env"]
	// from a higher-level wrapper) — secrets fill in missing keys
	// only.
	merged := map[string]string{}
	if existing, ok := out["env"].(map[string]string); ok {
		for k, v := range existing {
			merged[k] = v
		}
	}
	for k, v := range resolved {
		if _, present := merged[k]; !present {
			merged[k] = v
		}
	}
	out["env"] = merged
	return out
}

// defaultLoadSecrets is the production secrets-fetcher. The supervisor
// calls this lazily so a missing secrets.toml stays a soft failure.
func defaultLoadSecrets() (*secrets.Store, error) {
	return secrets.LoadOrEmpty(secrets.DefaultPath())
}

// configLoadSecrets is the callsite the supervisor uses; kept as a
// package var so tests can swap the resolver without touching globals.
var configLoadSecrets = defaultLoadSecrets

// _ silences the unused-import warning on config; the package import
// is needed for the secrets file's path resolution to land on the
// same XDG dir as the rest of clawtool's state.
var _ = config.DefaultPath
