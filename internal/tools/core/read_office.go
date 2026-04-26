package core

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// readDocx shells out to pandoc to render the Word document as plain text.
// We chose pandoc over pure-Go libraries because:
//   - it handles the full .docx zoo (tables, footnotes, lists, comments);
//   - it's already the universal office-format converter agents tend to
//     have available (Microsoft, NASA, academia ship it everywhere);
//   - shelling out keeps clawtool's MIT license clean despite pandoc being
//     GPL-licensed (no Go-side linkage means no license bleed).
func readDocx(ctx context.Context, path string, lineStart, lineEnd int, res *ReadResult) {
	pandoc := LookupEngine("pandoc")
	if pandoc.Bin == "" {
		res.ErrorReason = "pandoc is not installed; install via your package manager (apt: pandoc, brew: pandoc) — required for .docx and other office formats"
		return
	}
	cmd := exec.CommandContext(ctx, pandoc.Bin, "--to=plain", "--wrap=preserve", path)
	applyProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		res.ErrorReason = "pandoc failed: " + reason
		return
	}
	applyLineRangeFromBuffer(stdout.String(), lineStart, lineEnd, res)
}

// readXlsx renders one sheet of the workbook as TSV-style rows.
//
// Workbook structure is exposed via the Sheets slice on ReadResult so the
// agent can page through with subsequent calls (`sheet="Sheet2"`). The
// default sheet is the first one — this matches every spreadsheet client's
// expected behaviour.
//
// We picked github.com/xuri/excelize/v2 per ADR-007: pure-Go, BSD-3,
// production-tested at Microsoft / Alibaba / Oracle. No CGO, no Excel
// runtime needed, handles XLSX 2007+ and XLSM/XLAM variants.
func readXlsx(path, requestedSheet string, lineStart, lineEnd int, res *ReadResult) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		res.ErrorReason = "excelize open: " + err.Error()
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	res.Sheets = sheets
	if len(sheets) == 0 {
		res.ErrorReason = "no sheets in workbook"
		return
	}

	sheet := requestedSheet
	if sheet == "" {
		sheet = sheets[0]
	}
	// Validate requested sheet name.
	known := false
	for _, s := range sheets {
		if s == sheet {
			known = true
			break
		}
	}
	if !known {
		res.ErrorReason = fmt.Sprintf("sheet %q not found; available: %s", sheet, strings.Join(sheets, ", "))
		return
	}

	rows, err := f.GetRows(sheet)
	if err != nil {
		res.ErrorReason = "excelize get rows: " + err.Error()
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# sheet: %s (%d rows)\n", sheet, len(rows))
	for _, row := range rows {
		// TSV-ish so column boundaries survive without quoting headaches.
		// Blank cells preserved as empty fields.
		sb.WriteString(strings.Join(row, "\t"))
		sb.WriteByte('\n')
	}
	applyLineRangeFromBuffer(sb.String(), lineStart, lineEnd, res)
}

// Useful constant for callers that want to inspect the maximum row count
// excelize will return without further configuration. Kept here so the
// magic number isn't sprinkled across tests.
const xlsxRowSummaryHint = "set line_end to a smaller number for a quick preview of large sheets"

// strconvShim avoids unused-import on builds without xlsx tests when
// strconv is otherwise unreferenced in this file.
var _ = strconv.Itoa
