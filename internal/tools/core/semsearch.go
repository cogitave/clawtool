// Package core — SemanticSearch MCP tool (ADR-014 T6, design from
// the 2026-04-26 multi-CLI fan-out).
//
// Concept queries ("how is auth rotated?") that Grep can't reach
// because the literal token isn't there. We wrap chromem-go's
// in-memory vector store + the configured embedding provider
// (OpenAI default, Ollama override). One Store per repo, lazily
// built on first Search call so cold-boot doesn't pay the embedding
// cost when the tool isn't being used.
//
// Coexistence with Grep: Grep stays the literal regex tool; this is
// the conceptual one. Tool descriptions carry the routing hint so
// ToolSearch ranks each correctly per query.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/index"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// SemanticSearchResult is the MCP response shape.
type SemanticSearchResult struct {
	BaseResult
	Repo    string         `json:"repo"`
	Query   string         `json:"query"`
	Results []index.Result `json:"results"`
}

// Render satisfies Renderer. One result per line in the human form,
// score in parentheses. Path:lines: snippet first 80 chars.
func (r SemanticSearchResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Repo)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("semsearch %q in %s", r.Query, r.Repo)))
	b.WriteByte('\n')
	if len(r.Results) == 0 {
		b.WriteString("(no matches)\n")
	} else {
		for _, h := range r.Results {
			snippet := strings.ReplaceAll(h.Snippet, "\n", " ⏎ ")
			if len(snippet) > 120 {
				snippet = snippet[:120] + "…"
			}
			fmt.Fprintf(&b, "%s:%d-%d (%.3f) %s\n", h.Path, h.LineStart, h.LineEnd, h.Score, snippet)
		}
	}
	b.WriteString(r.FooterLine(fmt.Sprintf("%d match(es)", len(r.Results))))
	return b.String()
}

// storeCache holds at most one *index.Store per repo path. We
// rebuild lazily when the store is missing; persisting + invalidation
// land in v0.14.x. Mutex guards concurrent first-Build attempts.
var (
	semStoreMu sync.Mutex
	semStores  = map[string]*index.Store{}
)

// RegisterSemanticSearch wires the tool. Always registered; missing
// embedding key surfaces as a per-call error, not a boot failure.
func RegisterSemanticSearch(s *server.MCPServer) {
	tool := mcp.NewTool(
		"SemanticSearch",
		mcp.WithDescription(
			"Semantic (intent-based) code search across a repo. Use for "+
				"conceptual queries like \"how is auth rotated?\" or "+
				"\"where do we cache embeddings?\" — Grep stays the "+
				"literal-regex tool. Wraps chromem-go (MIT) for the vector "+
				"store; embedding via OpenAI text-embedding-3-small (default; "+
				"requires OPENAI_API_KEY) or Ollama nomic-embed-text "+
				"(override via CLAWTOOL_EMBED_PROVIDER=ollama). The index "+
				"is built lazily on the first call per repo.",
		),
		mcp.WithString("repo", mcp.Required(),
			mcp.Description("Repo path to search.")),
		mcp.WithString("query", mcp.Required(),
			mcp.Description("Natural-language description of what to find.")),
		mcp.WithNumber("limit",
			mcp.Description("Max number of hits to return. Default 10.")),
	)
	s.AddTool(tool, runSemanticSearch)
}

func runSemanticSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: repo"), nil
	}
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: query"), nil
	}
	limit := int(req.GetFloat("limit", 10))
	if limit <= 0 {
		limit = 10
	}

	start := time.Now()
	out := SemanticSearchResult{
		BaseResult: BaseResult{Operation: "SemanticSearch", Engine: "chromem-go"},
		Repo:       repo,
		Query:      query,
	}

	store, err := getOrBuildStore(ctx, repo)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	results, err := store.Search(ctx, query, limit)
	if err != nil {
		out.ErrorReason = err.Error()
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Results = results
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

func getOrBuildStore(ctx context.Context, repo string) (*index.Store, error) {
	semStoreMu.Lock()
	defer semStoreMu.Unlock()
	if s, ok := semStores[repo]; ok && s.Count() > 0 {
		return s, nil
	}
	provider := strings.TrimSpace(os.Getenv("CLAWTOOL_EMBED_PROVIDER"))
	if provider == "" {
		provider = "openai"
	}
	s := index.New(repo, index.Options{Provider: provider})
	if err := s.Build(ctx); err != nil {
		return nil, fmt.Errorf("build index: %w", err)
	}
	if s.Count() == 0 {
		return nil, errors.New("index built but empty (no readable text files in repo)")
	}
	semStores[repo] = s
	return s, nil
}

// ResetSemanticSearchCache lets tests drop the cached stores. No-op
// in production.

