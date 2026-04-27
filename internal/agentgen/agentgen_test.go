package agentgen

import (
	"strings"
	"testing"
)

func TestIsValidName(t *testing.T) {
	cases := map[string]bool{
		"deep-grep":    true,
		"codex-rescue": true,
		"a":            true,
		"agent-1":      true,
		"":             false,
		"-leading":     false,
		"trailing-":    false,
		"With-Caps":    false,
		"snake_case":   false,
		"has spaces":   false,
		"multi--dash":  true, // permitted; doublestar not banned
	}
	for name, want := range cases {
		if got := IsValidName(name); got != want {
			t.Errorf("IsValidName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParseTools(t *testing.T) {
	cases := map[string][]string{
		"":        nil,
		"   ":     nil,
		"a":       {"a"},
		"a, b ,c": {"a", "b", "c"},
		"mcp__clawtool__SendMessage,mcp__clawtool__TaskNotify": {"mcp__clawtool__SendMessage", "mcp__clawtool__TaskNotify"},
		" trailing , , empty ":                                 {"trailing", "empty"},
	}
	for in, want := range cases {
		got := ParseTools(in)
		if len(got) != len(want) {
			t.Errorf("ParseTools(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("ParseTools(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestRender_MinimalFrontmatter(t *testing.T) {
	out := Render(RenderArgs{
		Name:        "deep-grep",
		Description: "Codebase exploration subagent.",
	})
	want := []string{
		"---\n",
		"name: deep-grep\n",
		"description: >\n",
		"  Codebase exploration subagent.\n",
		"---\n",
		"# deep-grep\n",
		"## When to fire",
		"## When NOT to fire",
		"## Workflow",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("Render output missing %q\n--- got:\n%s", w, out)
		}
	}
	// No optional fields when not set.
	for _, banned := range []string{"tools:", "model:", "Default instance:"} {
		if strings.Contains(out, banned) {
			t.Errorf("Render output unexpectedly contains %q\n--- got:\n%s", banned, out)
		}
	}
}

func TestRender_AllOptionalFields(t *testing.T) {
	out := Render(RenderArgs{
		Name:        "research-fanout",
		Description: "Parallel multi-agent research.",
		Tools:       []string{"mcp__clawtool__SendMessage", "Read", "Glob"},
		Instance:    "codex",
		Model:       "sonnet",
	})
	for _, want := range []string{
		"name: research-fanout",
		"description: >",
		"tools: mcp__clawtool__SendMessage, Read, Glob",
		"model: sonnet",
		"# research-fanout",
		"**Default instance:** `codex`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Render output missing %q\n--- got:\n%s", want, out)
		}
	}
}

func TestUserAgentsRoot_NotEmpty(t *testing.T) {
	if UserAgentsRoot() == "" {
		t.Fatal("UserAgentsRoot returned empty string")
	}
}
