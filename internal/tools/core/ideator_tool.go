// Package core — IdeateRun / IdeateApply MCP tools.
//
// MCP mirror of the `clawtool ideate` CLI. IdeateRun is read-only —
// it surveys repo signals and returns Idea candidates without
// touching the autopilot queue. IdeateApply does the same survey
// then pushes each surviving Idea onto the autopilot backlog at
// status=proposed; an operator running AutopilotAccept (or
// `clawtool autopilot accept <id>`) is the only path from proposed
// to pending.
//
// This separation matches the v0.22.108 description-anatomy guidance:
// the model picks IdeateRun when it wants to *learn* what's wrong,
// IdeateApply when the operator has already greenlit pushing the
// findings into the queue. They share a runtime; the difference is
// purely whether the queue gets written.
package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/autopilot"
	"github.com/cogitave/clawtool/internal/ideator"
	"github.com/cogitave/clawtool/internal/ideator/sources"
	"github.com/cogitave/clawtool/internal/tools/registry"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// init wires the canonical manifest builder into the manifest_drift
// source registry so the source picks up live tool descriptions
// when the MCP server runs without going through the CLI's init.
// The slot accepts any number of writers (last write wins); both
// the CLI's init and this one publish the same builder.
func init() {
	sources.SetDefaultManifestProvider(func() *registry.Manifest {
		return BuildManifest()
	})
}

// ideateResult is the wire shape both Ideate tools return. Mirrors
// ideator.RunResult plus tool-result baseline fields.
type ideateResult struct {
	BaseResult
	Ideas        []ideator.Idea    `json:"ideas"`
	PerSource    map[string]int    `json:"per_source"`
	Deduped      int               `json:"deduped"`
	Added        int               `json:"added,omitempty"`
	Skipped      int               `json:"skipped,omitempty"`
	SourceErrors map[string]string `json:"source_errors,omitempty"`
}

