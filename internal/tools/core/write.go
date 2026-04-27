// Package core — Write creates or overwrites a whole file with given
// content. Sister tool to Edit; the boundary is intentional:
//
//   - Edit modifies a substring of an existing file. Refuses if the file
//     is missing.
//   - Write replaces or creates the whole file. If the file already exists
//     and has detectable line endings, those endings are preserved by
//     default so existing tooling (CR LF on Windows, etc.) keeps working.
//
// Per ADR-007 we wrap stdlib `os` for I/O. The polish layer is the same
// atomic-write primitive Edit uses, plus a parent-directory auto-create
// so agents don't need a separate `mkdir` step before writing.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/hooks"
	"github.com/cogitave/clawtool/internal/lint"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// guardReadBeforeWrite enforces ADR-021's Read-before-Write
// invariant. Returns nil to proceed, or a descriptive error the
// caller surfaces verbatim. Never panics; never reads the
// existing file body — only os.Stat for existence + the session
// registry for the prior-Read record.
func guardReadBeforeWrite(ctx context.Context, path, mode string, mustNotExist, unsafeOverwrite bool) error {
	exists := false
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			// Let executeWrite emit the directory error.
			return nil
		}
		exists = true
	}

	switch mode {
	case "create":
		if exists {
			return fmt.Errorf("Write mode=\"create\" but %q already exists; use mode=\"overwrite\" or pick a different path", path)
		}
		return nil
	case "", "overwrite":
		// fall through to the overwrite branch below.
	default:
		return fmt.Errorf("Write mode must be \"\" | \"create\" | \"overwrite\" (got %q)", mode)
	}

	if mustNotExist && exists {
		return fmt.Errorf("Write must_not_exist=true but %q already exists", path)
	}

	if !exists {
		// Brand-new file via the implicit overwrite path. We
		// allow it (matches pre-ADR-021 behaviour) but the
		// agent is encouraged to use mode="create" for clarity.
		return nil
	}

	if unsafeOverwrite {
		return nil // explicit opt-out, loud at call site
	}

	sid := SessionKeyFromContext(ctx)
	rec, ok := Sessions.ReadOf(sid, path)
	if !ok {
		return errors.New(
			"Write refused: this session has not Read " + path + " — Read it first " +
				"(or pass mode=\"create\" for a brand-new file, or " +
				"unsafe_overwrite_without_read=true to bypass the ADR-021 guardrail).",
		)
	}
	currentHash, err := HashFile(path)
	if err != nil {
		return fmt.Errorf("hash %q: %w", path, err)
	}
	if currentHash != rec.FileHash {
		return errors.New(
			"Write refused: " + path + " changed since this session Read it " +
				"(file_hash mismatch — likely an external edit). Re-Read the " +
				"file before overwriting, or pass " +
				"unsafe_overwrite_without_read=true to bypass.",
		)
	}
	return nil
}

// WriteResult is the uniform shape returned to the agent.
type WriteResult struct {
	BaseResult
	Path         string         `json:"path"`
	BytesWritten int64          `json:"bytes_written"`
	Created      bool           `json:"created"`
	LineEndings  string         `json:"line_endings"`
	LintFindings []lint.Finding `json:"lint_findings,omitempty"`
}

// RegisterWrite adds the Write tool to the given MCP server.
func RegisterWrite(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Write",
		mcp.WithDescription(
			"Create or replace a whole file. Atomic temp+rename. Parent directory "+
				"auto-created when create_parents=true (default). When the file "+
				"already exists, line endings (LF/CRLF/CR) are preserved by default "+
				"so existing Windows / legacy tooling stays compatible.",
		),
		mcp.WithString("path", mcp.Required(),
			mcp.Description("File path. Created if absent.")),
		mcp.WithString("content", mcp.Required(),
			mcp.Description("Full file content. Use empty string to write a zero-byte file.")),
		mcp.WithBoolean("create_parents",
			mcp.Description("Create missing parent directories. Default true.")),
		mcp.WithBoolean("preserve_line_endings",
			mcp.Description("When the file exists, preserve its detected line-ending style. Default true.")),
		mcp.WithString("line_endings",
			mcp.Description("Force a specific style: lf | crlf | cr. Overrides preserve_line_endings.")),
		mcp.WithString("cwd",
			mcp.Description("Working directory for relative paths. Defaults to $HOME.")),
		mcp.WithString("mode",
			mcp.Description("\"create\" to require the file does NOT exist (brand-new file flow); \"overwrite\" to require a prior Read on the same MCP session of an existing file. Default \"overwrite\". ADR-021 Read-before-Write guardrail.")),
		mcp.WithBoolean("must_not_exist",
			mcp.Description("Companion of mode=\"create\": if true, fail when the path already exists. Default false (legacy passthrough; mode=\"create\" implies true).")),
		mcp.WithBoolean("unsafe_overwrite_without_read",
			mcp.Description("Bypass the Read-before-Write check. Loud, opt-in. Use only when the operator has confirmed they intend to overwrite a file the agent has not Read this session. ADR-021.")),
	)
	s.AddTool(tool, runWrite)
}

