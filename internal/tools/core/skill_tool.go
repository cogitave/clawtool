// Package core — SkillNew MCP tool. Mirrors `clawtool skill new`
// so a model can scaffold a Claude Code skill from inside a
// conversation, without the user dropping to a shell. Both
// surfaces share the same template renderer (passed in at
// registration so we don't reach across packages — this file
// stays a leaf).
//
// Spec compliance: agentskills.io. SKILL.md gets YAML frontmatter
// with `name` + `description` (required) plus optional `triggers`,
// followed by the body template. Same shape as the CLI emits.
package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/skillgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type skillNewResult struct {
	BaseResult
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Triggers    []string `json:"triggers,omitempty"`
	Description string   `json:"description"`
	Created     bool     `json:"created"`
	Overwrote   bool     `json:"overwrote"`
}

func (r skillNewResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Name)
	}
	verb := "created"
	if r.Overwrote {
		verb = "overwrote"
	}
	return r.SuccessLine(verb+" "+r.Name, r.Path)
}

// RegisterSkillNew adds the SkillNew tool to s. Template +
// helpers come from internal/skillgen so this MCP surface and
// the `clawtool skill new` CLI emit byte-identical output.
func RegisterSkillNew(s *server.MCPServer) {
	tool := mcp.NewTool(
		"SkillNew",
		mcp.WithDescription(
			"Scaffold a Claude Code skill per the agentskills.io standard: "+
				"a folder containing SKILL.md (frontmatter name+description) plus "+
				"scripts/, references/, assets/ subdirectories. Use this to bootstrap "+
				"a new skill from inside a conversation; the user (or you) edit the body "+
				"afterwards. Same template the `clawtool skill new` CLI emits.",
		),
		mcp.WithString("name", mcp.Required(),
			mcp.Description("Kebab-case skill name, e.g. \"karpathy-llm-wiki\". Becomes both the directory name and the frontmatter `name` field.")),
		mcp.WithString("description", mcp.Required(),
			mcp.Description("One-paragraph description that tells the agent WHEN to load the skill. Required by the agentskills.io spec.")),
		mcp.WithString("triggers",
			mcp.Description("Comma-separated trigger phrases (optional). Captured in frontmatter as `triggers:` list and surfaced in the body's \"When to use\" section.")),
		mcp.WithString("location",
			mcp.Description("Where to install. \"user\" → ~/.claude/skills/<name>/ (default), \"local\" → ./.claude/skills/<name>/.")),
		mcp.WithBoolean("force",
			mcp.Description("Overwrite an existing SKILL.md. Default false.")),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: name"), nil
		}
		desc, err := req.RequireString("description")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: description"), nil
		}
		if !skillgen.IsValidName(name) {
			return mcp.NewToolResultError(fmt.Sprintf("invalid skill name %q (kebab-case [a-z0-9-]+ required)", name)), nil
		}
		if strings.TrimSpace(desc) == "" {
			return mcp.NewToolResultError("description must be non-empty (agentskills.io requires a description)"), nil
		}

		triggers := skillgen.ParseTriggers(req.GetString("triggers", ""))
		location := strings.ToLower(strings.TrimSpace(req.GetString("location", "user")))
		force := req.GetBool("force", false)

		var root string
		switch location {
		case "", "user":
			root = skillgen.UserSkillsRoot()
		case "local":
			root = skillgen.LocalSkillsRoot()
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown location %q (allowed: user, local)", location)), nil
		}

		dir := filepath.Join(root, name)
		path := filepath.Join(dir, "SKILL.md")

		out := skillNewResult{
			BaseResult:  BaseResult{Operation: "SkillNew"},
			Name:        name,
			Path:        dir,
			Triggers:    triggers,
			Description: desc,
		}

		if _, statErr := os.Stat(path); statErr == nil {
			if !force {
				out.ErrorReason = fmt.Sprintf("%s already exists; pass force=true to overwrite", path)
				return resultOf(out), nil
			}
			out.Overwrote = true
		} else {
			out.Created = true
		}

		body := skillgen.Render(name, desc, triggers)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			out.ErrorReason = fmt.Sprintf("mkdir %s: %v", dir, err)
			return resultOf(out), nil
		}
		for _, sub := range []string{"scripts", "references", "assets"} {
			_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
			_ = os.WriteFile(filepath.Join(dir, sub, ".gitkeep"), nil, 0o644)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			out.ErrorReason = fmt.Sprintf("write %s: %v", path, err)
			return resultOf(out), nil
		}
		return resultOf(out), nil
	})
}
