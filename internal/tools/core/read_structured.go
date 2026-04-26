package core

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
)

// readCSV parses a delimited file with the stdlib reader and renders a
// header-aware preview. We don't try to "be Pandas" — agents that need
// full data analysis run a Python source. clawtool's job is to give a
// faithful textual peek (header row, then up to N data rows) the agent
// can use to plan further actions.
//
// Quoting is handled by encoding/csv (RFC 4180 default). We use
// LazyQuotes + FieldsPerRecord=-1 so real-world ragged files don't
// abort with an error.
func readCSV(path string, comma rune, lineStart, lineEnd int, res *ReadResult) {
	f, err := os.Open(path)
	if err != nil {
		res.ErrorReason = err.Error()
		return
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = comma
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	var (
		sb       strings.Builder
		header   []string
		rowCount int
	)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Continue past a malformed row — emit a marker so the agent
			// can see we skipped something rather than silently dropping.
			fmt.Fprintf(&sb, "# (skipped malformed row: %v)\n", err)
			continue
		}
		if header == nil {
			header = row
			fmt.Fprintf(&sb, "# columns (%d): %s\n", len(header), strings.Join(header, " | "))
			continue
		}
		// Use a TSV-ish render so a single delimiter per line is enough
		// for visual scan. Pipe-spaced is friendlier than tab in chat.
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteByte('\n')
		rowCount++
	}
	if header != nil {
		fmt.Fprintf(&sb, "# total data rows: %d\n", rowCount)
	}
	applyLineRangeFromBuffer(sb.String(), lineStart, lineEnd, res)
}
