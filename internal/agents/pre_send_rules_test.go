package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// stagePreSendRules drops the given TOML body at $tmp/.clawtool/rules.toml,
// chdirs into $tmp so rules.findProjectRulesPath finds it, and isolates
// XDG_CONFIG_HOME so a stray user-global rules.toml on the dev box
// can't mask the fixture.
func stagePreSendRules(t *testing.T, body string) {
	t.Helper()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".clawtool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .clawtool: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write rules.toml: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
}

// TestSend_PreSendRulesBlocksDispatch wires a rules.toml fixture
// containing a `pre_send` rule that blocks dispatches to
// "blocked-agent" and asserts Supervisor.Send surfaces the rule's
// name + hint via the dispatch error rather than reaching the
// transport. Sister sub-test "passes-through" guards against an
// "always block" miswiring that would still satisfy the first
// assertion.
//
// Mirrors how the Commit tool's pre_commit invocation surfaces
// rule violations and how the existing shell pre_send hook block
// is reported (see supervisor.go's "pre_send hook blocked
// dispatch" path — the rules engine reuses that error wrapper).
//
// Engine semantics: the condition expresses the invariant that
// MUST hold; condition=false → rule fails → severity acts. So
// "refuse dispatches to blocked-agent" is `arg("agent") !=
// "blocked-agent"` + severity=block — same shape as the canonical
// no-coauthor pre_commit rule (`not commit_message_contains(...)`).
func TestSend_PreSendRulesBlocksDispatch(t *testing.T) {
	rulesBody := `
[[rule]]
name = "no-blocked-agent"
description = "Refuse dispatches to the blocklisted agent."
when = "pre_send"
condition = 'arg("agent") != "blocked-agent"'
severity = "block"
hint = "Pick a different agent."
`
	t.Run("blocks-matching-agent", func(t *testing.T) {
		stagePreSendRules(t, rulesBody)
		cfg := config.Config{
			Agents: map[string]config.AgentConfig{
				"blocked-agent": {Family: "claude"},
			},
		}
		s := newTestSupervisor(t, cfg, map[string]bool{"claude": true})

		_, err := s.Send(context.Background(), "blocked-agent", "hi", nil)
		if err == nil {
			t.Fatal("expected rules-block error, got nil (dispatch reached transport)")
		}
		msg := err.Error()
		for _, want := range []string{
			"pre_send hook blocked dispatch", // mirrors shell-hook block wrapper
			"blocked-agent",                  // instance surfaced
			"no-blocked-agent",               // rule name surfaced
			"Pick a different agent.",        // hint surfaced
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("error missing %q; full message: %s", want, msg)
			}
		}
	})

	t.Run("passes-through-on-no-match", func(t *testing.T) {
		stagePreSendRules(t, rulesBody)
		cfg := config.Config{
			Agents: map[string]config.AgentConfig{
				"my-claude": {Family: "claude"},
			},
		}
		s := newTestSupervisor(t, cfg, map[string]bool{"claude": true})
		rc, err := s.Send(context.Background(), "my-claude", "hi", nil)
		if err != nil {
			t.Fatalf("expected pass-through dispatch, got error: %v", err)
		}
		defer rc.Close()
	})
}
