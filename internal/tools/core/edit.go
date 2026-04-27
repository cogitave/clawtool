// Package core — Edit performs a precise search-and-replace on an existing
// file with safety polish:
//   - uniqueness check by default (refuses ambiguous edits)
//   - atomic temp+rename so a crash never leaves a half-written file
//   - line-ending preserve (LF / CRLF / CR detected from current content)
//   - BOM preserve
//   - binary refusal (symmetric with Read)
//
// Per ADR-007 we wrap stdlib `os` for I/O and add our own polish layer.
// The search-and-replace shape mirrors what Claude Code's native Edit
// uses today (old_string / new_string / replace_all) — agents that learnt
// that interface get the same affordances against clawtool's stronger
// invariants.
package core

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/hooks"
	"github.com/cogitave/clawtool/internal/lint"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// EditResult is the uniform shape returned to the agent.
type EditResult struct {
	BaseResult
	Path                string         `json:"path"`
	Replaced            bool           `json:"replaced"`
	OccurrencesReplaced int            `json:"occurrences_replaced"`
	SizeBytesBefore     int64          `json:"size_bytes_before"`
	SizeBytesAfter      int64          `json:"size_bytes_after"`
	LineEndings         string         `json:"line_endings"`
	LintFindings        []lint.Finding `json:"lint_findings,omitempty"`
}

// RegisterEdit adds the Edit tool to the given MCP server.
func RegisterEdit(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Edit",
		mcp.WithDescription(
			"Replace an exact substring in a file with new content. Atomic write "+
				"(temp+rename), line-ending preserve (LF/CRLF/CR detected from current "+
				"file), BOM preserve, binary refusal. By default refuses to run when "+
				"old_string appears more than once — set replace_all=true to override. "+
				"Use Write for whole-file create/replace.",
		),
		mcp.WithString("path", mcp.Required(),
			mcp.Description("File path. Must exist; use Write to create new files.")),
		mcp.WithString("old_string", mcp.Required(),
			mcp.Description("Exact substring to find. Must match byte-for-byte (whitespace counts).")),
		mcp.WithString("new_string", mcp.Required(),
			mcp.Description("Replacement substring. May be empty to delete the match.")),
		mcp.WithBoolean("replace_all",
			mcp.Description("Replace every occurrence instead of refusing on duplicates. Default false.")),
		mcp.WithString("cwd",
			mcp.Description("Working directory for relative paths. Defaults to $HOME.")),
	)
	s.AddTool(tool, runEdit)
}

func runEdit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: path"), nil
	}
	oldStr, err := req.RequireString("old_string")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: old_string"), nil
	}
	newStr := req.GetString("new_string", "")
	replaceAll := req.GetBool("replace_all", false)
	cwd := req.GetString("cwd", "")

	resolved := resolvePath(path, cwd)
	if mgr := hooks.Get(); mgr != nil {
		// pre_edit: block_on_error entries veto the write (e.g. a
		// "no edits inside vendor/" guard).
		if hookErr := mgr.Emit(ctx, hooks.EventPreEdit, map[string]any{
			"path":        resolved,
			"replace_all": replaceAll,
		}); hookErr != nil {
			return resultOf(EditResult{
				BaseResult: BaseResult{Operation: "Edit", ErrorReason: hookErr.Error()},
				Path:       resolved,
			}), nil
		}
	}
	res := executeEdit(resolved, oldStr, newStr, replaceAll)
	if !res.IsError() && lintEnabled() {
		if findings, _ := globalLintRunner.Lint(ctx, res.Path); len(findings) > 0 {
			res.LintFindings = findings
		}
	}
	if mgr := hooks.Get(); mgr != nil && !res.IsError() {
		_ = mgr.Emit(ctx, hooks.EventPostEdit, map[string]any{
			"path":          res.Path,
			"replaced":      res.Replaced,
			"size_after":    res.SizeBytesAfter,
			"lint_findings": len(res.LintFindings),
		})
	}
	return resultOf(res), nil
}

