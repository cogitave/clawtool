package cli

import (
	"os"
	"testing"
)

func TestParseSendArgs_PromptCollection(t *testing.T) {
	args, err := parseSendArgs([]string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if args.prompt != "hello world" {
		t.Errorf("prompt should be joined with space; got %q", args.prompt)
	}
}

func TestParseSendArgs_FlagsBeforePrompt(t *testing.T) {
	args, err := parseSendArgs([]string{"--agent", "claude-personal", "--model", "opus", "fix this"})
	if err != nil {
		t.Fatal(err)
	}
	if args.agent != "claude-personal" {
		t.Errorf("agent: got %q", args.agent)
	}
	if args.model != "opus" {
		t.Errorf("model: got %q", args.model)
	}
	if args.prompt != "fix this" {
		t.Errorf("prompt: got %q", args.prompt)
	}
}

func TestParseSendArgs_FlagsAfterPrompt(t *testing.T) {
	args, err := parseSendArgs([]string{"fix", "this", "--agent", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	// Trailing flag is interpreted; positional 'fix this' becomes prompt.
	if args.prompt != "fix this" {
		t.Errorf("prompt: got %q", args.prompt)
	}
	if args.agent != "claude" {
		t.Errorf("agent: got %q", args.agent)
	}
}

func TestParseSendArgs_ListShortcut(t *testing.T) {
	args, err := parseSendArgs([]string{"--list"})
	if err != nil {
		t.Fatal(err)
	}
	if !args.list {
		t.Error("--list should set list=true")
	}
	if args.prompt != "" {
		t.Errorf("--list should not collect a prompt; got %q", args.prompt)
	}
}

func TestParseSendArgs_FlagWithoutValueErrors(t *testing.T) {
	for _, flag := range []string{"--agent", "--model", "--session", "--format"} {
		_, err := parseSendArgs([]string{flag})
		if err == nil {
			t.Errorf("%s without value should error", flag)
		}
	}
}

func TestParseSendArgs_AllFlags(t *testing.T) {
	args, err := parseSendArgs([]string{
		"--agent", "codex1",
		"--session", "abc-123",
		"--model", "gpt-5.2",
		"--format", "stream-json",
		"--tag", "long-context",
		"investigate the regression",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args.agent != "codex1" || args.session != "abc-123" || args.model != "gpt-5.2" || args.format != "stream-json" || args.tag != "long-context" {
		t.Errorf("flags not parsed: %+v", args)
	}
	if args.prompt != "investigate the regression" {
		t.Errorf("prompt: got %q", args.prompt)
	}
}

func TestParseSendArgs_TagAlone(t *testing.T) {
	args, err := parseSendArgs([]string{"--tag", "fast", "summarise"})
	if err != nil {
		t.Fatal(err)
	}
	if args.tag != "fast" {
		t.Errorf("tag: got %q", args.tag)
	}
	if args.prompt != "summarise" {
		t.Errorf("prompt: got %q", args.prompt)
	}
}

func TestParseSendArgs_TagWithoutValueErrors(t *testing.T) {
	_, err := parseSendArgs([]string{"--tag"})
	if err == nil {
		t.Error("--tag without value should error")
	}
}

// TestParseSendArgs_NoAutoCloseFlag asserts the v0.22.x ADR-034 Q3
// CLI surface: `--no-auto-close` lifts the flag through to
// sendArgs.noAutoClose so Send() can thread `auto_close=false`
// into the supervisor opts.
func TestParseSendArgs_NoAutoCloseFlag(t *testing.T) {
	args, err := parseSendArgs([]string{"--no-auto-close", "investigate"})
	if err != nil {
		t.Fatal(err)
	}
	if !args.noAutoClose {
		t.Error("--no-auto-close should set noAutoClose=true")
	}
	if args.prompt != "investigate" {
		t.Errorf("prompt should land after the flag; got %q", args.prompt)
	}
}

// TestParseSendArgs_NoAutoCloseDefault confirms the legacy default —
// flag absent → noAutoClose stays false, so Send() does NOT thread
// the auto_close key into opts and the supervisor's autoCloseFromOpts
// returns the default true.
func TestParseSendArgs_NoAutoCloseDefault(t *testing.T) {
	args, err := parseSendArgs([]string{"prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if args.noAutoClose {
		t.Error("noAutoClose should default to false")
	}
}

// TestParseSendArgs_ModeFlag covers the routing-mode passthrough
// (peer-prefer / peer-only / auto-tmux / spawn-only). The CLI just
// surfaces the same string the SendMessage MCP `mode` arg accepts;
// validation lives in the supervisor's parseSendMode.
func TestParseSendArgs_ModeFlag(t *testing.T) {
	args, err := parseSendArgs([]string{"--mode", "auto-tmux", "go"})
	if err != nil {
		t.Fatal(err)
	}
	if args.mode != "auto-tmux" {
		t.Errorf("mode: got %q, want auto-tmux", args.mode)
	}
}

func TestParseSendArgs_ModeWithoutValueErrors(t *testing.T) {
	_, err := parseSendArgs([]string{"--mode"})
	if err == nil {
		t.Error("--mode without value should error")
	}
}

// TestSend_NoAutoCloseFlag_OptsWiring asserts the load-bearing
// contract end-to-end at the CLI layer: when `--no-auto-close` is
// parsed, Send() builds an opts map with `auto_close=false` (typed
// bool) before handing off to the supervisor. We exercise this by
// going through the same code path Send() uses to assemble opts;
// the build is small enough that we can inline it here without
// dragging the supervisor in.
//
// The test's invariant: the CLI MUST emit opts["auto_close"] = false
// (bool, not string) so the supervisor's autoCloseFromOpts switch
// hits the bool branch and matches the MCP path byte-for-byte.
func TestSend_NoAutoCloseFlag_OptsWiring(t *testing.T) {
	// Mirror the opts-assembly block in Send(). Keeping this
	// inline (rather than refactoring Send into a helper) avoids
	// dragging the supervisor / unattended / worktree side-effects
	// just to assert one map field.
	args := sendArgs{
		agent:       "codex",
		prompt:      "go",
		noAutoClose: true,
		mode:        "auto-tmux",
	}
	opts := buildSendOpts(args)
	v, ok := opts["auto_close"]
	if !ok {
		t.Fatal(`opts["auto_close"] missing; --no-auto-close MUST thread the key through`)
	}
	b, isBool := v.(bool)
	if !isBool {
		t.Fatalf(`opts["auto_close"] should be a bool; got %T`, v)
	}
	if b {
		t.Error(`opts["auto_close"] should be false when --no-auto-close was passed`)
	}
	if got := opts["mode"]; got != "auto-tmux" {
		t.Errorf(`opts["mode"]: got %v, want "auto-tmux"`, got)
	}
}

// TestSend_DefaultDoesNotEmitAutoClose locks in the back-compat
// invariant: when the flag is NOT set, the CLI MUST NOT emit an
// auto_close key at all. Pre-v0.22.109 supervisor releases never
// saw the key; introducing a default `true` here would break the
// MCP path's "missing key = legacy default" contract.
func TestSend_DefaultDoesNotEmitAutoClose(t *testing.T) {
	args := sendArgs{agent: "codex", prompt: "go"}
	opts := buildSendOpts(args)
	if _, ok := opts["auto_close"]; ok {
		t.Error(`opts["auto_close"] must be absent when --no-auto-close is not set`)
	}
}

// TestSend_UnattendedEnvPropagation locks in the ADR-023 Q2
// resolution: when `clawtool send --unattended` is invoked, the
// dispatch MUST set CLAWTOOL_UNATTENDED=1 on the current process
// env so the spawned upstream peer (codex / gemini / opencode /
// claude) inherits unattended mode without re-acquiring consent.
//
// Two directions:
//
//  1. Flag → env: --unattended is parsed, buildSendOpts stamps
//     CLAWTOOL_UNATTENDED=1 on os.Environ() so spawned upstreams
//     inherit it.
//
//  2. Env → flag: a parent dispatch already set
//     CLAWTOOL_UNATTENDED=1; a nested `clawtool send` (without
//     re-passing --unattended) reads it back and stays in
//     unattended mode. Mirrors the CLAWTOOL_AGENT precedence
//     chain.
func TestSend_UnattendedEnvPropagation(t *testing.T) {
	t.Run("flag stamps env for children", func(t *testing.T) {
		t.Setenv(EnvUnattended, "")
		args := sendArgs{agent: "codex", prompt: "go", unattended: true}
		_ = buildSendOpts(args)
		if got := os.Getenv(EnvUnattended); got != "1" {
			t.Errorf("CLAWTOOL_UNATTENDED after dispatch = %q, want %q", got, "1")
		}
	})

	t.Run("env promotes to flag in nested call", func(t *testing.T) {
		t.Setenv(EnvUnattended, "1")
		args := sendArgs{agent: "codex", prompt: "go"} // no --unattended
		promoted := resolveUnattendedFromEnv(args)
		if !promoted.unattended {
			t.Error("CLAWTOOL_UNATTENDED=1 in env should promote args.unattended")
		}
	})
}

// TestSend_UnattendedNoEnvPropagationByDefault is the negative
// control: a vanilla `clawtool send` (no flag, no env) MUST NOT
// stamp CLAWTOOL_UNATTENDED on the process env. Spawned upstreams
// stay in attended (interactive-approval) mode by default.
func TestSend_UnattendedNoEnvPropagationByDefault(t *testing.T) {
	t.Setenv(EnvUnattended, "")
	args := sendArgs{agent: "codex", prompt: "go"}
	_ = buildSendOpts(args)
	if got := os.Getenv(EnvUnattended); got == "1" {
		t.Errorf("CLAWTOOL_UNATTENDED should stay unset for a vanilla send; got %q", got)
	}
}

// TestSend_UnattendedRejectsNonCanonicalEnv guards against a stray
// CLAWTOOL_UNATTENDED=0 (or "true", "yes", etc.) silently re-arming
// unattended mode. Only the canonical "1" form promotes.
func TestSend_UnattendedRejectsNonCanonicalEnv(t *testing.T) {
	for _, v := range []string{"0", "true", "yes", "TRUE", "  1  ", ""} {
		t.Run("env="+v, func(t *testing.T) {
			t.Setenv(EnvUnattended, v)
			args := sendArgs{agent: "codex", prompt: "go"}
			promoted := resolveUnattendedFromEnv(args)
			if promoted.unattended {
				t.Errorf("non-canonical env %q should NOT promote args.unattended", v)
			}
		})
	}
}
