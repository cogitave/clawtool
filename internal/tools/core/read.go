// Package core — Read returns file content with stable line cursors,
// deterministic line counts, and per-format dispatch (text / pdf / ipynb).
//
// Per ADR-007 we wrap stdlib for plain text (Go's `bufio` already excellent),
// shell out to `pdftotext` (poppler-utils) for PDF, and parse `.ipynb` JSON
// natively. Binary files we can't decode are refused with a structured
// "unsupported format" result rather than dumped as garbage.
package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	readSizeCapBytes = 5 * 1024 * 1024 // 5 MB cap on returned content
)

// ReadResult is the uniform shape across all formats.
type ReadResult struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	LineStart   int    `json:"line_start"`
	LineEnd     int    `json:"line_end"`
	TotalLines  int    `json:"total_lines"`
	SizeBytes   int64  `json:"size_bytes"`
	Format      string `json:"format"`        // "text" | "pdf" | "ipynb" | "binary-rejected"
	Engine      string `json:"engine"`        // "stdlib" | "pdftotext" | "ipynb-json"
	Truncated   bool   `json:"truncated"`     // true if hit byte cap or line_end before EOF
	DurationMs  int64  `json:"duration_ms"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// RegisterRead adds the Read tool to the given MCP server.
func RegisterRead(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Read",
		mcp.WithDescription(
			"Read a file with stable, line-based cursors and deterministic line counts. "+
				"Format-aware: plain text via stdlib, PDF via pdftotext (poppler) when present, "+
				"Jupyter notebooks (.ipynb) via native JSON cell parsing. "+
				"Binary files unrecognized are refused with a structured error rather than dumped.",
		),
		mcp.WithString("path", mcp.Required(),
			mcp.Description("File path. Resolved relative to cwd.")),
		mcp.WithString("cwd",
			mcp.Description("Working directory. Defaults to $HOME if empty.")),
		mcp.WithNumber("line_start",
			mcp.Description("First line to return, 1-indexed inclusive. Default 1.")),
		mcp.WithNumber("line_end",
			mcp.Description("Last line to return, 1-indexed inclusive. Default end of file.")),
	)
	s.AddTool(tool, runRead)
}

func runRead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: path"), nil
	}
	cwd := req.GetString("cwd", "")
	if cwd == "" {
		cwd = homeDir()
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	lineStart := int(req.GetFloat("line_start", 1))
	if lineStart < 1 {
		lineStart = 1
	}
	lineEndF := req.GetFloat("line_end", 0)
	lineEnd := int(lineEndF) // 0 means "to EOF"

	res := executeRead(ctx, path, lineStart, lineEnd)
	body, _ := json.Marshal(res)
	return mcp.NewToolResultText(string(body)), nil
}

func executeRead(ctx context.Context, path string, lineStart, lineEnd int) ReadResult {
	start := time.Now()
	res := ReadResult{Path: path, LineStart: lineStart, LineEnd: lineEnd}

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
	res.SizeBytes = info.Size()

	format := detectFormat(path)
	res.Format = format
	switch format {
	case "ipynb":
		res.Engine = "ipynb-json"
		readIpynb(path, lineStart, lineEnd, &res)
	case "pdf":
		res.Engine = "pdftotext"
		readPDF(ctx, path, lineStart, lineEnd, &res)
	case "binary-rejected":
		res.Engine = "stdlib"
		res.ErrorReason = "binary content detected; refusing to decode (use Bash with hexdump/xxd if you need raw bytes)"
	default: // "text"
		res.Engine = "stdlib"
		readText(path, lineStart, lineEnd, &res)
	}

	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// detectFormat picks one of "text" / "pdf" / "ipynb" / "binary-rejected".
//
// Rules (cheap to evaluate, ordered):
//  1. Extension hints first (.pdf, .ipynb).
//  2. Sniff first 4 KiB for NUL bytes or PDF magic.
//  3. Default: "text".
func detectFormat(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return "pdf"
	case ".ipynb":
		return "ipynb"
	}
	f, err := os.Open(path)
	if err != nil {
		return "text" // let the reader surface the actual open error
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	head := buf[:n]
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return "pdf"
	}
	if bytes.IndexByte(head, 0x00) >= 0 {
		return "binary-rejected"
	}
	return "text"
}

// readText walks the file once, counting lines and capturing the requested
// range. Single pass keeps it O(N) regardless of how large the file is.
func readText(path string, lineStart, lineEnd int, res *ReadResult) {
	f, err := os.Open(path)
	if err != nil {
		res.ErrorReason = err.Error()
		return
	}
	defer f.Close()

	var sb strings.Builder
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 16<<20)
	lineNo := 0
	bytesEmitted := 0
	for scan.Scan() {
		lineNo++
		if lineNo < lineStart {
			continue
		}
		if lineEnd > 0 && lineNo > lineEnd {
			res.Truncated = true
			// Continue counting total_lines so the caller can paginate.
			continue
		}
		if bytesEmitted >= readSizeCapBytes {
			res.Truncated = true
			continue
		}
		text := scan.Text()
		if bytesEmitted+len(text)+1 > readSizeCapBytes {
			text = text[:readSizeCapBytes-bytesEmitted]
			res.Truncated = true
		}
		sb.WriteString(text)
		sb.WriteByte('\n')
		bytesEmitted += len(text) + 1
	}
	if err := scan.Err(); err != nil && !errors.Is(err, io.EOF) {
		res.ErrorReason = err.Error()
	}
	res.TotalLines = lineNo
	res.Content = strings.TrimRight(sb.String(), "\n")
	if res.LineEnd == 0 || res.LineEnd > res.TotalLines {
		res.LineEnd = res.TotalLines
	}
}

// readPDF shells out to `pdftotext - -` (poppler-utils). Falls back to a
// helpful error message if the binary is absent — clawtool does not bundle
// poppler, per ADR-007 we rely on the system installation.
func readPDF(ctx context.Context, path string, lineStart, lineEnd int, res *ReadResult) {
	pdftotext := LookupEngine("pdftotext")
	if pdftotext.Bin == "" {
		res.ErrorReason = "pdftotext (poppler-utils) is not installed; install via your package manager (apt: poppler-utils, brew: poppler)"
		return
	}
	cmd := exec.CommandContext(ctx, pdftotext.Bin, "-layout", path, "-")
	applyProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		res.ErrorReason = "pdftotext failed: " + reason
		return
	}
	applyLineRangeFromBuffer(stdout.String(), lineStart, lineEnd, res)
}

// readIpynb parses the JSON cells and renders them as line-numbered text
// with cell-type markers. We do not try to be a full Jupyter renderer; we
// give the agent something predictable to read.
func readIpynb(path string, lineStart, lineEnd int, res *ReadResult) {
	raw, err := os.ReadFile(path)
	if err != nil {
		res.ErrorReason = err.Error()
		return
	}
	var nb struct {
		Cells []struct {
			CellType string          `json:"cell_type"`
			Source   json.RawMessage `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(raw, &nb); err != nil {
		res.ErrorReason = "invalid ipynb JSON: " + err.Error()
		return
	}
	var sb strings.Builder
	for i, cell := range nb.Cells {
		fmt.Fprintf(&sb, "# --- cell %d (%s) ---\n", i+1, cell.CellType)
		// `source` may be a string or array of strings (legacy nb format).
		if len(cell.Source) > 0 && cell.Source[0] == '[' {
			var lines []string
			_ = json.Unmarshal(cell.Source, &lines)
			for _, line := range lines {
				sb.WriteString(line)
				if !strings.HasSuffix(line, "\n") {
					sb.WriteByte('\n')
				}
			}
		} else {
			var src string
			_ = json.Unmarshal(cell.Source, &src)
			sb.WriteString(src)
			if !strings.HasSuffix(src, "\n") {
				sb.WriteByte('\n')
			}
		}
	}
	applyLineRangeFromBuffer(sb.String(), lineStart, lineEnd, res)
}

// applyLineRangeFromBuffer is the shared post-processor for engines that
// produce a single in-memory string (PDF, ipynb). It applies the line
// range, computes total_lines, and respects the size cap.
func applyLineRangeFromBuffer(buf string, lineStart, lineEnd int, res *ReadResult) {
	all := strings.Split(buf, "\n")
	// Drop trailing empty produced by the final \n, if any.
	if n := len(all); n > 0 && all[n-1] == "" {
		all = all[:n-1]
	}
	res.TotalLines = len(all)
	if lineStart > res.TotalLines {
		res.Content = ""
		res.LineEnd = res.TotalLines
		res.Truncated = true
		return
	}
	end := lineEnd
	if end == 0 || end > res.TotalLines {
		end = res.TotalLines
	}
	if end < lineStart {
		end = lineStart
	}
	slice := all[lineStart-1 : end]
	out := strings.Join(slice, "\n")
	if len(out) > readSizeCapBytes {
		out = out[:readSizeCapBytes]
		res.Truncated = true
	}
	if end < res.TotalLines && lineEnd != 0 {
		res.Truncated = true
	}
	res.Content = out
	res.LineEnd = end
}
