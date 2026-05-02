// Package core — Mcp* MCP tools (ADR-019). v0.17 fills in
// `McpNew` (real generator wrapper), `McpList` (real walker),
// and keeps thin stubs for `McpRun` / `McpBuild` / `McpInstall`
// that point at the CLI shortcut (those are inherently
// filesystem-side operations the model doesn't usually drive).
package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cogitave/clawtool/internal/catalog"
	"github.com/cogitave/clawtool/internal/mcpgen"
)

type mcpListResult struct {
	BaseResult
	Projects []mcpListEntry `json:"projects"`
	Root     string         `json:"root"`
}

type mcpListEntry struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Path     string `json:"path"`
}

func (r mcpListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	if len(r.Projects) == 0 {
		fmt.Fprintf(&b, "(no MCP server projects under %s — `clawtool mcp new <name>` to scaffold one)\n", r.Root)
	} else {
		fmt.Fprintf(&b, "%d project(s) under %s\n\n", len(r.Projects), r.Root)
		fmt.Fprintf(&b, "  %-32s %-12s %s\n", "PROJECT", "LANGUAGE", "PATH")
		for _, p := range r.Projects {
			fmt.Fprintf(&b, "  %-32s %-12s %s\n", p.Name, p.Language, p.Path)
		}
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type mcpNewResult struct {
	BaseResult
	Project string `json:"project"`
	Path    string `json:"path"`
}

func (r mcpNewResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Project)
	}
	return r.SuccessLine(fmt.Sprintf("scaffolded %s at %s", r.Project, r.Path))
}

type mcpDeferredResult struct {
	BaseResult
	Verb string `json:"verb"`
}

func (r mcpDeferredResult) Render() string { return r.ErrorLine("Mcp" + r.Verb) }

// RegisterMcpTools wires the Mcp* surface (ADR-019). McpNew runs
// the real generator. McpList walks the on-disk markers. McpRun /
// McpBuild / McpInstall are CLI-side filesystem operations and
// surface a hint to use the shell command — that's the natural
// path for a model giving advice rather than driving the build.
func RegisterMcpTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"McpList",
			mcp.WithDescription(
				"Discover MCP server projects scaffolded under a directory tree. "+
					"Use when the operator asks \"what MCP servers do I have here?\" "+
					"or before McpRun / McpBuild to find the project path. Walks "+
					"from `root` (default cwd), detecting projects via the "+
					"`.clawtool/mcp.toml` marker that `McpNew` / `clawtool mcp new` "+
					"writes; reports name, language (go/python/typescript), and "+
					"absolute path. NOT for listing already-installed MCP servers "+
					"a host runs — those live in the host's own config (e.g. "+
					"~/.claude.json). Read-only.",
			),
			mcp.WithString("root",
				mcp.Description("Search root path. Defaults to the server's cwd.")),
		),
		runMcpList,
	)

	s.AddTool(
		mcp.NewTool(
			"McpNew",
			mcp.WithDescription(
				"Scaffold a new MCP server project. Each language wraps the "+
					"canonical SDK: Go via mark3labs/mcp-go, Python via fastmcp, "+
					"TypeScript via @modelcontextprotocol/sdk. Result lives at "+
					"<output>/<name>/. .claude-plugin/ is opt-in via the plugin "+
					"flag. Tool definitions ship a single starter — the agent "+
					"edits the generated source to add more. Use --from-source "+
					"<name> to fork an existing catalog entry as the starting "+
					"point.",
			),
			mcp.WithString("name", mcp.Required(),
				mcp.Description("Project name. kebab-case [a-z0-9][a-z0-9-]{1,63}.")),
			mcp.WithString("description",
				mcp.Description("One-sentence server self-description. Required unless `from_source` is set, in which case the catalog entry's description seeds the default.")),
			mcp.WithString("language", mcp.Required(),
				mcp.Description("go | python | typescript")),
			mcp.WithString("transport",
				mcp.Description("stdio (default) | streamable-http")),
			mcp.WithString("packaging",
				mcp.Description("native (default) | docker")),
			mcp.WithString("tool_name",
				mcp.Description("Snake_case name of the first tool. Defaults to echo_back.")),
			mcp.WithString("tool_description",
				mcp.Description("First tool's description. Defaults to a placeholder, or to the catalog entry's description when `from_source` is set.")),
			mcp.WithString("output",
				mcp.Description("Parent directory for the project folder. Defaults to the server's cwd.")),
			mcp.WithBoolean("plugin",
				mcp.Description("Generate .claude-plugin/ manifest files (default true).")),
			mcp.WithString("from_source",
				mcp.Description("Fork the wizard's defaults from a built-in catalog entry (e.g. \"github\"). Pre-fills `description` + `tool_description` from the entry; explicit args still override.")),
		),
		runMcpNew,
	)

	for _, verb := range []string{"Run", "Build", "Install"} {
		boundVerb := verb
		hint := fmt.Sprintf(
			"clawtool MCP scaffolder — %s verb. This operation runs in the "+
				"operator's shell because it touches the filesystem + language "+
				"toolchain (make / npm / pip / docker). Use `clawtool mcp %s "+
				"<path>` instead. Calling this MCP tool surfaces the same hint.",
			strings.ToLower(verb), strings.ToLower(verb))
		s.AddTool(
			mcp.NewTool(
				"Mcp"+verb,
				mcp.WithDescription(hint),
			),
			func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				out := mcpDeferredResult{
					BaseResult: BaseResult{Operation: "Mcp" + boundVerb, Engine: "mcpgen"},
					Verb:       boundVerb,
				}
				out.ErrorReason = fmt.Sprintf(
					"Mcp%s runs in the shell — invoke `clawtool mcp %s <path>` instead.",
					boundVerb, strings.ToLower(boundVerb))
				return resultOf(out), nil
			},
		)
	}
}

