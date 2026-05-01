// SkillList / SkillLoad MCP tools — the on-demand skill mount
// pattern (ADR-029 phase 3, task #208).
//
// claude.ai mounts /mnt/skills/public/<name>/SKILL.md into the
// container's filesystem; the model issues `view` / `read` to
// pull a skill into the current turn's context. The clawtool
// equivalent: SkillList enumerates installed Agent Skills,
// SkillLoad returns one skill's full content (frontmatter +
// markdown). Same on-demand semantic, different transport
// (MCP tool call vs filesystem read).
//
// Skill discovery roots (resolved on each call so re-installs
// without restart pick up new skills):
//
//  1. `./.claude/skills/<name>/SKILL.md`        (project)
//  2. `~/.claude/skills/<name>/SKILL.md`        (user)
//  3. `$CLAWTOOL_SKILLS_DIR/<name>/SKILL.md`    (override; tests)
//
// Lookup precedence: project beats user beats override.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/skillgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterSkillLoad adds the SkillLoad tool. Pairs with the
// pre-existing SkillNew (CLI scaffolder) and with the new
// SkillList tool so a model can discover-then-load.
func RegisterSkillLoad(s *server.MCPServer) {
	tool := mcp.NewTool(
		"SkillLoad",
		mcp.WithDescription(
			"Load one Agent Skill's content (frontmatter + body) by name. "+
				"Use this when you've decided to apply a skill the operator has "+
				"installed — list available skills via SkillList first. "+
				"Lookup precedence: ./.claude/skills/<name>/SKILL.md > "+
				"~/.claude/skills/<name>/SKILL.md > $CLAWTOOL_SKILLS_DIR/<name>.",
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Skill folder name, e.g. \"docx\" or \"frontend-design\"."),
		),
	)
	s.AddTool(tool, runSkillLoad)
}

// RegisterSkillList exposes installed skills on the MCP plane.
// CLI has `clawtool skill list` already; this lets a model
// enumerate skills before deciding which one to SkillLoad.
func RegisterSkillList(s *server.MCPServer) {
	tool := mcp.NewTool(
		"SkillList",
		mcp.WithDescription(
			"Discover Agent Skills installed on this host. Use FIRST when the "+
				"operator says \"use the X skill\" or you suspect a relevant "+
				"skill exists — returns each skill's name, scope "+
				"(project|user|catalog), description from frontmatter, and "+
				"absolute SKILL.md path. Pair with SkillLoad to pull one "+
				"skill's full content into the current turn. NOT for loading "+
				"skill content — use SkillLoad. Read-only; cheap.",
		),
	)
	s.AddTool(tool, runSkillList)
}

// ─── handlers ────────────────────────────────────────────────────

type skillLoadResult struct {
	BaseResult
	Name        string `json:"name"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
	SizeBytes   int    `json:"size_bytes"`
}

func (r skillLoadResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "skill: %s (%s)\n", r.Name, r.Scope)
	if r.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", r.Description)
	}
	b.WriteString("\n---\n")
	b.WriteString(r.Content)
	if !strings.HasSuffix(r.Content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(r.FooterLine(
		fmt.Sprintf("path: %s", r.Path),
		fmt.Sprintf("size: %dB", r.SizeBytes),
	))
	return b.String()
}

type skillListEntry struct {
	Name        string `json:"name"`
	Scope       string `json:"scope"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type skillListResult struct {
	BaseResult
	Skills []skillListEntry `json:"skills"`
	Count  int              `json:"count"`
}

func (r skillListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("SkillList")
	}
	if len(r.Skills) == 0 {
		return "(no Agent Skills installed)\n→ clawtool skill new my-first-skill --description \"...\"\n"
	}
	var b strings.Builder
	for _, s := range r.Skills {
		fmt.Fprintf(&b, "  %s\t%s\t%s\n", s.Name, s.Scope, s.Description)
	}
	b.WriteString(r.FooterLine(fmt.Sprintf("%d skill(s)", len(r.Skills))))
	return b.String()
}

