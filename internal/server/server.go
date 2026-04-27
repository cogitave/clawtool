// Package server starts the clawtool MCP server.
//
// Per ADR-004, clawtool exposes itself as one MCP server over stdio.
// Per ADR-006, core tools use PascalCase names (Bash, Read, Edit, ...).
// Per ADR-008, configured sources spawn as child MCP servers and their
// tools are aggregated under `<instance>__<tool>` wire names.
//
// Boot order on every `clawtool serve`:
//  1. Load config + secrets.
//  2. Build sources.Manager and start each configured source. Failures on
//     individual sources are non-fatal; their tools just don't show up.
//  3. Build a search.Index from descriptors of every tool we plan to
//     register: enabled core tools + ToolSearch + aggregated source tools.
//     This index powers the ToolSearch primitive — see ADR-005 for why
//     search-first is the prerequisite that lets a 50+ tool catalog scale.
//  4. Register all tools on the parent MCP server. ToolSearch closes over
//     the index reference; aggregated source-tool handlers route via the
//     manager.
package server

import (
	"context"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/observability"
	"github.com/cogitave/clawtool/internal/search"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/sources"
	"github.com/cogitave/clawtool/internal/tools/core"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/mark3labs/mcp-go/server"

	// Pull every recipe subpackage's init() so the setup registry
	// is populated before RegisterRecipeTools wires the MCP surface.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// ServeStdio runs clawtool as an MCP server speaking over stdio. It blocks
// until stdin closes (the conventional MCP shutdown signal) or an
// unrecoverable error occurs.
func ServeStdio(ctx context.Context) error {
	s, mgr, _, _, err := buildMCPServer(ctx)
	if err != nil {
		return err
	}
	defer mgr.Stop()
	if err := server.ServeStdio(s); err != nil {
		return fmt.Errorf("stdio serve: %w", err)
	}
	return nil
}

// buildMCPServer wires the full MCP server (config, secrets, sources,
// search index, every tool registration). Returned to the caller so a
// transport other than stdio (e.g. the Phase 2 HTTP gateway) can run
// the same server. The Manager is returned alongside so callers can
// Stop() it on shutdown.
func buildMCPServer(ctx context.Context) (*server.MCPServer, *sources.Manager, config.Config, *secrets.Store, error) {
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return nil, nil, config.Config{}, nil, fmt.Errorf("load config: %w", err)
	}
	sec, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return nil, nil, cfg, nil, fmt.Errorf("load secrets: %w", err)
	}

	// Observability — wires OTLP/HTTP exporter and registers the
	// process-wide observer agents.NewSupervisor picks up
	// automatically. Disabled-by-default: zero overhead when off.
	// Init failures are logged but non-fatal — clawtool keeps serving.
	obs := observability.New()
	if err := obs.Init(ctx, cfg.Observability); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: observability init failed (continuing without traces): %v\n", err)
	} else if cfg.Observability.Enabled {
		agents.SetGlobalObserver(obs)
		fmt.Fprintf(os.Stderr, "clawtool: observability enabled (exporter=%s)\n", cfg.Observability.ExporterURL)
	}

	// Auto-lint guardrails (ADR-014 T2). Default = on; explicit
	// AutoLint.Enabled = false flips the package-level flag in
	// internal/tools/core. The Runner detects the linter binary
	// per-call so missing tools (e.g. ruff on a Go-only repo) are a
	// silent skip, not an error.
	if cfg.AutoLint.Enabled != nil {
		core.SetAutoLintEnabled(*cfg.AutoLint.Enabled)
	}

	mgr := sources.NewManager(cfg, sec)
	if err := mgr.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: some sources failed to start: %v\n", err)
	}

	// Build the search-index descriptors before any registration so the
	// final corpus reflects what we're actually about to serve.
	docs := buildIndexDocs(cfg, mgr)
	idx, err := search.Build(docs)
	if err != nil {
		mgr.Stop()
		return nil, nil, cfg, sec, fmt.Errorf("build search index: %w", err)
	}

	s := server.NewMCPServer(
		version.Name,
		version.Version,
		server.WithToolCapabilities(true),
		server.WithLogging(),
	)

	// Core tools, filtered by config.IsEnabled. ADR-005 / ADR-006: agents
	// can disable any core tool and use the agent's native one instead.
	if cfg.IsEnabled("Bash").Enabled {
		core.RegisterBash(s)
	}
	if cfg.IsEnabled("Grep").Enabled {
		core.RegisterGrep(s)
	}
	if cfg.IsEnabled("Read").Enabled {
		core.RegisterRead(s)
	}
	if cfg.IsEnabled("Glob").Enabled {
		core.RegisterGlob(s)
	}
	if cfg.IsEnabled("ToolSearch").Enabled {
		core.RegisterToolSearch(s, idx)
	}
	if cfg.IsEnabled("WebFetch").Enabled {
		core.RegisterWebFetch(s)
	}
	if cfg.IsEnabled("WebSearch").Enabled {
		core.RegisterWebSearch(s, sec)
	}
	if cfg.IsEnabled("Edit").Enabled {
		core.RegisterEdit(s)
	}
	if cfg.IsEnabled("Write").Enabled {
		core.RegisterWrite(s)
	}

	// Recipe* tools mirror `clawtool recipe …` so a model can list,
	// detect, and apply project-setup recipes from inside a chat.
	// Always registered — there's no per-tool gate for the recipe
	// surface yet (cfg.IsEnabled is core-tool scoped). Adding one is
	// trivial when the need shows up.
	core.RegisterRecipeTools(s)

	// Bridge* tools mirror `clawtool bridge add/list/remove/upgrade`
	// so a model can install / inspect bridges to other coding-agent
	// CLIs (codex / opencode / gemini) mid-conversation. Per ADR-014.
	core.RegisterBridgeTools(s)

	// SendMessage + AgentList expose the supervisor's dispatch +
	// registry surface over MCP. Same call site as `clawtool send`
	// CLI and the future HTTP gateway.
	core.RegisterAgentTools(s)

	// Verify runs a repo's tests/lints/typechecks via whichever
	// runner the repo declares (Make/pnpm/npm/go/pytest/ruby/cargo/
	// just) and returns one structured pass/fail per check. ADR-014
	// T4. Always registered.
	core.RegisterVerify(s)

	// SemanticSearch wraps chromem-go for intent-based code search;
	// the embedding index is built lazily on first call. Registered
	// always — missing OPENAI_API_KEY (or absent Ollama daemon) is
	// surfaced as a per-call error, not a boot failure.
	core.RegisterSemanticSearch(s)

	// SkillNew lets a model scaffold an agentskills.io-standard
	// skill from inside a conversation. Same template the
	// `clawtool skill new` CLI emits — both go through the
	// internal/skillgen package.
	core.RegisterSkillNew(s)

	// Aggregated source tools — one entry per (running instance × tool),
	// already named in wire form `<instance>__<tool>`.
	for _, st := range mgr.AggregatedTools() {
		s.AddTool(st.Tool, st.Handler)
	}
	return s, mgr, cfg, sec, nil
}

