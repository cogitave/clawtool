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

// detail_level enum values. Default `name_desc` preserves the
// pre-v0.22.112 result shape for callers that don't pass the arg.
const (
	detailLevelName     = "name"      // name + score only
	detailLevelNameDesc = "name_desc" // name + description + score + type + instance (default)
	detailLevelFull     = "full"      // adds input_schema for binding
)

// ToolSearchResult is the JSON envelope returned to the agent.
// Results is a slice of detail-level-specific entries; the
// concrete shape (name-only / name+desc / full schema) is
// chosen at handler time based on the operator's
// `detail_level` arg. We carry it as `[]any` rather than
// generic `map` so the on-wire JSON keeps stable field order
// per detail level.
type ToolSearchResult struct {
	BaseResult
	Query        string `json:"query"`
	Results      []any  `json:"results"`
	TotalIndexed int    `json:"total_indexed"`
	TypeFilter   string `json:"type_filter,omitempty"`
	DetailLevel  string `json:"detail_level,omitempty"`
}

// hitName / hitNameDesc / hitFull are the three concrete result
// shapes ToolSearch emits. JSON tags fix field order per shape so
// the wire output stays stable.
type hitName struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

type hitNameDesc struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`
	Description string  `json:"description"`
	Type        string  `json:"type"`
	Instance    string  `json:"instance,omitempty"`
}

type hitFull struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`
	Description string  `json:"description"`
	Type        string  `json:"type"`
	Instance    string  `json:"instance,omitempty"`
	// InputSchema mirrors the tool's `inputSchema` from
	// tools/list. Pulled from server.GetTool at handler time —
	// only `full` callers pay the cost. `any` (rather than the
	// concrete mcp.ToolInputSchema) so tools registered via
	// RawInputSchema serialize correctly too.
	InputSchema any `json:"input_schema,omitempty"`
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
				"keywords^2 field boosts. Use detail_level=name for cheap "+
				"discovery, name_desc for selection (default), full for "+
				"binding. NOT for running the tool — call the matched tool "+
				"directly afterward; NOT for searching code or files — use "+
				"Grep / SemanticSearch / Glob.",
		),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("Natural-language description of what you want to do.")),
		mcp.WithNumber("limit",
			mcp.Description("Max results. Default 8, hard cap 50.")),
		mcp.WithString("type",
			mcp.Description("Filter results by tool type: 'core', 'sourced', or 'any' (default).")),
		mcp.WithString("detail_level",
			mcp.Description("Result verbosity: 'name' (cheap discovery — name + score only), "+
				"'name_desc' (selection — name + description + score + type + instance, the default), "+
				"or 'full' (binding — adds input_schema for schema-shaped tool calls). "+
				"Match the level to the harness's next step: a code-mode loop typically "+
				"asks for stubs first, then upgrades to full only on the chosen tool.")),
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
		detailLevel := normalizeDetailLevel(req.GetString("detail_level", ""))

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
			Results:      shapeHits(s, hits, detailLevel),
			TotalIndexed: idx.Total(),
			TypeFilter:   typeFilter,
			DetailLevel:  detailLevel,
		}
		return resultOf(out), nil
	})
}

// normalizeDetailLevel maps the operator's arg onto the canonical
// enum. Empty string and unknown values fall back to the default
// (`name_desc`) so the existing wire shape is preserved for
// callers that never pass the arg.
func normalizeDetailLevel(raw string) string {
	switch raw {
	case detailLevelName, detailLevelFull:
		return raw
	default:
		return detailLevelNameDesc
	}
}

// shapeHits projects search.Hit entries onto the requested
// detail level. For `full`, looks up each hit's tool on the live
// server to attach its input_schema; tools no longer registered
// (defensive: the index could outlive a removed tool) get a
// nil schema and are still returned with the rest of the
// detail-level data.
func shapeHits(s *server.MCPServer, hits []search.Hit, level string) []any {
	out := make([]any, 0, len(hits))
	for _, h := range hits {
		switch level {
		case detailLevelName:
			out = append(out, hitName{Name: h.Name, Score: h.Score})
		case detailLevelFull:
			entry := hitFull{
				Name:        h.Name,
				Score:       h.Score,
				Description: h.Description,
				Type:        h.Type,
				Instance:    h.Instance,
			}
			if st := s.GetTool(h.Name); st != nil {
				if st.Tool.RawInputSchema != nil {
					entry.InputSchema = st.Tool.RawInputSchema
				} else {
					entry.InputSchema = st.Tool.InputSchema
				}
			}
			out = append(out, entry)
		default: // detailLevelNameDesc
			out = append(out, hitNameDesc{
				Name:        h.Name,
				Score:       h.Score,
				Description: h.Description,
				Type:        h.Type,
				Instance:    h.Instance,
			})
		}
	}
	return out
}

// Render satisfies the Renderer contract. Each hit is rendered as
// "[score] name (type) — description" so the agent and the user
// see a plausible top-N list rather than raw JSON. The renderer
// type-switches on the concrete hit shape so detail_level=name
// (which has no Type / Description) still produces a readable
// summary line.
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
		for _, raw := range r.Results {
			switch h := raw.(type) {
			case hitName:
				fmt.Fprintf(&b, "  %.2f  %s\n", h.Score, h.Name)
			case hitNameDesc:
				fmt.Fprintf(&b, "  %.2f  %s (%s)\n", h.Score, h.Name, h.Type)
				if h.Description != "" {
					fmt.Fprintf(&b, "        %s\n", h.Description)
				}
			case hitFull:
				fmt.Fprintf(&b, "  %.2f  %s (%s)\n", h.Score, h.Name, h.Type)
				if h.Description != "" {
					fmt.Fprintf(&b, "        %s\n", h.Description)
				}
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
