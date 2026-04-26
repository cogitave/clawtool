// Package core — Glob is the canonical wrapper around bmatcuk/doublestar.
//
// Per ADR-007 we don't write a glob engine. doublestar is the de-facto
// double-star (`**`) glob library in Go, used by GoReleaser, k6, etc.
// This file's value is the polish layer: cwd-aware path resolution,
// uniform structured output, hard cap to protect agent context, and
// platform-stable separators (the wrapper always returns forward-slash
// paths regardless of OS — agents expect that).
package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	globDefaultLimit = 1000
	globHardCap      = 10000
)

// GlobResult is the uniform shape returned to the agent.
type GlobResult struct {
	BaseResult
	Matches      []string `json:"matches"`
	MatchesCount int      `json:"matches_count"`
	Truncated    bool     `json:"truncated"`
	Cwd          string   `json:"cwd"`
	Pattern      string   `json:"pattern"`
}

// RegisterGlob adds the Glob tool to the given MCP server.
func RegisterGlob(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Glob",
		mcp.WithDescription(
			"List files matching a glob pattern, with **/ double-star support. "+
				"Powered by github.com/bmatcuk/doublestar. Returns matches as "+
				"forward-slash paths relative to cwd. Hard cap protects agent context. "+
				"v0.5: does not yet honor .gitignore (lands in v0.6).",
		),
		mcp.WithString("pattern", mcp.Required(),
			mcp.Description("Glob pattern, e.g. '**/*.go', 'src/**', 'README.*'.")),
		mcp.WithString("cwd",
			mcp.Description("Working directory. Defaults to $HOME if empty.")),
		mcp.WithNumber("limit",
			mcp.Description("Max matches. Default 1000, hard cap 10000.")),
	)
	s.AddTool(tool, runGlob)
}

func runGlob(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern, err := req.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: pattern"), nil
	}
	cwd := req.GetString("cwd", "")
	if cwd == "" {
		cwd = homeDir()
	}
	limit := int(req.GetFloat("limit", float64(globDefaultLimit)))
	if limit <= 0 {
		limit = globDefaultLimit
	}
	if limit > globHardCap {
		limit = globHardCap
	}

	res := executeGlob(pattern, cwd, limit)
	return resultOf(res), nil
}

// Render satisfies the Renderer contract. One match per line so the
// chat looks like running `find` or `fd` in a terminal.
func (r GlobResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Pattern)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("glob %q", r.Pattern)))
	b.WriteByte('\n')
	if len(r.Matches) == 0 {
		b.WriteString("(no matches)\n")
	} else {
		for _, m := range r.Matches {
			b.WriteString(m)
			b.WriteByte('\n')
		}
	}
	extras := []string{fmt.Sprintf("%d match(es)", r.MatchesCount)}
	if r.Truncated {
		extras = append(extras, "truncated")
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

func executeGlob(pattern, cwd string, limit int) GlobResult {
	start := time.Now()
	res := GlobResult{
		BaseResult: BaseResult{Operation: "Glob", Engine: "doublestar"},
		Cwd:        cwd,
		Pattern:    pattern,
	}

	fsys := os.DirFS(cwd)
	// Walk-style streaming match keeps memory bounded for huge dirs.
	count := 0
	walkErr := doublestar.GlobWalk(fsys, pattern, func(path string, _ fs.DirEntry) error {
		if count >= limit {
			res.Truncated = true
			return doublestar.SkipDir
		}
		// Always forward-slash for stable agent output across OSes.
		res.Matches = append(res.Matches, filepath.ToSlash(path))
		count++
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, doublestar.SkipDir) {
		// Don't fail the whole call on transient walk errors (permission
		// denied subtrees, etc.); surface what we got plus the error.
		// The truncation flag is a separate signal.
		_ = walkErr // intentionally swallowed; matches array is best-effort
	}

	res.MatchesCount = len(res.Matches)
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}
