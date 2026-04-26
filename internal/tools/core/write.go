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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// WriteResult is the uniform shape returned to the agent.
type WriteResult struct {
	Path         string `json:"path"`
	BytesWritten int64  `json:"bytes_written"`
	Created      bool   `json:"created"`
	LineEndings  string `json:"line_endings"`
	DurationMs   int64  `json:"duration_ms"`
	ErrorReason  string `json:"error_reason,omitempty"`
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
	)
	s.AddTool(tool, runWrite)
}

func runWrite(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	res := executeWrite(resolvePath(path, cwd), content, createParents, preserveEndings, LineEndings(forced))
	body, _ := json.Marshal(res)
	return mcp.NewToolResultText(string(body)), nil
}

func executeWrite(path, content string, createParents, preserveEndings bool, forced LineEndings) WriteResult {
	start := time.Now()
	res := WriteResult{Path: path}

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
