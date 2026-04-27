package agents

import (
	"errors"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/sandbox"
)

// fakeCfg is the test seam — a small config slice with two valid
// sandbox profiles. Tests pass it via a closure so the loader
// signature matches supervisor.loadConfig.
func fakeCfg(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Sandboxes: map[string]config.SandboxConfig{
			"strict": {
				Description: "no network, ro repo",
				Paths:       []config.SandboxPath{{Path: "/tmp", Mode: "rw"}},
				Network:     config.SandboxNetwork{Policy: "none"},
			},
			"lenient": {
				Description: "open network",
				Paths:       []config.SandboxPath{{Path: "/tmp", Mode: "rw"}},
				Network:     config.SandboxNetwork{Policy: "open"},
			},
		},
	}
}

func loaderOK(t *testing.T) func() (config.Config, error) {
	cfg := fakeCfg(t)
	return func() (config.Config, error) { return cfg, nil }
}

func TestWithSandboxResolved_TypedProfilePassthrough(t *testing.T) {
	preset := &sandbox.Profile{Name: "preset"}
	opts := map[string]any{"sandbox": preset}
	got, _ := withSandboxResolved(opts, Agent{}, loaderOK(t))
	if got["sandbox"].(*sandbox.Profile) != preset {
		t.Errorf("typed *sandbox.Profile in opts should be passed through unchanged")
	}
}

func TestWithSandboxResolved_StringNameResolves(t *testing.T) {
	opts := map[string]any{"sandbox": "strict"}
	got, _ := withSandboxResolved(opts, Agent{}, loaderOK(t))
	p, ok := got["sandbox"].(*sandbox.Profile)
	if !ok {
		t.Fatalf("string name should resolve to *sandbox.Profile, got %T", got["sandbox"])
	}
	if p.Name != "strict" {
		t.Errorf("resolved name = %q, want %q", p.Name, "strict")
	}
	// Original opts must NOT be mutated.
	if _, ok := opts["sandbox"].(*sandbox.Profile); ok {
		t.Error("caller's opts was mutated — must clone")
	}
}

// Audit fix #202 — fail-CLOSED on per-call name resolution failure.
// Operator's `--sandbox <name>` is an explicit security request; if the
// profile is missing or invalid, refuse the dispatch instead of silently
// running unsandboxed.
func TestWithSandboxResolved_StringNameUnknownIsFailClosed(t *testing.T) {
	opts := map[string]any{"sandbox": "ghost"}
	got, err := withSandboxResolved(opts, Agent{}, loaderOK(t))
	if err == nil {
		t.Fatal("explicit --sandbox <unknown> must error (fail-closed), got nil")
	}
	if !errors.Is(err, ErrSandboxUnresolvable) {
		t.Errorf("error should wrap ErrSandboxUnresolvable; got %v", err)
	}
	if got != nil {
		t.Errorf("opts should be nil on fail-closed; got %v", got)
	}
}

// Original opts must not be mutated even when fail-closed fires —
// test scope reuses the same opts across iterations.
func TestWithSandboxResolved_FailClosedDoesNotMutate(t *testing.T) {
	opts := map[string]any{"sandbox": "ghost", "model": "sonnet"}
	_, _ = withSandboxResolved(opts, Agent{}, loaderOK(t))
	if opts["sandbox"] != "ghost" || opts["model"] != "sonnet" {
		t.Errorf("opts mutated on fail-closed; got %+v", opts)
	}
}

func TestWithSandboxResolved_AgentConfigSandbox(t *testing.T) {
	a := Agent{Instance: "claude", Sandbox: "lenient"}
	got, _ := withSandboxResolved(map[string]any{}, a, loaderOK(t))
	p, ok := got["sandbox"].(*sandbox.Profile)
	if !ok {
		t.Fatalf("agent.Sandbox should resolve, got %T", got["sandbox"])
	}
	if p.Name != "lenient" {
		t.Errorf("agent.Sandbox resolved to %q, want lenient", p.Name)
	}
}

func TestWithSandboxResolved_AgentConfigSandboxUnknown(t *testing.T) {
	a := Agent{Instance: "claude", Sandbox: "ghost"}
	got, _ := withSandboxResolved(map[string]any{}, a, loaderOK(t))
	if _, present := got["sandbox"]; present {
		t.Errorf("unknown agent.Sandbox should result in no sandbox key; got %v", got["sandbox"])
	}
}

func TestWithSandboxResolved_NoSandboxAtAll(t *testing.T) {
	got, _ := withSandboxResolved(map[string]any{"foo": "bar"}, Agent{}, loaderOK(t))
	if _, present := got["sandbox"]; present {
		t.Errorf("expected no sandbox key when nothing requests one; got %v", got["sandbox"])
	}
	if got["foo"] != "bar" {
		t.Errorf("other opts should pass through")
	}
}

func TestWithSandboxResolved_LoadConfigError(t *testing.T) {
	a := Agent{Instance: "claude", Sandbox: "strict"}
	loader := func() (config.Config, error) {
		return config.Config{}, errors.New("disk on fire")
	}
	got, _ := withSandboxResolved(map[string]any{}, a, loader)
	if _, present := got["sandbox"]; present {
		t.Error("config load error should drop the sandbox key")
	}
}

func TestWithSandboxResolved_PerCallOverridesAgentConfig(t *testing.T) {
	// Agent has Sandbox="strict" but caller passed "lenient" in opts.
	a := Agent{Instance: "claude", Sandbox: "strict"}
	opts := map[string]any{"sandbox": "lenient"}
	got, _ := withSandboxResolved(opts, a, loaderOK(t))
	p, ok := got["sandbox"].(*sandbox.Profile)
	if !ok || p.Name != "lenient" {
		t.Errorf("per-call override should win over agent.Sandbox; got %+v", got["sandbox"])
	}
}

func TestCloneOpts_IsShallow(t *testing.T) {
	preset := &sandbox.Profile{Name: "preset"}
	in := map[string]any{"sandbox": preset, "model": "sonnet"}
	out := cloneOpts(in)
	if out["sandbox"].(*sandbox.Profile) != preset {
		t.Error("cloneOpts should keep pointer-typed values shared (shallow)")
	}
	out["model"] = "opus"
	if in["model"] == "opus" {
		t.Error("cloneOpts must not mutate the source map")
	}
}
