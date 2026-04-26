// Package core — Read returns file content with stable line cursors,
// deterministic line counts, and per-format dispatch.
//
// Per ADR-007 we wrap mature engines instead of writing parsers:
//   - text          → stdlib bufio (line-walked, single-pass)
//   - pdf           → pdftotext (poppler-utils) shell-out
//   - ipynb         → native JSON cell parse
//   - docx          → pandoc shell-out (universal office converter)
//   - xlsx          → github.com/xuri/excelize/v2 (Microsoft/Alibaba/Oracle in prod)
//   - csv / tsv     → stdlib encoding/csv (header + bounded preview)
//   - html          → github.com/go-shiori/go-readability (Mozilla Readability port)
//   - json / yaml / toml / xml → text passthrough with format tag
//   - binary        → refused with structured error
//
// Adding a new format means: extend detectFormat, add a reader function,
// update CoreToolDocs's description, ship tests for the format. The
// readResult shape stays uniform so the agent never has to branch on
// which engine ran.
package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	readSizeCapBytes = 5 * 1024 * 1024 // 5 MB cap on returned content
)

// ReadResult is the uniform shape across all formats. Embeds
// BaseResult so common fields (engine, duration, error) and their
// rendering helpers are inherited.
type ReadResult struct {
	BaseResult
	Path       string `json:"path"`
	Content    string `json:"content"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	TotalLines int    `json:"total_lines"`
	SizeBytes  int64  `json:"size_bytes"`
	Format     string `json:"format"`
	Truncated  bool   `json:"truncated"`

	// Sheets is populated only for spreadsheet formats; lets the agent
	// page through workbook structure without re-reading the file.
	Sheets []string `json:"sheets,omitempty"`
}

// RegisterRead adds the Read tool to the given MCP server.
func RegisterRead(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Read",
		mcp.WithDescription(
			"Read a file with stable line cursors and deterministic line counts. "+
				"Format-aware: plain text, PDF (pdftotext), Jupyter notebooks (.ipynb), "+
				"Word (.docx via pandoc), Excel (.xlsx via excelize), CSV/TSV, HTML (Readability), "+
				"and JSON/YAML/TOML/XML pass-through with format tagging. "+
				"Binary files unrecognized are refused with a structured error.",
		),
		mcp.WithString("path", mcp.Required(),
			mcp.Description("File path. Resolved relative to cwd.")),
		mcp.WithString("cwd",
			mcp.Description("Working directory. Defaults to $HOME if empty.")),
		mcp.WithNumber("line_start",
			mcp.Description("First line to return, 1-indexed inclusive. Default 1.")),
		mcp.WithNumber("line_end",
			mcp.Description("Last line to return, 1-indexed inclusive. Default end of file.")),
		mcp.WithString("sheet",
			mcp.Description("For .xlsx: name of the sheet to render. Defaults to the first sheet.")),
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
	lineEnd := int(req.GetFloat("line_end", 0)) // 0 = EOF
	sheet := req.GetString("sheet", "")

	res := executeRead(ctx, path, lineStart, lineEnd, sheet)
	return resultOf(res), nil
}

// Render satisfies the Renderer contract. The body is the file
// content framed by horizontal rules; header carries path and
// engine, footer carries cursor + size.
func (r ReadResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Path)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("%s · %s · %s", r.Path, r.Format, humanBytes(r.SizeBytes))))
	b.WriteByte('\n')
	if len(r.Sheets) > 0 {
		fmt.Fprintf(&b, "sheets: %s\n", strings.Join(r.Sheets, ", "))
	}
	b.WriteString("───\n")
	b.WriteString(r.Content)
	if !strings.HasSuffix(r.Content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("───\n")
	extras := []string{fmt.Sprintf("lines %d–%d of %d", r.LineStart, r.LineEnd, r.TotalLines)}
	if r.Truncated {
		extras = append(extras, "truncated")
	}
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}


func executeRead(ctx context.Context, path string, lineStart, lineEnd int, sheet string) ReadResult {
	start := time.Now()
	res := ReadResult{
		BaseResult: BaseResult{Operation: "Read"},
		Path:       path,
		LineStart:  lineStart,
		LineEnd:    lineEnd,
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
	case "docx":
		res.Engine = "pandoc"
		readDocx(ctx, path, lineStart, lineEnd, &res)
	case "xlsx":
		res.Engine = "excelize"
		readXlsx(path, sheet, lineStart, lineEnd, &res)
	case "csv":
		res.Engine = "csv-stdlib"
		readCSV(path, ',', lineStart, lineEnd, &res)
	case "tsv":
		res.Engine = "csv-stdlib"
		readCSV(path, '\t', lineStart, lineEnd, &res)
	case "html":
		res.Engine = "go-readability"
		readHTML(path, lineStart, lineEnd, &res)
	case "json", "yaml", "toml", "xml":
		// Structured-text formats: passthrough with format tag.
		res.Engine = "stdlib"
		readText(path, lineStart, lineEnd, &res)
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

// detectFormat picks a format tag for path. Extension-first; content sniff
// only when the extension is missing or generic enough to mislead.
func detectFormat(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return "pdf"
	case ".ipynb":
		return "ipynb"
	case ".docx":
		return "docx"
	case ".xlsx":
		return "xlsx"
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	case ".html", ".htm":
		return "html"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	}
	// Sniff content for files without a hint extension or with .txt.
	f, err := os.Open(path)
	if err != nil {
		return "text"
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	head := buf[:n]
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return "pdf"
	}
	// PK\x03\x04 = zip magic — but xlsx/docx/odt all use this. Without
	// extension we can't disambiguate cheaply, so we fall through to
	// binary-rejected which sends a helpful error.
	if bytes.IndexByte(head, 0x00) >= 0 {
		return "binary-rejected"
	}
	// Best-effort HTML sniff for content delivered without an extension.
	low := strings.ToLower(string(head))
	if strings.HasPrefix(strings.TrimSpace(low), "<!doctype html") ||
		strings.HasPrefix(strings.TrimSpace(low), "<html") {
		return "html"
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

	body, err := io.ReadAll(f)
	if err != nil {
		res.ErrorReason = err.Error()
		return
	}
	applyLineRangeFromBuffer(string(body), lineStart, lineEnd, res)
}

// applyLineRangeFromBuffer is the shared post-processor for engines that
// produce a single in-memory string. Applies the line range, computes
// total_lines, respects the size cap, sets Truncated when end < total.
func applyLineRangeFromBuffer(buf string, lineStart, lineEnd int, res *ReadResult) {
	all := strings.Split(buf, "\n")
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

// Sentinel for empty errors.
var _ = errors.New