func runMcpList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root := strings.TrimSpace(req.GetString("root", "."))
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	out := mcpListResult{
		BaseResult: BaseResult{Operation: "McpList", Engine: "mcpgen"},
		Root:       abs,
	}
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	projects, err := walkMcpProjectsForTool(abs)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Projects = projects
	return resultOf(out), nil
}

func runMcpNew(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: name"), nil
	}
	language, err := req.RequireString("language")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: language"), nil
	}
	out := mcpNewResult{
		BaseResult: BaseResult{Operation: "McpNew", Engine: "mcpgen"},
		Project:    name,
	}

	// from_source: pre-fill description / first-tool description from
	// the named built-in catalog entry. Explicit `description` and
	// `tool_description` args still override.
	fromSource := strings.TrimSpace(req.GetString("from_source", ""))
	var sourceEntry *catalog.Entry
	if fromSource != "" {
		cat, cerr := catalog.Builtin()
		if cerr != nil {
			out.ErrorReason = fmt.Sprintf("from_source: catalog: %v", cerr)
			return resultOf(out), nil
		}
		entry, ok := cat.Lookup(fromSource)
		if !ok {
			out.ErrorReason = fmt.Sprintf("from_source: %q is not in the built-in catalog (run `clawtool source list --catalog` to see available entries)", fromSource)
			return resultOf(out), nil
		}
		sourceEntry = &entry
	}

	description := strings.TrimSpace(req.GetString("description", ""))
	if description == "" {
		if sourceEntry != nil {
			description = sourceEntry.Description
		} else {
			return mcp.NewToolResultError("missing required argument: description"), nil
		}
	}

	output := strings.TrimSpace(req.GetString("output", ""))
	if output == "" {
		cwd, _ := os.Getwd()
		output = cwd
	}
	toolName := strings.TrimSpace(req.GetString("tool_name", "echo_back"))
	if toolName == "" {
		toolName = "echo_back"
	}
	defaultToolDesc := "Return the input string verbatim. Replace with your real tool."
	if sourceEntry != nil {
		defaultToolDesc = sourceEntry.Description
	}
	toolDescription := strings.TrimSpace(req.GetString("tool_description", defaultToolDesc))
	if toolDescription == "" {
		toolDescription = defaultToolDesc
	}
	spec := mcpgen.Spec{
		Name:        name,
		Description: description,
		Language:    language,
		Transport:   strings.TrimSpace(req.GetString("transport", "stdio")),
		Packaging:   strings.TrimSpace(req.GetString("packaging", "native")),
		Plugin:      req.GetBool("plugin", true),
		Tools: []mcpgen.ToolSpec{{
			Name:        toolName,
			Description: toolDescription,
			Schema:      `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`,
		}},
	}
	root, err := mcpgen.Generate(output, spec)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Path = root
	return resultOf(out), nil
}

// walkMcpProjectsForTool mirrors internal/cli/mcp.go's walkForMcpProjects
// but lives here so the MCP tool doesn't import internal/cli (which
// would invert the dependency direction).
func walkMcpProjectsForTool(root string) ([]mcpListEntry, error) {
	var out []mcpListEntry
	skip := map[string]bool{
		"node_modules": true, ".git": true, "vendor": true,
		"dist": true, "build": true, ".venv": true, "__pycache__": true,
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() && skip[info.Name()] {
			return filepath.SkipDir
		}
		if info.IsDir() && info.Name() == ".clawtool" {
			marker := filepath.Join(path, "mcp.toml")
			if _, err := os.Stat(marker); err == nil {
				projDir := filepath.Dir(path)
				name, language := readMcpProjectFields(marker)
				out = append(out, mcpListEntry{
					Name:     name,
					Language: language,
					Path:     projDir,
				})
			}
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}

// readMcpProjectFields cheaply pulls name + language without
// pulling the full TOML parser dep into this file. Marker files
// always have the same shape (we wrote them).
func readMcpProjectFields(marker string) (name, language string) {
	body, err := os.ReadFile(marker)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "name        ="):
			name = parseQuoted(strings.TrimPrefix(line, "name        ="))
		case strings.HasPrefix(line, "language    ="):
			language = parseQuoted(strings.TrimPrefix(line, "language    ="))
		}
		if name != "" && language != "" {
			return
		}
	}
	return
}

func parseQuoted(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
