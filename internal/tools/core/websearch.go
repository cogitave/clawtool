// Package core — WebSearch is a pluggable web-search primitive. clawtool
// itself does no crawling or ranking; it adapts whichever search backend
// the user has configured (Brave today; Tavily / SearXNG planned) into a
// uniform `{results, backend, …}` shape.
//
// Per ADR-007 we wrap, never reinvent. The backend interface is small on
// purpose so adding a new provider is one file (see websearch_brave.go
// for the canonical example).
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	webSearchDefaultLimit = 5
	webSearchMaxLimit     = 20
	webSearchTimeoutMs    = 15_000
)

// WebSearchResult is the uniform result envelope the agent receives.
// Backend lives in BaseResult.Engine because the engine concept is the
// same — which backend ran this query — and consolidating in the
// embedded struct keeps every tool's "who did the work" field in one
// place across the catalog.
type WebSearchResult struct {
	BaseResult
	Query        string         `json:"query"`
	Results      []WebSearchHit `json:"results"`
	ResultsCount int            `json:"results_count"`
	Truncated    bool           `json:"truncated"`
}

// WebSearchHit is one ranked result from any backend.
type WebSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// Backend abstracts a web-search provider. Implementations must be safe
// to invoke from multiple goroutines and complete within the supplied
// context's deadline.
type Backend interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]WebSearchHit, error)
}

// websearchHTTPClient is package-level so tests can inject a transport.
var websearchHTTPClient = &http.Client{Timeout: webSearchTimeoutMs * time.Millisecond}

// resolveBackend picks the configured backend and returns it ready to
// use. The selection lives in the secrets store under scope "websearch"
// (key `backend`) or, for first-run convenience, falls back to the
// CLAWTOOL_WEBSEARCH_BACKEND env var. Default is brave because it has
// the most lenient free-tier policy among supported providers.
//
// Each backend reads its API key from the same secrets store under
// scope "websearch" (or scope "global"); see backend implementation.
func resolveBackend(store *secrets.Store) (Backend, error) {
	name, _ := store.Get("websearch", "backend")
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(os.Getenv("CLAWTOOL_WEBSEARCH_BACKEND")))
	}
	if name == "" {
		name = "brave"
	}
	switch name {
	case "brave":
		return newBraveBackend(store)
	default:
		return nil, fmt.Errorf("unknown websearch backend %q", name)
	}
}

// RegisterWebSearch adds the WebSearch tool to the given MCP server. The
// secrets-store reference is captured so per-call backend resolution can
// pick up updated API keys without restart.
func RegisterWebSearch(s *server.MCPServer, store *secrets.Store) {
	tool := mcp.NewTool(
		"WebSearch",
		mcp.WithDescription(
			"Run a web search via the configured backend (default: Brave). "+
				"Returns ranked {title, url, snippet} hits. Backend selection "+
				"is read from secrets[scope=websearch].backend; API key from "+
				"the same scope. Brave: BRAVE_API_KEY; obtain at "+
				"https://api.search.brave.com/app/keys.",
		),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("The search query.")),
		mcp.WithNumber("limit",
			mcp.Description("Number of results to return. Default 5, max 20.")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("missing required argument: query"), nil
		}
		limit := int(req.GetFloat("limit", float64(webSearchDefaultLimit)))
		if limit <= 0 {
			limit = webSearchDefaultLimit
		}
		if limit > webSearchMaxLimit {
			limit = webSearchMaxLimit
		}

		out := WebSearchResult{
			BaseResult: BaseResult{Operation: "WebSearch"},
			Query:      query,
		}
		start := time.Now()
		backend, err := resolveBackend(store)
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		out.Engine = backend.Name()

		searchCtx, cancel := context.WithTimeout(ctx, webSearchTimeoutMs*time.Millisecond)
		defer cancel()
		hits, err := backend.Search(searchCtx, query, limit)
		if err != nil {
			out.ErrorReason = err.Error()
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		if len(hits) > limit {
			hits = hits[:limit]
			out.Truncated = true
		}
		out.Results = hits
		out.ResultsCount = len(hits)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	})
}

// Render satisfies the Renderer contract. Header carries query +
// backend; body lists `[N] title — url` rows so a developer scans
// results the same way they would in a browser results page.
func (r WebSearchResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Query)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("search %q", r.Query)))
	b.WriteByte('\n')
	if len(r.Results) == 0 {
		b.WriteString("(no results)\n")
	} else {
		for i, h := range r.Results {
			fmt.Fprintf(&b, "[%d] %s\n    %s\n", i+1, h.Title, h.URL)
			if h.Snippet != "" {
				fmt.Fprintf(&b, "    %s\n", h.Snippet)
			}
		}
	}
	extras := []string{fmt.Sprintf("%d result(s)", r.ResultsCount)}
	if r.Truncated {
		extras = append(extras, "truncated")
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// ErrMissingAPIKey is returned by backends when their required API key
// is not present in either the secrets store or process env.
var ErrMissingAPIKey = errors.New("missing API key")
