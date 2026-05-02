// Package core — Commit MCP tool. Wraps internal/checkpoint's
// Commit primitive (ADR-022) so an agent can land a Conventional
// Commits-validated, Co-Authored-By-blocked commit through one
// tool call instead of three Bash invocations.
//
// This tool is what closes the operator's earlier gap: agents
// shell out to `Bash git commit -m "feat: …"` because there's no
// Commit tool, the messages aren't always conventional-shaped,
// and Bash has no way to refuse a Co-Authored-By trailer. Commit
// makes the right path the easy path.
//
// Pre-commit guardrails layered through (in order):
//  1. Repo check — bails with a clear error if cwd isn't a Git repo.
//  2. internal/rules.Evaluate at EventPreCommit — operator's
//     declarative invariants (e.g. "skill routing-map row updated"
//     when a core tool changed). A Verdict.IsBlocked() = true is
//     a hard refusal.
//  3. internal/checkpoint.ValidateMessage — Conventional Commits +
//     Co-Authored-By block.
//  4. Optional dirtiness guard — refuses to commit if the working
//     tree still has unstaged changes after staging (catches
//     "you forgot to stage X" mid-flight).
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

type commitToolResult struct {
	BaseResult
	checkpoint.CommitResult
	// RuleViolations is non-empty when the pre_commit rules
	// engine flagged the action. When any have severity=block,
	// the commit is refused and the SHA fields stay empty.
	RuleViolations []rules.Result `json:"rule_violations,omitempty"`
}

func (r commitToolResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Subject)
	}
	var b strings.Builder
	if r.Sha != "" {
		fmt.Fprintf(&b, "✓ %s [%s]\n", r.Subject, r.ShortSha)
		if r.Branch != "" {
			fmt.Fprintf(&b, "  branch: %s\n", r.Branch)
		}
		if len(r.Files) > 0 {
			fmt.Fprintf(&b, "  files: %s\n", strings.Join(r.Files, ", "))
		}
		if r.Pushed {
			b.WriteString("  ✓ pushed\n")
		}
	}
	if len(r.RuleViolations) > 0 {
		b.WriteString("\nrule violations:\n")
		for _, v := range r.RuleViolations {
			marker := "!"
			if v.Severity == rules.SeverityBlock {
				marker = "✗"
			}
			fmt.Fprintf(&b, "  %s %s — %s\n", marker, v.Rule, v.Reason)
			if v.Hint != "" {
				fmt.Fprintf(&b, "    hint: %s\n", v.Hint)
			}
		}
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterCommit wires the Commit MCP tool. Idempotent.
func RegisterCommit(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"Commit",
			mcp.WithDescription(
				"Create a git commit with Conventional Commits validation, "+
					"a hard Co-Authored-By trailer block, and a pre_commit rules.toml "+
					"gate. Use this INSTEAD OF `Bash git commit -m \"…\"` whenever the "+
					"task is shipping a commit — Bash can't enforce the operator's "+
					"policy. Returns the SHA + branch + subject on success; on a rule "+
					"or validation block, returns the violation list and refuses to "+
					"commit.",
			),
			mcp.WithString("message", mcp.Required(),
				mcp.Description("Commit message body. First line must match Conventional Commits 1.0.0: `<type>(<scope>)?(!)?: <subject>`. Type allowlist: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert. Co-Authored-By trailer is hard-blocked.")),
			mcp.WithString("cwd",
				mcp.Description("Repo root. Defaults to the server's current directory.")),
			mcp.WithArray("files",
				mcp.Description("Paths to stage before committing. Empty = use the existing index."),
				mcp.Items(map[string]any{"type": "string"}),
			),
			mcp.WithBoolean("auto_stage_all",
				mcp.Description("Run `git add -A` before commit. Default false.")),
			mcp.WithBoolean("allow_empty",
				mcp.Description("Allow `git commit --allow-empty`. Default false — empty commits are usually a bug.")),
			mcp.WithBoolean("allow_dirty",
				mcp.Description("Bypass the post-stage dirtiness guard. Default false.")),
			mcp.WithBoolean("require_conventional",
				mcp.Description("Enforce Conventional Commits message shape. Default true.")),
			mcp.WithBoolean("forbid_coauthor",
				mcp.Description("Hard-block Co-Authored-By trailer. Default true (operator policy).")),
			mcp.WithBoolean("push",
				mcp.Description("Run `git push` after commit. Default false.")),
			mcp.WithBoolean("sign",
				mcp.Description("Override `git commit -S`. Omit (default) to honour the operator's `git config commit.gpgsign` (per ADR-022 §Resolved 2026-05-02). Pass true to force-sign or false to force-unsigned regardless of git config.")),
		),
		runCommit,
	)
}

