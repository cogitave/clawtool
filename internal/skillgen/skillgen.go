// Package skillgen produces SKILL.md content per the agentskills.io
// standard. Lives here, not in cli or tools/core, so both surfaces
// (the `clawtool skill new` CLI and the SkillNew MCP tool) share
// one template + one validator without forcing a leaf-package to
// import its sibling.
package skillgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsValidName enforces kebab-case [a-z0-9-]+ with no leading or
// trailing dash. Both the directory name and the frontmatter
// `name` field use this — keeps file paths portable across
// filesystems and avoids hyphen-prefix shell-arg footguns.
func IsValidName(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

// ParseTriggers turns "a, b ,c" into ["a","b","c"] — comma-
// separated, whitespace-trimmed, empties dropped. Used for both
// CLI flags and MCP arguments.
func ParseTriggers(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Render builds SKILL.md content per agentskills.io: YAML
// frontmatter (name + description, optional triggers) followed
// by a body skeleton with the recommended sections. Description
// is folded onto multiple lines so YAML readers parse cleanly.
func Render(name, description string, triggers []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	b.WriteString("description: >\n")
	for _, line := range wrapDescription(description) {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	if len(triggers) > 0 {
		b.WriteString("triggers:\n")
		for _, t := range triggers {
			fmt.Fprintf(&b, "  - %q\n", t)
		}
	}
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "%s\n\n", description)
	b.WriteString("## When to use this skill\n\n")
	if len(triggers) > 0 {
		b.WriteString("Triggers on phrases like:\n\n")
		for _, t := range triggers {
			fmt.Fprintf(&b, "- %q\n", t)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("Describe the situations / phrases that should make the agent load this skill.\n\n")
	}
	b.WriteString("## How to apply\n\n")
	b.WriteString("1. Step one — what the agent should do first.\n")
	b.WriteString("2. Step two — and so on.\n\n")
	b.WriteString("## Resources\n\n")
	b.WriteString("- `scripts/` — executable helpers the agent can run.\n")
	b.WriteString("- `references/` — reference material the agent can read on demand.\n")
	b.WriteString("- `assets/` — templates, fixtures, sample inputs.\n")
	return b.String()
}

// UserSkillsRoot returns ~/.claude/skills (or $CLAUDE_HOME/skills
// when set). Never empty — degrades to ".claude/skills" if the
// home directory can't be resolved.
func UserSkillsRoot() string {
	if x := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); x != "" {
		return filepath.Join(x, "skills")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "skills")
	}
	return ".claude/skills"
}

// LocalSkillsRoot is the project-scope analogue: ./.claude/skills.
func LocalSkillsRoot() string { return ".claude/skills" }

// wrapDescription folds long descriptions onto multiple lines so
// the YAML block-scalar reads cleanly. ~78 chars per line.
func wrapDescription(s string) []string {
	const width = 78
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() == 0 {
			cur.WriteString(w)
			continue
		}
		if cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
			continue
		}
		cur.WriteByte(' ')
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
