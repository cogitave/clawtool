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

	"github.com/cogitave/clawtool/internal/config"
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
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	sec, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}

	mgr := sources.NewManager(cfg, sec)
	if err := mgr.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "clawtool: some sources failed to start: %v\n", err)
	}
	defer mgr.Stop()

	// Build the search-index descriptors before any registration so the
	// final corpus reflects what we're actually about to serve.
	docs := buildIndexDocs(cfg, mgr)
	idx, err := search.Build(docs)
	if err != nil {
		return fmt.Errorf("build search index: %w", err)
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

	if err := server.ServeStdio(s); err != nil {
		return fmt.Errorf("stdio serve: %w", err)
	}
	return nil
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