func runCommit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	message, err := req.RequireString("message")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: message"), nil
	}

	opts := checkpoint.CommitOptions{
		Message:             message,
		Cwd:                 req.GetString("cwd", ""),
		AutoStageAll:        req.GetBool("auto_stage_all", false),
		AllowEmpty:          req.GetBool("allow_empty", false),
		AllowDirty:          req.GetBool("allow_dirty", false),
		RequireConventional: req.GetBool("require_conventional", true),
		ForbidCoauthor:      req.GetBool("forbid_coauthor", true),
		Push:                req.GetBool("push", false),
	}
	// `sign` is the per-call override (per ADR-022 §Resolved
	// 2026-05-02). Only set the pointer when the caller actually
	// passed the argument — leaving it nil tells checkpoint.Run
	// to consult `git config --get commit.gpgsign` and propagate
	// the operator's configured preference.
	if rawSign, ok := req.GetArguments()["sign"]; ok {
		if v, ok := rawSign.(bool); ok {
			opts.Sign = checkpoint.BoolPtr(v)
		}
	}
	// Files is the only array argument; mcp-go decodes []any.
	if raw, ok := req.GetArguments()["files"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				opts.Files = append(opts.Files, s)
			}
		}
	}

	start := time.Now()
	out := commitToolResult{
		BaseResult: BaseResult{Operation: "Commit", Engine: "git"},
	}

	if opts.Cwd == "" {
		opts.Cwd, _ = os.Getwd()
	}
	if !checkpoint.IsGitRepo(opts.Cwd) {
		out.ErrorReason = fmt.Sprintf("not a git repository: %s", opts.Cwd)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Validate message FIRST — message-shape problems are cheap
	// to detect and don't need any git state.
	if err := checkpoint.ValidateMessage(message, opts); err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Stage BEFORE rules evaluation so the rules engine's
	// `changed(glob)` predicate has a populated ChangedPaths
	// from `git diff --name-only --cached`. The previous order
	// (rules → validate → stage) meant every rule referencing
	// changed() saw an empty list under direct Commit invocations
	// — Codex pass-2 review flagged this as 'declared capability
	// ahead of enforcement'.
	if err := checkpoint.Stage(opts.Cwd, opts.Files, opts.AutoStageAll); err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}

	// Load rules + populate ChangedPaths from the staged index,
	// then evaluate at pre_commit. Loading is best-effort:
	// a missing rules.toml means "no rules", not an error —
	// operator's rules are opt-in.
	if loaded, _, _, lerr := rules.LoadDefault(); lerr == nil && len(loaded) > 0 {
		stagedPaths, _ := checkpoint.StagedFiles(opts.Cwd)
		// Pre-compute docsync violations so the
		// `docsync_violation()` predicate has data. Per
		// ADR-022 §Resolved (2026-05-02), the docsync rule
		// type reuses rules.Severity verbatim — FS work
		// happens here, eval.go stays pure.
		ctxRules := rules.Context{
			Event:             rules.EventPreCommit,
			CommitMessage:     message,
			ChangedPaths:      stagedPaths,
			DocsyncViolations: checkpoint.CheckDocsync(opts.Cwd, stagedPaths),
			Now:               time.Now(),
		}
		v := rules.Evaluate(loaded, ctxRules)
		out.RuleViolations = append(out.RuleViolations, v.Blocked...)
		out.RuleViolations = append(out.RuleViolations, v.Warnings...)
		if v.IsBlocked() {
			out.ErrorReason = fmt.Sprintf("rules.toml blocked the commit (%d rule(s) failed)", len(v.Blocked))
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
	}
	if !opts.AllowDirty {
		// After staging, a remaining dirty status means there are
		// unstaged tracked changes OR untracked files we didn't
		// pick up. Block by default — usually means the operator
		// expected `auto_stage_all` or named the wrong files.
		clean, err := checkpoint.IsClean(opts.Cwd)
		if err == nil && !clean && len(opts.Files) > 0 && !opts.AutoStageAll {
			out.ErrorReason = "working tree still dirty after staging — pass auto_stage_all=true OR allow_dirty=true if intentional"
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
	}

	res, err := checkpoint.Run(ctx, opts)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.CommitResult = res
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}
