// Package core — AgentNew MCP tool. Mirrors `clawtool agent new`
// so a model can scaffold a Claude Code subagent persona from
// inside a conversation. Both surfaces share the same template
// renderer (internal/agentgen) so the output is byte-identical.
//
// Terminology reminder (operator's 2026-04-27 ruling):
//   - **agent** = a USER-DEFINED PERSONA (this tool scaffolds one)
//   - **instance** = a configured upstream CLI bridge (claude /
//     codex / gemini / opencode / hermes / openclaw / ...)
//
// Don't confuse this with the legacy AgentList tool (agents_tool.go),
// which currently still surfaces *instances* under the legacy
// "agent" name. That rename is tracked separately.
package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/agentgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type agentNewResult struct {
	BaseResult
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Tools       []string `json:"tools,omitempty"`
	Instance    string   `json:"instance,omitempty"`
	Model       string   `json:"model,omitempty"`
	Description string   `json:"description"`
	Created     bool     `json:"created"`
	Overwrote   bool     `json:"overwrote"`
}

func (r agentNewResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Name)
	}
	verb := "created"
	if r.Overwrote {
		verb = "overwrote"
	}
	return r.SuccessLine(verb+" agent "+r.Name, r.Path)
}

// RegisterAgentNew adds the AgentNew tool to s. Template + helpers
// come from internal/agentgen so this MCP surface and the
// `clawtool agent new` CLI emit byte-identical files.
func RegisterAgentNew(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AgentNew",
		mcp.WithDescription(
			"Scaffold a Claude Code subagent definition (a USER-DEFINED "+
				"persona — not a bridge or instance). Writes a YAML-frontmatter + "+
				"markdown-body file under ~/.claude/agents/<name>.md (or "+
				"./.claude/agents/<name>.md with location=local). The persona "+
				"can declare allowed-tools, a default clawtool instance to "+
				"dispatch to via SendMessage, and a model preference. Same "+
				"template the `clawtool agent new` CLI emits.",
		),
		mcp.WithString("name", mcp.Required(),
			mcp.Description("Kebab-case agent name, e.g. \"deep-grep\" or \"codex-rescue\". Becomes both the file name and the frontmatter `name` field.")),
		mcp.WithString("description", mcp.Required(),
			mcp.Description("One-paragraph description that tells the parent agent WHEN to dispatch this subagent. Concrete triggers beat vague preferences.")),
		mcp.WithString("tools",
			mcp.Description("Comma-separated allowed-tools whitelist (e.g. \"mcp__clawtool__SendMessage, mcp__clawtool__TaskNotify, Read, Glob\"). Empty = inherit parent.")),
		mcp.WithString("instance",
			mcp.Description("Optional default clawtool instance this agent dispatches to via SendMessage (e.g. \"codex\", \"gemini\"). Body includes a 'Default instance' line so the routing is explicit.")),
		mcp.WithString("model",
			mcp.Description("Optional frontmatter model field: sonnet | haiku | opus. Empty = Claude Code default.")),
		mcp.WithString("location",
			mcp.Description("Where to install. \"user\" → ~/.claude/agents/<name>.md (default), \"local\" → ./.claude/agents/<name>.md.")),
		mcp.WithBoolean("force",
			mcp.Description("Overwrite an existing agent file. Default false.")),
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
		if !agentgen.IsValidName(name) {
			return mcp.NewToolResultError(fmt.Sprintf("invalid agent name %q (kebab-case [a-z0-9-]+ required)", name)), nil
		}
		if strings.TrimSpace(desc) == "" {
			return mcp.NewToolResultError("description must be non-empty"), nil
		}

		tools := agentgen.ParseTools(req.GetString("tools", ""))
		instance := strings.TrimSpace(req.GetString("instance", ""))
		model := strings.TrimSpace(req.GetString("model", ""))
		location := strings.ToLower(strings.TrimSpace(req.GetString("location", "user")))
		force := req.GetBool("force", false)

		var root string
		switch location {
		case "", "user":
			root = agentgen.UserAgentsRoot()
		case "local":
			root = agentgen.LocalAgentsRoot()
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown location %q (allowed: user, local)", location)), nil
		}

		path := filepath.Join(root, name+".md")
		out := agentNewResult{
			BaseResult:  BaseResult{Operation: "AgentNew"},
			Name:        name,
			Path:        path,
			Tools:       tools,
			Instance:    instance,
			Model:       model,
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

		body := agentgen.Render(agentgen.RenderArgs{
			Name:        name,
			Description: desc,
			Tools:       tools,
			Instance:    instance,
			Model:       model,
		})
		if err := os.MkdirAll(root, 0o755); err != nil {
			out.ErrorReason = fmt.Sprintf("mkdir %s: %v", root, err)
			return resultOf(out), nil
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			out.ErrorReason = fmt.Sprintf("write %s: %v", path, err)
			return resultOf(out), nil
		}
		return resultOf(out), nil
	})
}