// buildIndexDocs assembles search descriptors from every tool clawtool will
// register. Disabled core tools are excluded from the index too — an agent
// shouldn't discover a tool it can't call.
func buildIndexDocs(cfg config.Config, mgr *sources.Manager) []search.Doc {
	var docs []search.Doc

	enabled := map[string]bool{
		"Bash":       cfg.IsEnabled("Bash").Enabled,
		"Edit":       cfg.IsEnabled("Edit").Enabled,
		"Glob":       cfg.IsEnabled("Glob").Enabled,
		"Grep":       cfg.IsEnabled("Grep").Enabled,
		"Read":       cfg.IsEnabled("Read").Enabled,
		"ToolSearch": cfg.IsEnabled("ToolSearch").Enabled,
		"WebFetch":   cfg.IsEnabled("WebFetch").Enabled,
		"WebSearch":  cfg.IsEnabled("WebSearch").Enabled,
		"Write":      cfg.IsEnabled("Write").Enabled,
	}
	for _, d := range core.CoreToolDocs() {
		if enabled[d.Name] {
			docs = append(docs, d)
		}
	}

	// Aggregated source tools. We index name + description from the child's
	// own MCP advertisement — that's the canonical source of truth.
	for _, st := range mgr.AggregatedTools() {
		instance, _, _ := sources.SplitWireName(st.Tool.Name)
		docs = append(docs, search.Doc{
			Name:        st.Tool.Name,
			Description: st.Tool.Description,
			Type:        "sourced",
			Instance:    instance,
		})
	}
	return docs
}