func runWrite(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: path"), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: content"), nil
	}
	createParents := req.GetBool("create_parents", true)
	preserveEndings := req.GetBool("preserve_line_endings", true)
	forced := req.GetString("line_endings", "")
	cwd := req.GetString("cwd", "")
	mode := strings.ToLower(strings.TrimSpace(req.GetString("mode", "")))
	mustNotExist := req.GetBool("must_not_exist", false)
	unsafeOverwrite := req.GetBool("unsafe_overwrite_without_read", false)

	resolved := resolvePath(path, cwd)

	// ADR-021 Read-before-Write guardrail.
	if guardErr := guardReadBeforeWrite(ctx, resolved, mode, mustNotExist, unsafeOverwrite); guardErr != nil {
		return resultOf(WriteResult{
			BaseResult: BaseResult{Operation: "Write", ErrorReason: guardErr.Error()},
			Path:       resolved,
		}), nil
	}

	if mgr := hooks.Get(); mgr != nil {
		if hookErr := mgr.Emit(ctx, hooks.EventPreEdit, map[string]any{
			"path":  resolved,
			"write": true,
			"bytes": len(content),
		}); hookErr != nil {
			return resultOf(WriteResult{
				BaseResult: BaseResult{Operation: "Write", ErrorReason: hookErr.Error()},
				Path:       resolved,
			}), nil
		}
	}
	res := executeWrite(resolved, content, createParents, preserveEndings, LineEndings(forced))
	if !res.IsError() && lintEnabled() {
		if findings, _ := globalLintRunner.Lint(ctx, res.Path); len(findings) > 0 {
			res.LintFindings = findings
		}
	}
	if mgr := hooks.Get(); mgr != nil && !res.IsError() {
		_ = mgr.Emit(ctx, hooks.EventPostEdit, map[string]any{
			"path":          res.Path,
			"created":       res.Created,
			"bytes_written": res.BytesWritten,
			"lint_findings": len(res.LintFindings),
			"write":         true,
		})
	}
	return resultOf(res), nil
}

// Render satisfies the Renderer contract. The Operation field
// switches between "Write" and "Create" based on whether the file
// existed beforehand — agents glance-read whether something was
// clobbered.
func (r WriteResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Path)
	}
	return r.SuccessLine(r.Path, humanBytes(r.BytesWritten), r.LineEndings)
}

func executeWrite(path, content string, createParents, preserveEndings bool, forced LineEndings) WriteResult {
	start := time.Now()
	res := WriteResult{
		BaseResult: BaseResult{Operation: "Write"},
		Path:       path,
	}

	// Pre-flight: parent dir.
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if !createParents {
			res.ErrorReason = fmt.Sprintf("parent directory %q does not exist (set create_parents=true to auto-create)", dir)
			res.DurationMs = time.Since(start).Milliseconds()
			return res
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			res.ErrorReason = "mkdir parents: " + err.Error()
			res.DurationMs = time.Since(start).Milliseconds()
			return res
		}
	}

	// Detect existing line endings if any, decide target style.
	var existing []byte
	created := true
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			res.ErrorReason = fmt.Sprintf("path %q is a directory", path)
			res.DurationMs = time.Since(start).Milliseconds()
			return res
		}
		existing, _ = os.ReadFile(path)
		created = false
	}

	target := forced
	switch target {
	case "":
		// No explicit override.
		if preserveEndings && len(existing) > 0 {
			target = detectLineEndings(existing)
		} else {
			target = LineEndingsLF
		}
	case LineEndingsLF, LineEndingsCRLF, LineEndingsCR:
		// Honored.
	case LineEndingsUnknown:
		target = LineEndingsLF
	default:
		res.ErrorReason = fmt.Sprintf("invalid line_endings %q (allowed: lf, crlf, cr)", forced)
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if target == LineEndingsUnknown {
		target = LineEndingsLF
	}
	res.LineEndings = string(target)

	final := applyLineEndings([]byte(content), target)

	// Preserve BOM from existing file if present and content lacks one.
	if !created {
		if bom, _ := detectBOM(existing); len(bom) > 0 {
			if existingBOM, _ := detectBOM(final); len(existingBOM) == 0 {
				final = append(append([]byte{}, bom...), final...)
			}
		}
	}

	if err := writeAtomic(path, final); err != nil {
		res.ErrorReason = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	res.BytesWritten = int64(len(final))
	res.Created = created
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}
