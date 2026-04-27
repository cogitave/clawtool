package agents

import (
	"errors"
	"testing"

	"github.com/cogitave/clawtool/internal/secrets"
)

func TestWithSecretsResolved_NoOpForUnknownFamily(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{
		"global": {"ANTHROPIC_API_KEY": "shouldnt-leak"},
	}}
	loader := func() (*secrets.Store, error) { return store, nil }

	got := withSecretsResolved(map[string]any{"foo": "bar"}, Agent{Family: "made-up"}, loader)
	if _, present := got["env"]; present {
		t.Errorf("unknown family must not get an env key; got %v", got["env"])
	}
}

func TestWithSecretsResolved_ResolvesFamilyKeysFromAuthScope(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{
		"claude-personal": {"ANTHROPIC_API_KEY": "scoped-key"},
		"global":          {"ANTHROPIC_API_KEY": "global-fallback"},
	}}
	loader := func() (*secrets.Store, error) { return store, nil }

	a := Agent{Family: "claude", AuthScope: "claude-personal"}
	got := withSecretsResolved(map[string]any{}, a, loader)
	env, ok := got["env"].(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string env; got %T", got["env"])
	}
	if env["ANTHROPIC_API_KEY"] != "scoped-key" {
		t.Errorf("scoped key should win over global; got %q", env["ANTHROPIC_API_KEY"])
	}
}

func TestWithSecretsResolved_FallsBackToGlobalScope(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{
		"global": {"OPENAI_API_KEY": "g-key"},
	}}
	loader := func() (*secrets.Store, error) { return store, nil }

	a := Agent{Family: "codex"} // AuthScope empty → defaults to family
	got := withSecretsResolved(map[string]any{}, a, loader)
	env, _ := got["env"].(map[string]string)
	if env["OPENAI_API_KEY"] != "g-key" {
		t.Errorf("global key should fall through; got %q", env["OPENAI_API_KEY"])
	}
}

func TestWithSecretsResolved_MissingKeysAreSilent(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{
		"global": {},
	}}
	loader := func() (*secrets.Store, error) { return store, nil }

	a := Agent{Family: "claude"}
	got := withSecretsResolved(map[string]any{"foo": "bar"}, a, loader)
	if _, present := got["env"]; present {
		t.Errorf("no resolved keys → no env key; got %v", got["env"])
	}
	if got["foo"] != "bar" {
		t.Errorf("other opts should pass through")
	}
}

func TestWithSecretsResolved_PreservesCallerEnv(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{
		"claude": {"ANTHROPIC_API_KEY": "from-store"},
	}}
	loader := func() (*secrets.Store, error) { return store, nil }

	// Caller already injected an env. Resolver must not overwrite it.
	opts := map[string]any{
		"env": map[string]string{"ANTHROPIC_API_KEY": "caller-set"},
	}
	a := Agent{Family: "claude"}
	got := withSecretsResolved(opts, a, loader)
	env := got["env"].(map[string]string)
	if env["ANTHROPIC_API_KEY"] != "caller-set" {
		t.Errorf("caller's env value must win; got %q", env["ANTHROPIC_API_KEY"])
	}
}

func TestWithSecretsResolved_LoadStoreErrorIsSoftFail(t *testing.T) {
	loader := func() (*secrets.Store, error) {
		return nil, errors.New("file not found")
	}
	a := Agent{Family: "claude"}
	got := withSecretsResolved(map[string]any{"keep": "this"}, a, loader)
	if _, present := got["env"]; present {
		t.Error("store load error should leave opts unchanged")
	}
	if got["keep"] != "this" {
		t.Error("opts must pass through verbatim on store load error")
	}
}

func TestWithSecretsResolved_DoesNotMutateCallerOpts(t *testing.T) {
	store := &secrets.Store{Scopes: map[string]map[string]string{
		"claude": {"ANTHROPIC_API_KEY": "x"},
	}}
	loader := func() (*secrets.Store, error) { return store, nil }

	opts := map[string]any{"foo": "bar"}
	withSecretsResolved(opts, Agent{Family: "claude"}, loader)
	if _, present := opts["env"]; present {
		t.Error("caller's opts was mutated — must clone")
	}
}
