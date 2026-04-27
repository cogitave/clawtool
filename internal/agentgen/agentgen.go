// Package agentgen scaffolds Claude Code subagent definitions —
// the YAML-frontmatter + markdown-body files that live under
// `~/.claude/agents/<name>.md` (or `./.claude/agents/<name>.md`
// for project-scoped). Sister of skillgen: same template-renderer
// pattern, same dual-surface (CLI + MCP) ownership rules.
//
// Why this lives here, not in cli or tools/core: both the
// `clawtool agent new` CLI and the AgentNew MCP tool need the
// same templating + validation. Putting Render and IsValidName
// in a leaf package lets each surface stay an importer rather
// than re-implementing the renderer.
//
// Terminology distinction (per operator's 2026-04-27 ruling):
//   - **agent** = a USER-DEFINED PERSONA (this package). A
//     persona has a name, description, allowed-tools list,
//     system-prompt body, and OPTIONALLY a default `instance`
//     it dispatches to via clawtool's SendMessage layer.
//   - **instance** = a configured running upstream CLI bridge
//     (claude, codex, opencode, gemini, hermes, openclaw, …).
//     Lives in internal/agents/supervisor.go (legacy package
//     name; pre-dates this terminology split). An agent is
//     ASSIGNED an instance; instances are not the agent.
package agentgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsValidName enforces kebab-case [a-z0-9-]+ with no leading or
// trailing dash. Same rule skillgen uses; keeps agent file paths
// portable and prevents hyphen-prefix shell-arg footguns.
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

// ParseTools turns "a, b ,c" into ["a","b","c"] — comma-separated,
// whitespace-trimmed, empties dropped. Used for both CLI flags
// and MCP arguments to populate the frontmatter `tools:` list.
func ParseTools(raw string) []string {
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

// RenderArgs bundles every input the renderer needs. We use a
// struct rather than positional args so adding new fields (e.g.
// `model`, `instance`) is a non-breaking change for callers.
type RenderArgs struct {
	Name        string
	Description string
	// Tools is the frontmatter `tools:` list — what Claude Code
	// will whitelist for this subagent. Empty = inherit parent
	// agent's tool set (Claude Code's default).
	Tools []string
	// Instance is the optional default clawtool instance this
	// agent dispatches to. When set, the body includes a
	// "Default instance: <name>" line so the agent and the
	// reader both know which upstream gets called.
	Instance string
	// Model is the optional `model:` frontmatter field
	// (sonnet | haiku | opus). Empty = Claude Code default.
	Model string
}

// Render builds the subagent definition file: YAML frontmatter
// followed by a body skeleton. Output is byte-identical between
// the CLI and MCP surfaces because both go through this function.
func Render(args RenderArgs) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", args.Name)
	b.WriteString("description: >\n")
	for _, line := range wrapDescription(args.Description) {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	if len(args.Tools) > 0 {
		fmt.Fprintf(&b, "tools: %s\n", strings.Join(args.Tools, ", "))
	}
	if args.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", args.Model)
	}
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", args.Name)
	fmt.Fprintf(&b, "%s\n\n", args.Description)

	if args.Instance != "" {
		fmt.Fprintf(&b, "**Default instance:** `%s` — when this agent dispatches via\n", args.Instance)
		b.WriteString("`mcp__clawtool__SendMessage`, it routes to this instance unless\n")
		b.WriteString("the operator overrides via `--agent`.\n\n")
	}

	b.WriteString("## When to fire\n\n")
	b.WriteString("Describe the situations or operator phrases that should\n")
	b.WriteString("make the parent agent dispatch this subagent. Be concrete —\n")
	b.WriteString("vague triggers cause the agent to never (or always) fire.\n\n")

	b.WriteString("## When NOT to fire\n\n")
	b.WriteString("- Tasks better routed to a different agent (name them).\n")
	b.WriteString("- Operations the parent agent can do directly without\n")
	b.WriteString("  dispatching a subagent.\n\n")

	b.WriteString("## Workflow\n\n")
	b.WriteString("1. **Step one** — what to do first when fired.\n")
	b.WriteString("2. **Step two** — the next checkpoint.\n")
	b.WriteString("3. **Synthesize** — return a single, decision-shaped reply\n")
	b.WriteString("   to the parent agent. Don't paste raw transcripts.\n\n")

	b.WriteString("## Output budget\n\n")
	b.WriteString("Default to ~400 words. Tighter when the answer is yes/no;\n")
	b.WriteString("longer only when the operator's decision needs the detail.\n")
	return b.String()
}

// UserAgentsRoot returns ~/.claude/agents (or $CLAUDE_HOME/agents
// when set). Never empty — degrades to ".claude/agents" if the
// home directory can't be resolved.
func UserAgentsRoot() string {
	if x := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); x != "" {
		return filepath.Join(x, "agents")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "agents")
	}
	return ".claude/agents"
}

// LocalAgentsRoot is the project-scope analogue: ./.claude/agents.
func LocalAgentsRoot() string { return ".claude/agents" }

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
