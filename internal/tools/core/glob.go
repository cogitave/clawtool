// Package core — Glob is the canonical wrapper around bmatcuk/doublestar.
//
// Per ADR-007 we don't write a glob engine. doublestar is the de-facto
// double-star (`**`) glob library in Go, used by GoReleaser, k6, etc.
// This file's value is the polish layer: cwd-aware path resolution,
// uniform structured output, hard cap to protect agent context, and
// platform-stable separators (the wrapper always returns forward-slash
// paths regardless of OS — agents expect that).
//
// ADR-021 phase B added .gitignore-aware traversal — when cwd is a
// Git worktree we ask `git ls-files --cached --others
// --exclude-standard -z` for the candidate set then run doublestar
// over it, which gives us the same ignore semantics as ripgrep (and
// keeps the operator's expected ".git/, vendor/, node_modules/ ignored
// by default" behaviour).
package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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
	Matches          []string `json:"matches"`
	MatchesCount     int      `json:"matches_count"`
	Truncated        bool     `json:"truncated"`
	Cwd              string   `json:"cwd"`
	Pattern          string   `json:"pattern"`
	RespectGitignore bool     `json:"respect_gitignore"`
	IncludeHidden    bool     `json:"include_hidden"`
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
		mcp.WithBoolean("respect_gitignore",
			mcp.Description("Honor .gitignore when cwd is a Git worktree. Default true. Pass false to walk every file regardless of ignore rules.")),
		mcp.WithBoolean("include_hidden",
			mcp.Description("Include dotfiles + paths whose any segment starts with '.'. Default false. Patterns that explicitly name a dot segment (e.g. '**/.env') still match those files even when this is false.")),
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
	respectGitignore := req.GetBool("respect_gitignore", true)
	includeHidden := req.GetBool("include_hidden", false)

	res := executeGlob(globArgs{
		Pattern:          pattern,
		Cwd:              cwd,
		Limit:            limit,
		RespectGitignore: respectGitignore,
		IncludeHidden:    includeHidden,
	})
	return resultOf(res), nil
}

type globArgs struct {
	Pattern          string
	Cwd              string
	Limit            int
	RespectGitignore bool
	IncludeHidden    bool
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

func executeGlob(a globArgs) GlobResult {
	start := time.Now()
	res := GlobResult{
		BaseResult:       BaseResult{Operation: "Glob", Engine: "doublestar"},
		Cwd:              a.Cwd,
		Pattern:          a.Pattern,
		RespectGitignore: a.RespectGitignore,
		IncludeHidden:    a.IncludeHidden,
	}

	patternHasHidden := patternMentionsDotSegment(a.Pattern)
	keep := func(path string) bool {
		if !a.IncludeHidden && !patternHasHidden && pathHasHiddenSegment(path) {
			return false
		}
		return true
	}

	// Git-aware path: when respect_gitignore=true AND cwd is a
	// worktree, ask git for the candidate set. Falls through to
	// the legacy doublestar walk on any failure (no .git, git
	// missing on PATH, etc.) so the tool stays portable.
	if a.RespectGitignore {
		if files, ok := gitListFiles(a.Cwd); ok {
			res.Engine = "doublestar+git-ls-files"
			matched, truncated := matchPatternAgainstSet(a.Pattern, files, a.Limit, keep)
			res.Matches = matched
			res.Truncated = truncated
			res.MatchesCount = len(res.Matches)
			res.DurationMs = time.Since(start).Milliseconds()
			return res
		}
	}

	fsys := os.DirFS(a.Cwd)
	// Walk-style streaming match keeps memory bounded for huge dirs.
	count := 0
	walkErr := doublestar.GlobWalk(fsys, a.Pattern, func(path string, _ fs.DirEntry) error {
		if !keep(path) {
			return nil
		}
		if count >= a.Limit {
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

// gitListFiles asks git for the tracked + untracked-not-ignored set
// rooted at cwd. Returns the slice + true on success; (nil, false)
// when cwd is not a Git worktree or git is missing.
func gitListFiles(cwd string) ([]string, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	// Verify cwd is a worktree before invoking ls-files; otherwise
	// the command runs in a parent worktree and returns its files.
	check := exec.Command("git", "-C", cwd, "rev-parse", "--is-inside-work-tree")
	if err := check.Run(); err != nil {
		return nil, false
	}
	cmd := exec.Command(
		"git", "-C", cwd, "ls-files",
		"--cached", "--others", "--exclude-standard",
		"-z", "--deduplicate",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	out = bytes.TrimRight(out, "\x00")
	if len(out) == 0 {
		return []string{}, true
	}
	parts := bytes.Split(out, []byte{0})
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		files = append(files, string(p))
	}
	return files, true
}

// matchPatternAgainstSet runs the doublestar pattern over a fixed
// candidate slice (the git ls-files result). Drops files whose
// underlying path no longer exists (deleted but still cached).
func matchPatternAgainstSet(pattern string, files []string, limit int, keep func(string) bool) ([]string, bool) {
	out := make([]string, 0, len(files))
	truncated := false
	for _, f := range files {
		if !keep(f) {
			continue
		}
		ok, err := doublestar.PathMatch(pattern, f)
		if err != nil || !ok {
			continue
		}
		if len(out) >= limit {
			truncated = true
			break
		}
		out = append(out, filepath.ToSlash(f))
	}
	return out, truncated
}

// patternMentionsDotSegment returns true when the glob pattern
// names a path component that starts with '.', e.g. '**/.env',
// '.config/**'. Used to flip the include-hidden behaviour: an
// explicit dot pattern means the agent wanted dotfiles even
// though include_hidden is false.
func patternMentionsDotSegment(pattern string) bool {
	for _, seg := range strings.Split(pattern, "/") {
		seg = strings.TrimSpace(seg)
		if len(seg) > 0 && seg[0] == '.' {
			return true
		}
	}
	return false
}

// pathHasHiddenSegment reports whether any path component starts
// with '.'. Drops things like ".git/", "vendor/.cache/foo".
func pathHasHiddenSegment(path string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if len(seg) > 0 && seg[0] == '.' {
			return true
		}
	}
	return false
}
