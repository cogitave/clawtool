package agents

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// fakeTransport returns a known io.ReadCloser so tests can assert routing.
type fakeTransport struct {
	family string
	body   string
}

func (f fakeTransport) Family() string { return f.family }
func (f fakeTransport) Send(_ context.Context, prompt string, _ map[string]any) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.body + "|" + prompt)), nil
}

// newTestSupervisor wires a supervisor with controllable config + every
// transport synthesized as a fake. binaryOnPath is overridden inline.
func newTestSupervisor(t *testing.T, cfg config.Config, binaries map[string]bool) *supervisor {
	t.Helper()
	tmp := t.TempDir()
	binaryOnPath = func(name string) bool { return binaries[name] }
	t.Cleanup(func() {
		binaryOnPath = func(name string) bool {
			_, err := lookPath(name)
			return err == nil
		}
	})
	return &supervisor{
		loadConfig: func() (config.Config, error) { return cfg, nil },
		transports: map[string]Transport{
			"claude":   fakeTransport{family: "claude", body: "claude-out"},
			"codex":    fakeTransport{family: "codex", body: "codex-out"},
			"opencode": fakeTransport{family: "opencode", body: "opencode-out"},
			"gemini":   fakeTransport{family: "gemini", body: "gemini-out"},
		},
		stickyPath: filepath.Join(tmp, "active_agent"),
	}
}

func TestAgents_SynthesizesDefaultPerInstalledFamily(t *testing.T) {
	s := newTestSupervisor(t, config.Config{}, map[string]bool{
		"claude": true,
		"codex":  true,
	})
	all, err := s.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gotFamilies := map[string]bool{}
	for _, a := range all {
		gotFamilies[a.Instance] = a.Callable
	}
	if !gotFamilies["claude"] || !gotFamilies["codex"] {
		t.Fatalf("expected synthesized claude+codex defaults; got %+v", gotFamilies)
	}
	// opencode/gemini binaries absent → status bridge-missing, not callable.
	for _, a := range all {
		if (a.Instance == "opencode" || a.Instance == "gemini") && a.Callable {
			t.Errorf("instance %q should not be callable when binary absent", a.Instance)
		}
	}
}

func TestAgents_ConfiguredInstancesOverrideSynthesis(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude", SecretsScope: "personal"},
			"claude-work":     {Family: "claude"},
		},
	}
	s := newTestSupervisor(t, cfg, map[string]bool{"claude": true})
	all, err := s.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, a := range all {
		names[a.Instance] = true
	}
	if names["claude"] {
		t.Error("synthesized 'claude' instance should not appear when explicit instances exist")
	}
	if !names["claude-personal"] || !names["claude-work"] {
		t.Errorf("expected both configured instances; got %v", names)
	}
}

func TestResolve_PerCallFlagWins(t *testing.T) {
	s := newTestSupervisor(t, config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude"},
			"claude-work":     {Family: "claude"},
		},
	}, map[string]bool{"claude": true})
	t.Setenv("CLAWTOOL_AGENT", "claude-work")
	a, err := s.Resolve(context.Background(), "claude-personal")
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "claude-personal" {
		t.Errorf("--agent flag should win over env; got %q", a.Instance)
	}
}

func TestResolve_EnvOverridesSticky(t *testing.T) {
	s := newTestSupervisor(t, config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude"},
			"claude-work":     {Family: "claude"},
		},
	}, map[string]bool{"claude": true})
	// Sticky says personal; env should win.
	if err := os.WriteFile(s.stickyPath, []byte("claude-personal"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAWTOOL_AGENT", "claude-work")
	a, err := s.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "claude-work" {
		t.Errorf("env should win over sticky; got %q", a.Instance)
	}
}

func TestResolve_StickyWhenNoFlagOrEnv(t *testing.T) {
	s := newTestSupervisor(t, config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude"},
			"claude-work":     {Family: "claude"},
		},
	}, map[string]bool{"claude": true})
	if err := os.WriteFile(s.stickyPath, []byte("claude-work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAWTOOL_AGENT", "")
	a, err := s.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "claude-work" {
		t.Errorf("sticky should win when no flag/env; got %q", a.Instance)
	}
}