// globalLintRunner is the package-level Runner Edit/Write call. Init
// at package load (process boot) so we don't pay reflection on every
// call. Tests can swap via SetLintRunner.
var globalLintRunner lint.Runner = lint.New()

// SetLintRunner replaces the package-level Runner — used by tests to
// inject deterministic findings.
func SetLintRunner(r lint.Runner) { globalLintRunner = r }

// lintEnabled reads the package-level autoLintEnabled flag set by the
// server boot. Default = true (matches lint.IsEnabled(nil)).
var autoLintEnabled = true

// SetAutoLintEnabled lets server.go's boot path flip the flag based on
// config.AutoLint.Enabled. Idempotent.
func SetAutoLintEnabled(enabled bool) { autoLintEnabled = enabled }

func lintEnabled() bool { return autoLintEnabled }

// init: ensure the config import is referenced for forward-compat
// when AutoLintConfig grows additional fields the runner consumes.
var _ = config.AutoLintConfig{}

// Render satisfies the Renderer contract. Single-line success/failure;
// stateless tools don't need a multi-line body.
func (r EditResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Path)
	}
	delta := r.SizeBytesAfter - r.SizeBytesBefore
	sign := "+"
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	return r.SuccessLine(r.Path,
		fmt.Sprintf("%d replacement(s)", r.OccurrencesReplaced),
		fmt.Sprintf("%s%dB", sign, delta),
		r.LineEndings,
	)
}

// executeEdit is the testable core. Returns a populated EditResult; never
// panics; surfaces every failure via ErrorReason.
func executeEdit(path, oldStr, newStr string, replaceAll bool) EditResult {
	start := time.Now()
	res := EditResult{
		BaseResult: BaseResult{Operation: "Edit"},
		Path:       path,
	}

	if oldStr == "" {
		res.ErrorReason = "old_string is empty; nothing to find"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if oldStr == newStr {
		// No-op edits are an agent mistake worth flagging.
		res.ErrorReason = "old_string equals new_string; no change would be made"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	info, err := os.Stat(path)
	if err != nil {
		res.ErrorReason = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if info.IsDir() {
		res.ErrorReason = fmt.Sprintf("path %q is a directory", path)
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	res.SizeBytesBefore = info.Size()

	raw, err := os.ReadFile(path)
	if err != nil {
		res.ErrorReason = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if looksBinary(raw) {
		res.ErrorReason = "file contains NUL bytes (binary); refusing to edit (use Bash + sed/printf for raw byte work)"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	bom, body := detectBOM(raw)
	endings := detectLineEndings(body)
	res.LineEndings = string(endings)

	// Normalize content to LF for matching so old_string written with LF
	// matches a CRLF file. The output will be re-applied with the
	// detected endings.
	normalizedBody := applyLineEndings(body, LineEndingsLF)
	normalizedOld := applyLineEndings([]byte(oldStr), LineEndingsLF)
	normalizedNew := applyLineEndings([]byte(newStr), LineEndingsLF)

	occurrences := strings.Count(string(normalizedBody), string(normalizedOld))
	if occurrences == 0 {
		res.ErrorReason = "old_string not found in file"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if occurrences > 1 && !replaceAll {
		res.ErrorReason = fmt.Sprintf(
			"old_string appears %d times — refusing ambiguous edit. "+
				"Either include more context to make old_string unique, or pass replace_all=true.",
			occurrences,
		)
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	var newBody []byte
	if replaceAll {
		newBody = []byte(strings.ReplaceAll(string(normalizedBody), string(normalizedOld), string(normalizedNew)))
		res.OccurrencesReplaced = occurrences
	} else {
		newBody = []byte(strings.Replace(string(normalizedBody), string(normalizedOld), string(normalizedNew), 1))
		res.OccurrencesReplaced = 1
	}

	// Re-apply original line endings so the on-disk file matches its
	// original convention.
	newBody = applyLineEndings(newBody, endings)

	// Re-prepend BOM if the original had one.
	final := append([]byte{}, bom...)
	final = append(final, newBody...)

	if err := writeAtomic(path, final); err != nil {
		res.ErrorReason = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	res.Replaced = true
	res.SizeBytesAfter = int64(len(final))
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}
