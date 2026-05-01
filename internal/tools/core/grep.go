// Package core — Grep wraps ripgrep when present, falls back to system grep.
//
// Per ADR-007 we curate ripgrep as the default engine: it has the correct
// .gitignore semantics, --type aliases, fast Aho-Corasick matching, and a
// stable JSON output that is straightforward to parse. The fallback is
// system grep (-rn) so clawtool's Grep is never unavailable.
//
// Output is uniform across engines so the agent never has to know which
// one ran. The `engine` field in the result lets users / tests verify.
package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	grepDefaultMaxMatches = 100
	grepHardCapMatches    = 10000
)

// GrepResult is the uniform shape returned regardless of engine.
type GrepResult struct {
	BaseResult
	Matches      []GrepMatch `json:"matches"`
	MatchesCount int         `json:"matches_count"`
	Truncated    bool        `json:"truncated"`
	Cwd          string      `json:"cwd"`
	Pattern      string      `json:"pattern"`
}

// GrepMatch is a single hit. Line and column are 1-indexed for human
// readability and to match conventional editor jumping. Before/After
// arrive populated only when the caller asked for context lines.
type GrepMatch struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Column int      `json:"column"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// RegisterGrep adds the Grep tool to the given MCP server.
func RegisterGrep(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Grep",
		mcp.WithDescription(
			"Search file contents for a literal string or regular expression "+
				"and return per-match path, 1-indexed line/column, matched "+
				"text. Use Grep when the operator wants an EXACT token, "+
				"identifier, regex, or string they remember verbatim "+
				"(\"find every call to FooBar\", \"grep for TODO\"). NOT for "+
				"conceptual / intent-based queries (\"how is auth rotated?\") "+
				"— use SemanticSearch for those. Powered by ripgrep (`rg`) "+
				"when available with .gitignore-aware traversal and --type "+
				"aliases; falls back to system grep otherwise. Uniform "+
				"structured result regardless of engine.",
		),
		mcp.WithString("pattern", mcp.Required(),
			mcp.Description("Regular expression to search for.")),
		mcp.WithString("path",
			mcp.Description("File or directory to search. Defaults to cwd.")),
		mcp.WithString("cwd",
			mcp.Description("Working directory. Defaults to $HOME if empty.")),
		mcp.WithString("glob",
			mcp.Description("Glob filter, e.g. '*.go'. Honored by both engines.")),
		mcp.WithString("type",
			mcp.Description("File-type alias (rg --type), e.g. 'go', 'py', 'ts'. Ignored under grep fallback.")),
		mcp.WithBoolean("case_insensitive",
			mcp.Description("Case-insensitive match.")),
		mcp.WithNumber("max_matches",
			mcp.Description(fmt.Sprintf("Cap on matches returned. Default %d, hard max %d.",
				grepDefaultMaxMatches, grepHardCapMatches))),
		mcp.WithNumber("context_before",
			mcp.Description("Lines of source context BEFORE each hit (`rg -B`). Default 0.")),
		mcp.WithNumber("context_after",
			mcp.Description("Lines of source context AFTER each hit (`rg -A`). Default 0.")),
		mcp.WithString("patterns",
			mcp.Description("Newline-separated additional patterns OR-ed with `pattern`. Lets the agent find a definition AND its callers in one turn.")),
	)
	s.AddTool(tool, runGrep)
}

func runGrep(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern, err := req.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: pattern"), nil
	}
	cwd := defaultCwd(req.GetString("cwd", ""))
	path := req.GetString("path", ".")
	glob := req.GetString("glob", "")
	typeAlias := req.GetString("type", "")
	caseI := req.GetBool("case_insensitive", false)
	maxMatches := int(req.GetFloat("max_matches", float64(grepDefaultMaxMatches)))
	if maxMatches <= 0 {
		maxMatches = grepDefaultMaxMatches
	}
	if maxMatches > grepHardCapMatches {
		maxMatches = grepHardCapMatches
	}
	ctxBefore := int(req.GetFloat("context_before", 0))
	ctxAfter := int(req.GetFloat("context_after", 0))
	if ctxBefore < 0 {
		ctxBefore = 0
	}
	if ctxAfter < 0 {
		ctxAfter = 0
	}
	// Hard cap context to keep payloads sane — 50 each side is
	// already plenty for any code-comprehension turn.
	if ctxBefore > 50 {
		ctxBefore = 50
	}
	if ctxAfter > 50 {
		ctxAfter = 50
	}
	patterns := []string{pattern}
	if extra := strings.TrimSpace(req.GetString("patterns", "")); extra != "" {
		for _, p := range strings.Split(extra, "\n") {
			p = strings.TrimSpace(p)
			if p != "" {
				patterns = append(patterns, p)
			}
		}
	}

	res := executeGrep(ctx, grepArgs{
		Pattern:       pattern,
		Patterns:      patterns,
		Cwd:           cwd,
		Path:          path,
		Glob:          glob,
		Type:          typeAlias,
		IgnoreCase:    caseI,
		MaxMatches:    maxMatches,
		ContextBefore: ctxBefore,
		ContextAfter:  ctxAfter,
	})
	return resultOf(res), nil
}

// Render satisfies the Renderer contract. Output mirrors ripgrep's
// standard `path:line:col: text` so a developer reading the chat
// sees the same shape they'd see in a terminal.
func (r GrepResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Pattern)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("rg %q", r.Pattern)))
	b.WriteByte('\n')
	if len(r.Matches) == 0 {
		b.WriteString("(no matches)\n")
	} else {
		for _, m := range r.Matches {
			for i, c := range m.Before {
				fmt.Fprintf(&b, "%s-%d-: %s\n", m.Path, m.Line-len(m.Before)+i, c)
			}
			fmt.Fprintf(&b, "%s:%d:%d: %s\n", m.Path, m.Line, m.Column, m.Text)
			for i, c := range m.After {
				fmt.Fprintf(&b, "%s-%d-: %s\n", m.Path, m.Line+i+1, c)
			}
			if len(m.Before) > 0 || len(m.After) > 0 {
				b.WriteString("--\n")
			}
		}
	}
	extras := []string{fmt.Sprintf("%d match(es)", r.MatchesCount)}
	if r.Truncated {
		extras = append(extras, fmt.Sprintf("truncated at %d (raise max_matches up to %d for more)", r.MatchesCount, grepHardCapMatches))
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

type grepArgs struct {
	Pattern       string
	Patterns      []string // OR-ed; first entry equals Pattern for back-compat.
	Cwd           string
	Path          string
	Glob          string
	Type          string
	IgnoreCase    bool
	MaxMatches    int
	ContextBefore int
	ContextAfter  int
}

// executeGrep runs the search and returns a uniform GrepResult. Engine
// selection: ripgrep when present, else system grep. The result's `engine`
// field reflects which one ran.
func executeGrep(ctx context.Context, a grepArgs) GrepResult {
	start := time.Now()
	res := GrepResult{
		BaseResult: BaseResult{Operation: "Grep"},
		Cwd:        a.Cwd,
		Pattern:    a.Pattern,
	}

	if rg := LookupEngine("rg"); rg.Bin != "" {
		res.Engine = "ripgrep"
		runRipgrep(ctx, rg.Bin, a, &res)
	} else if grep := LookupEngine("grep"); grep.Bin != "" {
		res.Engine = "grep"
		runSystemGrep(ctx, grep.Bin, a, &res)
	} else {
		res.Engine = "none"
	}

	res.MatchesCount = len(res.Matches)
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// runRipgrep invokes `rg --json` and parses event-stream output.
func runRipgrep(ctx context.Context, bin string, a grepArgs, out *GrepResult) {
	args := []string{
		"--json",
		"--max-count", strconv.Itoa(a.MaxMatches),
		"--no-messages",
	}
	if a.IgnoreCase {
		args = append(args, "-i")
	}
	if a.Glob != "" {
		args = append(args, "--glob", a.Glob)
	}
	if a.Type != "" {
		args = append(args, "--type", a.Type)
	}
	if a.ContextBefore > 0 {
		args = append(args, "-B", strconv.Itoa(a.ContextBefore))
	}
	if a.ContextAfter > 0 {
		args = append(args, "-A", strconv.Itoa(a.ContextAfter))
	}
	patterns := a.Patterns
	if len(patterns) == 0 {
		patterns = []string{a.Pattern}
	}
	for _, p := range patterns {
		args = append(args, "-e", p)
	}
	args = append(args, a.Path)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = a.Cwd
	applyProcessGroup(cmd)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run() // rg returns nonzero on no-match; we don't treat that as error.

	scan := bufio.NewScanner(&stdout)
	scan.Buffer(make([]byte, 1<<20), 16<<20) // permit long lines
	matches := 0
	pendingMatchIdx := -1
	// pendingContext buffers `context` events as they arrive — rg
	// emits Before-context events BEFORE the corresponding `match`,
	// so we can't attach them until we see the next match. After
	// the loop any leftover events become trailing After-context
	// of the last match.
	var pendingContext []rgEvent

	flushPending := func(nextMatchLine int) (before []string) {
		for _, c := range pendingContext {
			text := strings.TrimRight(c.Data.Lines.Text, "\n")
			if c.Data.LineNumber < nextMatchLine {
				before = append(before, text)
			} else if pendingMatchIdx >= 0 {
				out.Matches[pendingMatchIdx].After = append(out.Matches[pendingMatchIdx].After, text)
			}
		}
		pendingContext = pendingContext[:0]
		return
	}

loop:
	for scan.Scan() {
		var event rgEvent
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			continue
		}
		switch event.Type {
		case "begin", "end":
			// File boundary. rg never emits context across files,
			// so trailing context belongs to the prior file's
			// last match — flush as After of that match.
			for _, c := range pendingContext {
				if pendingMatchIdx >= 0 {
					out.Matches[pendingMatchIdx].After = append(
						out.Matches[pendingMatchIdx].After,
						strings.TrimRight(c.Data.Lines.Text, "\n"),
					)
				}
			}
			pendingContext = pendingContext[:0]
		case "match":
			if matches >= a.MaxMatches {
				out.Truncated = true
				break loop
			}
			beforeForThis := flushPending(event.Data.LineNumber)
			path := event.Data.Path.Text
			line := event.Data.LineNumber
			text := strings.TrimRight(event.Data.Lines.Text, "\n")
			col := 1
			if len(event.Data.Submatches) > 0 {
				col = event.Data.Submatches[0].Start + 1
			}
			out.Matches = append(out.Matches, GrepMatch{
				Path:   path,
				Line:   line,
				Column: col,
				Text:   text,
				Before: beforeForThis,
			})
			pendingMatchIdx = len(out.Matches) - 1
			matches++
		case "context":
			pendingContext = append(pendingContext, event)
		}
	}
	// Tail flush: any remaining context belongs to the last match.
	for _, c := range pendingContext {
		if pendingMatchIdx >= 0 {
			out.Matches[pendingMatchIdx].After = append(
				out.Matches[pendingMatchIdx].After,
				strings.TrimRight(c.Data.Lines.Text, "\n"),
			)
		}
	}
}

// rgEvent matches the subset of ripgrep's --json schema we care about.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       rgPath       `json:"path"`
		LineNumber int          `json:"line_number"`
		Lines      rgPath       `json:"lines"`
		Submatches []rgSubmatch `json:"submatches"`
	} `json:"data"`
}
type rgPath struct {
	Text string `json:"text"`
}
type rgSubmatch struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// runSystemGrep is the fallback. -rHn prints `path:line:text` deterministically.
func runSystemGrep(ctx context.Context, bin string, a grepArgs, out *GrepResult) {
	args := []string{"-rHn"}
	if a.IgnoreCase {
		args = append(args, "-i")
	}
	if a.Glob != "" {
		args = append(args, "--include", a.Glob)
	}
	args = append(args, "-E", a.Pattern, a.Path)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = a.Cwd
	applyProcessGroup(cmd)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run() // grep exits 1 on no-match.

	scan := bufio.NewScanner(&stdout)
	scan.Buffer(make([]byte, 1<<20), 16<<20)
	for scan.Scan() {
		if len(out.Matches) >= a.MaxMatches {
			out.Truncated = true
			break
		}
		line := scan.Text()
		// Format: path:line:text  — split on first two ':'.
		first := strings.IndexByte(line, ':')
		if first < 0 {
			continue
		}
		second := strings.IndexByte(line[first+1:], ':')
		if second < 0 {
			continue
		}
		second += first + 1
		path := line[:first]
		ln, err := strconv.Atoi(line[first+1 : second])
		if err != nil {
			continue
		}
		text := line[second+1:]
		out.Matches = append(out.Matches, GrepMatch{
			Path:   path,
			Line:   ln,
			Column: 1,
			Text:   text,
		})
	}
}
