// Package core — RulesAdd MCP tool. Operator wants agents to be
// able to add rules from any context without hand-editing
// .clawtool/rules.toml. This tool wraps internal/rules.AppendRule
// with an explicit scope (user vs. local) so the file ends up in
// the right place.
//
// Companion to the `clawtool rules new` CLI verb — both go
// through internal/rules.AppendRule, so the on-disk shape is
// byte-identical regardless of which surface added the rule.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/rules"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type rulesAddResult struct {
	BaseResult
	Name      string `json:"name"`
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	When      string `json:"when"`
	Condition string `json:"condition"`
	Severity  string `json:"severity"`
}

func (r rulesAddResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Name)
	}
	return r.SuccessLine(
		fmt.Sprintf("rule %q added (scope=%s, when=%s, severity=%s)",
			r.Name, r.Scope, r.When, r.Severity),
		r.Path)
}

// RegisterRulesAdd wires the RulesAdd tool. Idempotent.
func RegisterRulesAdd(s *server.MCPServer) {
	tool := mcp.NewTool(
		"RulesAdd",
		mcp.WithDescription(
			"Append a new rule to .clawtool/rules.toml (local) or "+
				"~/.config/clawtool/rules.toml (user). Same shape `clawtool "+
				"rules new` writes — both surfaces share internal/rules.AppendRule. "+
				"Validates the condition's predicate DSL syntax BEFORE persisting "+
				"so a malformed add never corrupts existing rules. Use this when "+
				"the operator wants to enforce an invariant (e.g. 'README must "+
				"update when core tools change') without editing the toml by hand.",
		),
		mcp.WithString("name", mcp.Required(),
			mcp.Description("Stable rule identifier. Cannot duplicate an existing name in the same file.")),
		mcp.WithString("when", mcp.Required(),
			mcp.Description("Lifecycle event: pre_commit | post_edit | session_end | pre_send | pre_unattended.")),
		mcp.WithString("condition", mcp.Required(),
			mcp.Description("Predicate DSL: changed(glob) | any_change(glob) | commit_message_contains(s) | tool_call_count(name) <op> N | arg(key) <op> value | true | false. Combine with AND / OR / NOT. See docs/rules.md.")),
		mcp.WithString("severity",
			mcp.Description("off | warn | block. Default warn.")),
		mcp.WithString("description",
			mcp.Description("One-line human description (optional).")),
		mcp.WithString("hint",
			mcp.Description("Operator-facing hint emitted when the rule fires (optional).")),
		mcp.WithString("scope",
			mcp.Description("'local' (default; ./.clawtool/rules.toml) or 'user' ($XDG_CONFIG_HOME/clawtool/rules.toml).")),
	)

	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: name"), nil
		}
		when, err := req.RequireString("when")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: when"), nil
		}
		condition, err := req.RequireString("condition")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: condition"), nil
		}
		severity := strings.TrimSpace(req.GetString("severity", "warn"))
		if severity == "" {
			severity = "warn"
		}
		description := req.GetString("description", "")
		hint := req.GetString("hint", "")
		scope := strings.ToLower(strings.TrimSpace(req.GetString("scope", "local")))

		var path string
		switch scope {
		case "", "local":
			scope = "local"
			path = rules.LocalRulesPath()
		case "user":
			path = rules.UserRulesPath()
		default:
			return mcp.NewToolResultError(fmt.Sprintf(
				"unknown scope %q (allowed: local, user)", scope)), nil
		}

		start := time.Now()
		out := rulesAddResult{
			BaseResult: BaseResult{Operation: "RulesAdd", Engine: "rules"},
			Name:       name,
			Path:       path,
			Scope:      scope,
			When:       when,
			Condition:  condition,
			Severity:   severity,
		}

		rule := rules.Rule{
			Name:        name,
			Description: description,
			When:        rules.Event(when),
			Condition:   condition,
			Severity:    rules.Severity(severity),
			Hint:        hint,
		}
		if err := rules.AppendRule(path, rule); err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}