// Render produces the human-readable line + a per-idea bullet list.
func (r ideateResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ideate: %d idea(s)", len(r.Ideas))
	if r.Added > 0 || r.Skipped > 0 {
		fmt.Fprintf(&b, " · added=%d skipped=%d", r.Added, r.Skipped)
	}
	if r.Deduped > 0 {
		fmt.Fprintf(&b, " · deduped=%d", r.Deduped)
	}
	b.WriteByte('\n')
	for i, idea := range r.Ideas {
		fmt.Fprintf(&b, "  %2d. [%s prio=%d] %s\n",
			i+1, idea.SourceName, idea.SuggestedPriority,
			truncateForRender(idea.Title, 100))
		if idea.Evidence != "" {
			fmt.Fprintf(&b, "       evidence: %s\n", idea.Evidence)
		}
	}
	if len(r.PerSource) > 0 {
		b.WriteString("  per-source: ")
		first := true
		for _, name := range []string{"adr_questions", "todos", "ci_failures", "manifest_drift", "bench_regression", "deadcode_hits"} {
			n, ok := r.PerSource[name]
			if !ok {
				continue
			}
			if !first {
				b.WriteString(" · ")
			}
			first = false
			fmt.Fprintf(&b, "%s=%d", name, n)
		}
		b.WriteByte('\n')
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterIdeatorTools wires IdeateRun + IdeateApply onto the MCP
// server. Idempotent — calling twice replaces by name.
func RegisterIdeatorTools(s *server.MCPServer) {
	registerIdeateRun(s)
	registerIdeateApply(s)
}

func registerIdeateRun(s *server.MCPServer) {
	tool := mcp.NewTool(
		"IdeateRun",
		mcp.WithDescription(
			"Survey repo-local signals (open ADR questions, TODO/FIXME "+
				"comments, recent CI failures, MCP manifest description "+
				"drift, ToolSearch BM25 baseline regressions) and return a "+
				"ranked list of Idea candidates. Read-only — does NOT "+
				"touch the autopilot queue. Use when you've finished an "+
				"autopilot session, the queue is empty, and you want to "+
				"discover what to work on next without operator "+
				"re-prompting; pair with IdeateApply once the operator "+
				"approves pushing findings to the backlog. NOT for "+
				"semantic code lookup — IdeateRun mines signals (TODOs, "+
				"ADR open questions, CI failures); SemanticSearch finds "+
				"existing code by intent. Output is the same shape "+
				"`clawtool ideate` prints, plus per-source counts.",
		),
		mcp.WithNumber("top",
			mcp.Description("Cap on returned ideas (default 10). Higher means more breadth, lower means tighter focus.")),
		mcp.WithString("source",
			mcp.Description("Restrict to one source: adr_questions | todos | ci_failures | manifest_drift | bench_regression. Empty = all.")),
		mcp.WithString("repo",
			mcp.Description("Repo root to scan. Default: process cwd.")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return doIdeate(ctx, req, false)
	})
}

func registerIdeateApply(s *server.MCPServer) {
	tool := mcp.NewTool(
		"IdeateApply",
		mcp.WithDescription(
			"Survey repo-local signals (same as IdeateRun) and push every "+
				"surviving Idea onto the autopilot backlog at "+
				"status=proposed. Operator approval (AutopilotAccept / "+
				"`clawtool autopilot accept <id>`) is the ONLY path from "+
				"proposed → pending; without that gate the agent never "+
				"claims the work via AutopilotNext. Use when the operator "+
				"has explicitly told you to push the Ideator's findings "+
				"to the queue (\"go put your suggestions in the backlog "+
				"and I'll review\"). NOT for read-only investigation — "+
				"call IdeateRun for that. Returns the queued ideas plus "+
				"added / skipped (DedupeKey collision) counts.",
		),
		mcp.WithNumber("top",
			mcp.Description("Cap on queued ideas (default 10).")),
		mcp.WithString("source",
			mcp.Description("Restrict to one source: adr_questions | todos | ci_failures | manifest_drift | bench_regression. Empty = all.")),
		mcp.WithString("repo",
			mcp.Description("Repo root to scan. Default: process cwd.")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return doIdeate(ctx, req, true)
	})
}

// doIdeate is the shared body for the two MCP tools — the apply
// flag is the only behavioural delta.
func doIdeate(ctx context.Context, req mcp.CallToolRequest, apply bool) (*mcp.CallToolResult, error) {
	start := time.Now()
	op := "IdeateRun"
	if apply {
		op = "IdeateApply"
	}
	out := ideateResult{
		BaseResult: BaseResult{Operation: op, Engine: "ideator"},
	}
	top := int(req.GetFloat("top", 0))
	source := req.GetString("source", "")
	repo := strings.TrimSpace(req.GetString("repo", ""))
	if repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			repo = cwd
		}
	}

	opts := ideator.Options{
		RepoRoot:     repo,
		SourceFilter: source,
		TopK:         top,
		Sources:      defaultIdeatorSources(),
	}
	var (
		res ideator.RunResult
		err error
	)
	if apply {
		res, err = ideator.RunAndQueue(ctx, opts, autopilot.Open())
	} else {
		res, err = ideator.Run(ctx, opts)
	}
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Ideas = res.Ideas
	out.PerSource = res.PerSource
	out.Deduped = res.Deduped
	out.Added = res.Added
	out.Skipped = res.Skipped
	out.SourceErrors = res.SourceErrors
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

// defaultIdeatorSources is the canonical bundle assembled at the
// MCP edge — same five sources the CLI wires.
func defaultIdeatorSources() []ideator.IdeaSource {
	return []ideator.IdeaSource{
		sources.NewADRQuestions(),
		sources.NewTODOs(),
		sources.NewCIFailures(),
		sources.NewManifestDrift(),
		sources.NewBenchRegression(),
		sources.NewDeadcodeHits(),
	}
}
