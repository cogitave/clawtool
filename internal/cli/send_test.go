package cli

import "testing"

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
		"investigate the regression",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args.agent != "codex1" || args.session != "abc-123" || args.model != "gpt-5.2" || args.format != "stream-json" {
		t.Errorf("flags not parsed: %+v", args)
	}
	if args.prompt != "investigate the regression" {
		t.Errorf("prompt: got %q", args.prompt)
	}
}
