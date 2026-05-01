// Package core — ToolSearch is the discovery primitive that makes a 50+
// tool catalog usable per ADR-005. The agent calls ToolSearch with a
// natural-language query, gets ranked candidates, then binds to the right
// tool with its full schema via the regular tools/list output.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/search"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	toolSearchDefaultLimit = 8
	toolSearchMaxLimit     = 50
)

// ToolSearchResult is the JSON envelope returned to the agent.
type ToolSearchResult struct {
	BaseResult
	Query        string       `json:"query"`
	Results      []search.Hit `json:"results"`
	TotalIndexed int          `json:"total_indexed"`
	TypeFilter   string       `json:"type_filter,omitempty"`
}

// RegisterToolSearch adds the ToolSearch tool to the given MCP server,
// closing over the index built at startup.
func RegisterToolSearch(s *server.MCPServer, idx *search.Index) {
	tool := mcp.NewTool(
		"ToolSearch",
		mcp.WithDescription(
			"Find the right clawtool tool by natural-language intent. Use "+
				"FIRST when no eager-loaded tool clearly matches the task — "+
				"clawtool exposes 50+ tools and ToolSearch returns the "+
				"top-ranked ones for queries like \"commit with rules check\", "+
				"\"fetch a JS-rendered page\", \"run a shell command in the "+
				"background\", or \"semantic code search\". Returns ranked "+
				"candidates (name, score, description, type, instance) so the "+
				"agent can bind to the precise tool without holding every "+
				"schema in context. Engine: bleve BM25 with name^3 / "+
				"keywords^2 field boosts. NOT for running the tool — call "+
				"the matched tool directly afterward; NOT for searching code "+
				"or files — use Grep / SemanticSearch / Glob.",
		),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("Natural-language description of what you want to do.")),
		mcp.WithNumber("limit",
			mcp.Description("Max results. Default 8, hard cap 50.")),
		mcp.WithString("type",
			mcp.Description("Filter results by tool type: 'core', 'sourced', or 'any' (default).")),
	)

	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: query"), nil
		}
		limit := int(req.GetFloat("limit", float64(toolSearchDefaultLimit)))
		if limit <= 0 {
			limit = toolSearchDefaultLimit
		}
		if limit > toolSearchMaxLimit {
			limit = toolSearchMaxLimit
		}
		typeFilter := req.GetString("type", "")

		start := time.Now()
		hits, err := idx.Search(query, limit, typeFilter)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := ToolSearchResult{
			BaseResult: BaseResult{
				Operation:  "ToolSearch",
				Engine:     "bleve-bm25",
				DurationMs: time.Since(start).Milliseconds(),
			},
			Query:        query,
			Results:      hits,
			TotalIndexed: idx.Total(),
			TypeFilter:   typeFilter,
		}
		return resultOf(out), nil
	})
}

// Render satisfies the Renderer contract. Each hit is rendered as
// "[score] name (type) — description" so the agent and the user
// see a plausible top-N list rather than raw JSON.
func (r ToolSearchResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Query)
	}
	var b strings.Builder
	header := fmt.Sprintf("query %q", r.Query)
	if r.TypeFilter != "" {
		header += fmt.Sprintf(" · type=%s", r.TypeFilter)
	}
	b.WriteString(r.HeaderLine(header))
	b.WriteByte('\n')
	if len(r.Results) == 0 {
		b.WriteString("(no matches)\n")
	} else {
		for _, h := range r.Results {
			fmt.Fprintf(&b, "  %.2f  %s (%s)\n", h.Score, h.Name, h.Type)
			if h.Description != "" {
				fmt.Fprintf(&b, "        %s\n", h.Description)
			}
		}
	}
	extras := []string{
		fmt.Sprintf("%d/%d", len(r.Results), r.TotalIndexed),
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// CoreToolDocs returns search.Doc descriptors for every clawtool
// core tool. Step 4 of #173 collapsed the duplicated entry list
// into a delegate over BuildManifest().SearchDocs(nil) so the
// manifest is now the single source of truth. Kept as a public
// shim so the surface_drift_test (which iterates by spec name)
// stays a one-liner; internal callers go to the manifest
// directly.
func CoreToolDocs() []search.Doc {
	return BuildManifest().SearchDocs(nil)
}
