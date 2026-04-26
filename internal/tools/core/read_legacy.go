package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// readPDF shells out to `pdftotext - -` (poppler-utils). Falls back to a
// helpful error message if the binary is absent — clawtool does not bundle
// poppler.
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
// with cell-type markers.
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