func runSkillLoad(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: name"), nil
	}
	if !validSkillName(name) {
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid skill name %q: lowercase letters / digits / hyphens only", name)), nil
	}
	scope, path, err := resolveSkill(name)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read skill: %v", err)), nil
	}
	desc := extractSkillDescription(string(body))
	out := skillLoadResult{
		BaseResult:  BaseResult{Operation: "SkillLoad"},
		Name:        name,
		Path:        path,
		Scope:       scope,
		Description: desc,
		Content:     string(body),
		SizeBytes:   len(body),
	}
	return resultOf(out), nil
}

func runSkillList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	skills, err := enumerateSkills()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := skillListResult{
		BaseResult: BaseResult{Operation: "SkillList"},
		Skills:     skills,
		Count:      len(skills),
	}
	return resultOf(out), nil
}

// ─── lookup helpers ──────────────────────────────────────────────

// resolveSkill walks the precedence chain and returns the first
// directory containing SKILL.md for the given name. Empty result
// surfaces a clear "not installed" error so the model knows to
// SkillList first.
func resolveSkill(name string) (scope, path string, err error) {
	candidates := []struct{ scope, root string }{
		{"project", skillgen.LocalSkillsRoot()},
		{"user", skillgen.UserSkillsRoot()},
	}
	if x := strings.TrimSpace(os.Getenv("CLAWTOOL_SKILLS_DIR")); x != "" {
		candidates = append(candidates, struct{ scope, root string }{"catalog", x})
	}
	for _, c := range candidates {
		p := filepath.Join(c.root, name, "SKILL.md")
		if _, statErr := os.Stat(p); statErr == nil {
			return c.scope, p, nil
		}
	}
	return "", "", fmt.Errorf("skill %q not installed (checked project + user roots)", name)
}

// enumerateSkills walks every root and collects deduped skill
// entries. Project beats user; later duplicates are skipped.
func enumerateSkills() ([]skillListEntry, error) {
	roots := []struct{ scope, root string }{
		{"project", skillgen.LocalSkillsRoot()},
		{"user", skillgen.UserSkillsRoot()},
	}
	if x := strings.TrimSpace(os.Getenv("CLAWTOOL_SKILLS_DIR")); x != "" {
		roots = append(roots, struct{ scope, root string }{"catalog", x})
	}
	seen := map[string]bool{}
	var out []skillListEntry
	for _, r := range roots {
		entries, err := os.ReadDir(r.root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", r.root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if seen[name] {
				continue
			}
			skillPath := filepath.Join(r.root, name, "SKILL.md")
			body, rerr := os.ReadFile(skillPath)
			if rerr != nil {
				continue
			}
			seen[name] = true
			out = append(out, skillListEntry{
				Name:        name,
				Scope:       r.scope,
				Path:        skillPath,
				Description: extractSkillDescription(string(body)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// extractSkillDescription pulls the `description:` line from the
// SKILL.md YAML frontmatter. Minimal parser: looks for the field
// between two `---` markers, supports single-line and block-scalar
// (`description: >`) shapes. Empty string when absent or the
// frontmatter is malformed — non-fatal.
func extractSkillDescription(body string) string {
	if !strings.HasPrefix(body, "---\n") {
		return ""
	}
	end := strings.Index(body[4:], "\n---")
	if end < 0 {
		return ""
	}
	front := body[4 : 4+end]
	lines := strings.Split(front, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "description:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(ln, "description:"))
		if val != "" && val != ">" && val != "|" {
			return val
		}
		var b strings.Builder
		for j := i + 1; j < len(lines); j++ {
			cont := lines[j]
			if cont == "" || (len(cont) > 0 && cont[0] != ' ' && cont[0] != '\t') {
				break
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strings.TrimSpace(cont))
		}
		return b.String()
	}
	return ""
}

// validSkillName matches the kebab-case rule skillgen enforces on
// new scaffolds. Defensive — same regex would prevent path
// traversal via name="../../etc/passwd".
func validSkillName(s string) bool {
	if s == "" || len(s) > 64 {
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
