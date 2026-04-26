package skillgen

import (
	"strings"
	"testing"
)

func TestIsValidName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"karpathy-llm-wiki", true},
		{"my-skill", true},
		{"a", true},
		{"a1-b2", true},
		{"-leading", false},
		{"trailing-", false},
		{"", false},
		{"Has-Caps", false},
		{"under_score", false},
		{"with space", false},
		{"slash/no", false},
	}
	for _, c := range cases {
		if got := IsValidName(c.in); got != c.want {
			t.Errorf("IsValidName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseTriggers(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"x", []string{"x"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ,c ", []string{"a", "b", "c"}},
		{",,,a,,,", []string{"a"}},
	}
	for _, c := range cases {
		got := ParseTriggers(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseTriggers(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseTriggers(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestRender_FrontmatterAndSections(t *testing.T) {
	body := Render("test-skill", "A short description.", []string{"do x", "do y"})

	for _, want := range []string{
		"---\nname: test-skill\n",
		"description: >",
		"A short description.",
		"triggers:\n",
		`"do x"`,
		`"do y"`,
		"# test-skill",
		"## When to use this skill",
		"## How to apply",
		"## Resources",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Render output missing %q\n---\n%s", want, body)
		}
	}
}

func TestRender_NoTriggersSkipsSection(t *testing.T) {
	body := Render("plain", "no triggers here.", nil)
	if strings.Contains(body, "triggers:") {
		t.Errorf("Render with empty triggers should not emit a triggers list:\n%s", body)
	}
	// The "When to use" section still appears, but with the
	// generic placeholder copy.
	if !strings.Contains(body, "Describe the situations") {
		t.Errorf("expected fallback prose for the When-to-use section; got:\n%s", body)
	}
}

func TestRender_LongDescriptionWrapsAcrossLines(t *testing.T) {
	long := strings.Repeat("word ", 40) // ~200 chars
	body := Render("wrap", long, nil)
	// Must contain at least 2 indented lines under "description: >".
	count := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "  -") {
			count++
		}
		if count >= 2 {
			break
		}
	}
	if count < 2 {
		t.Errorf("long description should wrap to multiple indented lines; got body:\n%s", body)
	}
}

func TestUserSkillsRoot_RespectsClaudeHome(t *testing.T) {
	t.Setenv("CLAUDE_HOME", "/custom/claude")
	got := UserSkillsRoot()
	if got != "/custom/claude/skills" {
		t.Errorf("UserSkillsRoot with CLAUDE_HOME = %q, want /custom/claude/skills", got)
	}
}

func TestLocalSkillsRoot_IsConstant(t *testing.T) {
	if LocalSkillsRoot() != ".claude/skills" {
		t.Errorf("LocalSkillsRoot = %q, want .claude/skills", LocalSkillsRoot())
	}
}