func TestResolve_SingleInstanceFallback(t *testing.T) {
	s := newTestSupervisor(t, config.Config{}, map[string]bool{"claude": true})
	t.Setenv("CLAWTOOL_AGENT", "")
	a, err := s.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "claude" {
		t.Errorf("single registered instance should be implicit default; got %q", a.Instance)
	}
}

func TestResolve_AmbiguousMultiInstanceErrors(t *testing.T) {
	s := newTestSupervisor(t, config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude"},
			"claude-work":     {Family: "claude"},
		},
	}, map[string]bool{"claude": true})
	t.Setenv("CLAWTOOL_AGENT", "")
	_, err := s.Resolve(context.Background(), "")
	if err == nil {
		t.Fatal("expected ambiguity error with multiple instances and no resolution")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguity: %v", err)
	}
}

func TestResolve_UnknownInstanceErrors(t *testing.T) {
	s := newTestSupervisor(t, config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude"},
		},
	}, map[string]bool{"claude": true})
	_, err := s.Resolve(context.Background(), "claude-ghost")
	if err == nil {
		t.Fatal("expected error for non-registered instance")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say not found: %v", err)
	}
}

func TestResolve_BareFamilyResolvesWhenSole(t *testing.T) {
	s := newTestSupervisor(t, config.Config{
		Agents: map[string]config.AgentConfig{
			"my-claude": {Family: "claude"},
		},
	}, map[string]bool{"claude": true})
	a, err := s.Resolve(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "my-claude" {
		t.Errorf("bare family should resolve to sole matching instance; got %q", a.Instance)
	}
}

func TestSend_RoutesToTransport(t *testing.T) {
	s := newTestSupervisor(t, config.Config{}, map[string]bool{"codex": true})
	rc, err := s.Send(context.Background(), "codex", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("expected codex transport output, got %q", body)
	}
}

func TestSend_RefusesNonCallable(t *testing.T) {
	// codex transport exists but binary missing → not callable.
	s := newTestSupervisor(t, config.Config{}, map[string]bool{"claude": true})
	_, err := s.Send(context.Background(), "codex", "hi", nil)
	if err == nil {
		t.Fatal("expected error for non-callable instance")
	}
	if !strings.Contains(err.Error(), "bridge add") {
		t.Errorf("error should suggest `clawtool bridge add`; got %v", err)
	}
}

func TestParseOptions(t *testing.T) {
	o := ParseOptions(map[string]any{
		"session_id": "abc",
		"model":      "gpt-5.1",
		"format":     "stream-json",
		"cwd":        "/tmp",
		"extra_args": []string{"--verbose"},
	})
	if o.SessionID != "abc" || o.Model != "gpt-5.1" || o.Format != "stream-json" || o.Cwd != "/tmp" {
		t.Errorf("unexpected options: %+v", o)
	}
	if len(o.ExtraArgs) != 1 || o.ExtraArgs[0] != "--verbose" {
		t.Errorf("ExtraArgs not parsed; got %+v", o.ExtraArgs)
	}
	// any-slice form (JSON-decoded) also supported
	o2 := ParseOptions(map[string]any{"extra_args": []any{"--x", "--y"}})
	if len(o2.ExtraArgs) != 2 {
		t.Errorf("[]any extra_args should parse; got %v", o2.ExtraArgs)
	}
}

func TestErrBinaryMissingMessage(t *testing.T) {
	e := ErrBinaryMissing{Family: "codex", Binary: "codex"}
	if !strings.Contains(e.Error(), "bridge add codex") {
		t.Errorf("error should suggest bridge install: %v", e)
	}
}

func TestWriteSticky_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := WriteSticky("claude-personal"); err != nil {
		t.Fatal(err)
	}
	s := &supervisor{}
	got := s.readSticky()
	if got != "claude-personal" {
		t.Errorf("sticky round-trip: got %q", got)
	}
	if err := ClearSticky(); err != nil {
		t.Fatal(err)
	}
	if got := s.readSticky(); got != "" {
		t.Errorf("sticky should be empty after clear; got %q", got)
	}
	// Idempotent
	if err := ClearSticky(); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ClearSticky should be idempotent; got %v", err)
	}
}
