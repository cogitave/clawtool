// Package core — RulesCheck MCP tool. Surfaces the rules engine
// (internal/rules) so an agent can ask "are the operator's
// invariants satisfied right now?" without first having to call
// the unattended-mode supervisor or wait for pre_commit time.
//
// This tool is read-only: it loads .clawtool/rules.toml (or the
// XDG fallback), evaluates against a caller-supplied Context, and
// returns the Verdict (results + warnings + blocked). It does NOT
// hook into Edit/Write/Bash automatically — rule enforcement at
// tool-call time lands when the Tool Manifest Registry refactor
// (#173) gives us a middleware seam.
package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/checkpoint"
	"github.com/cogitave/clawtool/internal/rules"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type rulesCheckResult struct {
	BaseResult
	RulesPath  string        `json:"rules_path,omitempty"`
	Configured bool          `json:"configured"`
	Verdict    rules.Verdict `json:"verdict"`
	Summary    rulesSummary  `json:"summary"`
}

type rulesSummary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Warned  int `json:"warned"`
	Blocked int `json:"blocked"`
	Skipped int `json:"skipped"` // rules whose `when` didn't match the event
}

func (r rulesCheckResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("rules-check")
	}
	var b strings.Builder
	if !r.Configured {
		b.WriteString("(no rules configured — drop a .clawtool/rules.toml or ~/.config/clawtool/rules.toml to start enforcing operator invariants)\n\n")
		b.WriteString(r.FooterLine("event=" + string(r.Verdict.Event)))
		return b.String()
	}
	fmt.Fprintf(&b, "rules: %d total · %d passed · %d warned · %d blocked\n",
		r.Summary.Total, r.Summary.Passed, r.Summary.Warned, r.Summary.Blocked)
	fmt.Fprintf(&b, "source: %s · event: %s\n\n", r.RulesPath, r.Verdict.Event)

	if len(r.Verdict.Blocked) > 0 {
		b.WriteString("BLOCKED:\n")
		for _, res := range r.Verdict.Blocked {
			fmt.Fprintf(&b, "  ✗ %s — %s\n", res.Rule, res.Reason)
			if res.Hint != "" {
				fmt.Fprintf(&b, "     hint: %s\n", res.Hint)
			}
		}
		b.WriteByte('\n')
	}
	if len(r.Verdict.Warnings) > 0 {
		b.WriteString("WARNINGS:\n")
		for _, res := range r.Verdict.Warnings {
			fmt.Fprintf(&b, "  ! %s — %s\n", res.Rule, res.Reason)
			if res.Hint != "" {
				fmt.Fprintf(&b, "     hint: %s\n", res.Hint)
			}
		}
		b.WriteByte('\n')
	}
	if r.Summary.Passed > 0 && len(r.Verdict.Blocked) == 0 && len(r.Verdict.Warnings) == 0 {
		b.WriteString("✓ all rules pass for this event\n\n")
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterRulesCheck wires the RulesCheck tool. Idempotent.
func RegisterRulesCheck(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"RulesCheck",
			mcp.WithDescription(
				"Evaluate the operator's clawtool rules (internal/rules engine, "+
					".clawtool/rules.toml) against a caller-supplied Context. "+
					"Returns the Verdict — every applicable rule's pass/fail with "+
					"reasons and hints. Use this BEFORE committing / dispatching / "+
					"ending a session to confirm the operator's invariants hold. "+
					"Read-only: doesn't modify state, doesn't fire any rule's "+
					"side effect.",
			),
			mcp.WithString("event", mcp.Required(),
				mcp.Description("Lifecycle event to evaluate against. Allowed: pre_commit, post_edit, session_end, pre_send, pre_unattended.")),
			mcp.WithArray("changed_paths",
				mcp.Description("Forward-slash paths (relative to repo root) modified in this session / commit / edit. Backs `changed(glob)` predicates."),
				mcp.Items(map[string]any{"type": "string"}),
			),
			mcp.WithString("commit_message",
				mcp.Description("Proposed commit message body (for pre_commit). Backs `commit_message_contains(s)`.")),
			mcp.WithObject("tool_calls",
				mcp.Description("Map of tool_name → invocation count for the current session. Backs `tool_call_count(name) > N`."),
			),
			mcp.WithObject("args",
				mcp.Description("Free-form key→string map for predicates that aren't typed yet (e.g. SendMessage's instance arg). Backs `arg(key) == value`."),
			),
		),
		runRulesCheck,
	)
}

func runRulesCheck(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	event, err := req.RequireString("event")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: event"), nil
	}
	if !rules.IsValidEvent(rules.Event(event)) {
		return mcp.NewToolResultError(fmt.Sprintf(
			"invalid event %q (allowed: pre_commit, post_edit, session_end, pre_send, pre_unattended)", event)), nil
	}

	start := time.Now()
	out := rulesCheckResult{
		BaseResult: BaseResult{Operation: "RulesCheck", Engine: "rules"},
	}

	loaded, path, configured, loadErr := rules.LoadDefault()
	out.RulesPath = path
	out.Configured = configured
	if loadErr != nil {
		out.ErrorReason = fmt.Sprintf("load %s: %v", path, loadErr)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Build the Context.
	ctx := rules.Context{
		Event: rules.Event(event),
		Now:   time.Now(),
	}
	if pathsRaw := req.GetArguments()["changed_paths"]; pathsRaw != nil {
		if arr, ok := pathsRaw.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					ctx.ChangedPaths = append(ctx.ChangedPaths, s)
				}
			}
		}
	}
	ctx.CommitMessage = req.GetString("commit_message", "")
	if tcRaw := req.GetArguments()["tool_calls"]; tcRaw != nil {
		if m, ok := tcRaw.(map[string]any); ok {
			ctx.ToolCalls = make(map[string]int, len(m))
			for k, v := range m {
				switch n := v.(type) {
				case float64:
					ctx.ToolCalls[k] = int(n)
				case int:
					ctx.ToolCalls[k] = n
				}
			}
		}
	}
	if argsRaw := req.GetArguments()["args"]; argsRaw != nil {
		if m, ok := argsRaw.(map[string]any); ok {
			ctx.Args = make(map[string]string, len(m))
			for k, v := range m {
				if s, ok := v.(string); ok {
					ctx.Args[k] = s
				}
			}
		}
	}

	// Pre-compute docsync violations for the pre_commit phase so
	// the `docsync_violation()` predicate has data to read. Per
	// ADR-022 §Resolved (2026-05-02), the docsync rule type
	// reuses rules.Severity verbatim — the FS work happens here
	// (caller side), eval.go stays pure. Resolved against the
	// daemon's cwd; tests inject paths via changed_paths so the
	// lookup hits a temp tree.
	if ctx.Event == rules.EventPreCommit && len(ctx.ChangedPaths) > 0 {
		cwd, _ := os.Getwd()
		ctx.DocsyncViolations = checkpoint.CheckDocsync(cwd, ctx.ChangedPaths)
	}

	verdict := rules.Evaluate(loaded, ctx)
	out.Verdict = verdict
	out.Summary = rulesSummary{
		Total:   len(verdict.Results),
		Warned:  len(verdict.Warnings),
		Blocked: len(verdict.Blocked),
	}
	for _, r := range verdict.Results {
		if r.Passed {
			out.Summary.Passed++
		}
	}
	out.Summary.Skipped = len(loaded) - out.Summary.Total
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}
