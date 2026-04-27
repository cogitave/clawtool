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
		{
			Name:        "WebFetch",
			Description: "Retrieve a URL and return clean article text via Mozilla Readability for HTML, or raw text for text/* MIME types. Binary refused. 10 MB body cap.",
			Type:        "core",
			Keywords:    []string{"http", "https", "url", "fetch", "download", "web", "page", "article", "scrape", "readability"},
		},
		{
			Name:        "WebSearch",
			Description: "Run a web search via the configured backend (default Brave). Returns ranked {title, url, snippet}. API key in secrets[scope=websearch].",
			Type:        "core",
			Keywords:    []string{"search", "web", "google", "brave", "tavily", "duckduckgo", "results", "query", "engine"},
		},
		{
			Name:        "Edit",
			Description: "Replace a substring in an existing file. Atomic temp+rename, line-ending and BOM preserve, binary refusal. Refuses ambiguous matches unless replace_all=true.",
			Type:        "core",
			Keywords:    []string{"replace", "modify", "change", "patch", "substitute", "search-and-replace", "sed", "fix"},
		},
		{
			Name:        "Write",
			Description: "Create or replace a whole file. Atomic temp+rename, parent directory auto-create, line-ending and BOM preserve when overwriting.",
			Type:        "core",
			Keywords:    []string{"create", "save", "overwrite", "tee", "echo", "new", "file"},
		},
		{
			Name:        "SendMessage",
			Description: "Forward a prompt to another AI coding-agent CLI (claude / codex / opencode / gemini) and stream its reply. clawtool wraps each upstream's published headless mode; the bridge plugin must be installed first via BridgeAdd.",
			Type:        "core",
			Keywords:    []string{"dispatch", "delegate", "forward", "prompt", "agent", "claude", "codex", "opencode", "gemini", "relay", "ask", "ai"},
		},
		{
			Name:        "AgentList",
			Description: "Snapshot of the supervisor's agent registry — every configured instance with family, bridge, callable status, and auth scope.",
			Type:        "core",
			Keywords:    []string{"list", "agents", "instances", "registry", "available", "callable"},
		},
		{
			Name:        "BridgeList",
			Description: "List installable bridges to other coding-agent CLIs (codex, opencode, gemini) with current install state.",
			Type:        "core",
			Keywords:    []string{"bridges", "plugins", "install", "available", "codex", "opencode", "gemini", "list"},
		},
		{
			Name:        "BridgeAdd",
			Description: "Install the canonical bridge for a family (codex / opencode / gemini). Wraps the upstream's Claude Code plugin or built-in subcommand. Idempotent.",
			Type:        "core",
			Keywords:    []string{"install", "bridge", "plugin", "add", "codex", "opencode", "gemini", "setup"},
		},
		{
			Name:        "BridgeRemove",
			Description: "Remove the bridge for a family. v0.10 ships as a manual hint; full uninstall lands in v0.10.x.",
			Type:        "core",
			Keywords:    []string{"uninstall", "remove", "bridge", "plugin"},
		},
		{
			Name:        "BridgeUpgrade",
			Description: "Re-run the bridge install (idempotent; pulls the latest plugin version).",
			Type:        "core",
			Keywords:    []string{"upgrade", "update", "bridge", "plugin", "refresh"},
		},
		{
			Name:        "Verify",
			Description: "Run a repo's tests / lints / typechecks via whichever runner it declares (Make / pnpm / npm / go / pytest / ruby / cargo / just). Returns one structured pass/fail per check. Buffered single payload — for streaming output use Bash.",
			Type:        "core",
			Keywords:    []string{"verify", "test", "tests", "check", "ci", "make", "pnpm", "npm", "go-test", "pytest", "cargo", "just", "validate"},
		},
		{
			Name:        "SemanticSearch",
			Description: "Semantic (intent-based) code search. Use for conceptual queries like 'where do we rotate auth tokens?' or 'how is caching wired?' — Grep stays the literal-regex tool. Wraps chromem-go + an embedding provider; index is built lazily on first call.",
			Type:        "core",
			Keywords:    []string{"semantic", "embeddings", "vector", "concept", "intent", "find-code", "rag", "search-code", "discover", "where"},
		},
		{
			Name:        "TaskGet",
			Description: "Snapshot of one BIAM task: status + every message persisted under task_id. Pair with SendMessage --bidi to dispatch async and poll without blocking.",
			Type:        "core",
			Keywords:    []string{"task", "biam", "async", "poll", "result", "snapshot"},
		},
		{
			Name:        "TaskWait",
			Description: "Block until a BIAM task reaches a terminal state. Use when the caller has nothing else to do until the upstream finishes.",
			Type:        "core",
			Keywords:    []string{"task", "biam", "wait", "block", "result", "terminal"},
		},
		{
			Name:        "TaskList",
			Description: "Recent BIAM tasks (default 50). Use to find task_ids when the caller forgot one mid-conversation.",
			Type:        "core",
			Keywords:    []string{"task", "biam", "list", "recent", "history"},
		},
		{
			Name:        "BrowserFetch",
			Description: "Render a URL inside a real headless browser (Obscura, V8 + Chrome DevTools Protocol) and return clean prose for HTML or the value of a custom JS expression. Use when WebFetch returns empty SPA shells (Next.js / React / hydrated pages). Stateless per call; for cookies + multi-step interaction use BrowserAction.",
			Type:        "core",
			Keywords:    []string{"browser", "headless", "spa", "javascript", "render", "obscura", "puppeteer", "playwright", "fetch", "scrape", "react", "next", "hydrated", "cdp"},
		},
		{
			Name:        "BrowserScrape",
			Description: "Render many URLs in parallel through a real browser engine (Obscura) and capture a JS expression's value per page. Bulk SPA scraping with configurable concurrency. Stateless per URL.",
			Type:        "core",
			Keywords:    []string{"browser", "headless", "scrape", "bulk", "parallel", "spa", "obscura", "crawler", "harvest"},
		},
		{
			Name:        "PortalList",
			Description: "List configured web-UI portals (saved authenticated browser targets — ADR-018). A portal pairs a base URL with login cookies, selectors, and a 'response done' predicate so PortalAsk can drive the page through Obscura.",
			Type:        "core",
			Keywords:    []string{"portal", "portals", "list", "browser", "target", "saved", "config", "registry"},
		},
		{
			Name:        "PortalWhich",
			Description: "Resolve the sticky-default portal — env > sticky file > single-configured fallback.",
			Type:        "core",
			Keywords:    []string{"portal", "which", "default", "sticky"},
		},
		{
			Name:        "PortalUse",
			Description: "Set the sticky-default portal so PortalAsk calls without an explicit name route here.",
			Type:        "core",
			Keywords:    []string{"portal", "use", "sticky", "default", "set"},
		},
		{
			Name:        "PortalUnset",
			Description: "Clear the sticky-default portal.",
			Type:        "core",
			Keywords:    []string{"portal", "unset", "clear", "sticky"},
		},
		{
			Name:        "PortalRemove",
			Description: "Remove a portal stanza from config.toml. Cookies under [scopes.\"portal.<name>\"] in secrets.toml stay in place; clean manually if no longer needed.",
			Type:        "core",
			Keywords:    []string{"portal", "remove", "delete", "config"},
		},
		{
			Name:        "PortalAsk",
			Description: "Drive a saved portal (ADR-018) with the given prompt and return the rendered response. Spawns Obscura's CDP server, seeds cookies + extra headers, navigates to start_url, runs login_check + ready_predicate, fills the input selector, clicks submit (or dispatches Enter), polls response_done_predicate, and extracts the last response selector's innerText.",
			Type:        "core",
			Keywords:    []string{"portal", "ask", "browser", "chat", "deepseek", "perplexity", "phind", "send", "drive", "automate", "cdp"},
		},
		{
			Name:        "McpList",
			Description: "List MCP server projects under a root path (default cwd). Detects via the .clawtool/mcp.toml marker the v0.17 generator writes. ADR-019. Sister of `clawtool skill list` for MCP authoring.",
			Type:        "core",
			Keywords:    []string{"mcp", "scaffold", "author", "list", "projects", "server", "build"},
		},
		{
			Name:        "McpNew",
			Description: "Scaffold a new MCP server project (Go via mcp-go, Python via FastMCP, TypeScript via @modelcontextprotocol/sdk). Wizard asks for description / language / transport / packaging / tools. ADR-019. Generator lands in v0.17 — today returns a deferred-feature error.",
			Type:        "core",
			Keywords:    []string{"mcp", "scaffold", "new", "create", "generate", "author", "go", "python", "typescript"},
		},
		{
			Name:        "McpRun",
			Description: "Run an MCP server project in dev mode (stdio). ADR-019. Lands v0.17.",
			Type:        "core",
			Keywords:    []string{"mcp", "run", "dev", "stdio"},
		},
		{
			Name:        "McpBuild",
			Description: "Build / package an MCP server project (binary, npm, pypi, or Docker image). ADR-019. Lands v0.17.",
			Type:        "core",
			Keywords:    []string{"mcp", "build", "compile", "package", "docker"},
		},
		{
			Name:        "McpInstall",
			Description: "Build + register a local MCP server project as [sources.<instance>] in config.toml — same surface as `clawtool source add` but auto-discovers the launch command from the project's `.clawtool/mcp.toml`. ADR-019. Lands v0.17.",
			Type:        "core",
			Keywords:    []string{"mcp", "install", "register", "source", "local"},
		},
	}
}
