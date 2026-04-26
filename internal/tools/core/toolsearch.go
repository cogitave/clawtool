// Package core — ToolSearch is the discovery primitive that makes a 50+
// tool catalog usable per ADR-005. The agent calls ToolSearch with a
// natural-language query, gets ranked candidates, then binds to the right
// tool with its full schema via the regular tools/list output.
package core

import (
	"context"
	"encoding/json"
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
	Query         string        `json:"query"`
	Results       []search.Hit  `json:"results"`
	TotalIndexed  int           `json:"total_indexed"`
	TypeFilter    string        `json:"type_filter,omitempty"`
	Engine        string        `json:"engine"`
	DurationMs    int64         `json:"duration_ms"`
}

// RegisterToolSearch adds the ToolSearch tool to the given MCP server,
// closing over the index built at startup.
func RegisterToolSearch(s *server.MCPServer, idx *search.Index) {
	tool := mcp.NewTool(
		"ToolSearch",
		mcp.WithDescription(
			"Find tools by natural-language query. Returns ranked candidates "+
				"(name, score, description, type, instance) so an agent with a "+
				"large catalog can pick the right tool without holding every "+
				"schema in context. Engine: bleve BM25 with name^3 / keywords^2 "+
				"field boosts.",
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
		body, _ := json.Marshal(ToolSearchResult{
			Query:        query,
			Results:      hits,
			TotalIndexed: idx.Total(),
			TypeFilter:   typeFilter,
			Engine:       "bleve-bm25",
			DurationMs:   time.Since(start).Milliseconds(),
		})
		return mcp.NewToolResultText(string(body)), nil
	})
}

// CoreToolDocs returns search.Doc descriptors for every clawtool core tool.
// Centralised so the index-builder in server/server.go stays a one-liner
// and there's a single source of truth for what each core tool's
// description says — same string the user sees in tools/list.
func CoreToolDocs() []search.Doc {
	return []search.Doc{
		{
			Name:        "Bash",
			Description: "Run a shell command via /bin/bash. Returns structured JSON with stdout, stderr, exit_code, duration_ms, timed_out, cwd. Output preserved on timeout via process-group SIGKILL.",
			Type:        "core",
			Keywords:    []string{"shell", "execute", "run", "command", "terminal"},
		},
		{
			Name:        "Grep",
			Description: "Search file contents for a regular-expression pattern. Powered by ripgrep (rg) with .gitignore-aware traversal and --type aliases; falls back to system grep.",
			Type:        "core",
			Keywords:    []string{"search", "find", "regex", "ripgrep", "rg", "match", "pattern"},
		},
		{
			Name:        "Read",
			Description: "Read a file with stable line cursors and deterministic line counts. Format-aware: text, PDF (pdftotext), Jupyter (.ipynb), Word (.docx via pandoc), Excel (.xlsx via excelize), CSV/TSV, HTML (Mozilla Readability), and JSON/YAML/TOML/XML pass-through.",
			Type:        "core",
			Keywords:    []string{"file", "open", "cat", "view", "pdf", "docx", "word", "xlsx", "excel", "spreadsheet", "csv", "tsv", "html", "json", "yaml", "toml", "xml", "ipynb", "notebook", "office"},
		},
		{
			Name:        "Glob",
			Description: "List files matching a glob pattern (** double-star supported). Powered by github.com/bmatcuk/doublestar.",
			Type:        "core",
			Keywords:    []string{"find", "match", "files", "pattern", "wildcard", "ls", "list"},
		},
		{
			Name:        "ToolSearch",
			Description: "Find tools by natural-language query. BM25 ranking via bleve. Use this first when you have a large catalog.",
			Type:        "core",
			Keywords:    []string{"discover", "find", "search", "query", "tools"},
		},
	}
}
