// Package listfmt — small renderer used by every `clawtool * list`
// subcommand (bridges, agents, sources, recipes, sandboxes,
// portals, hooks, …). Repowire pattern: each list command
// accepts `--format json|tsv|table` (default: table) and the
// renderer outputs in the requested shape so shell pipes get
// machine-readable rows without needing `awk` to peel a
// human-formatted table.
//
// Usage:
//
//	listfmt.Render(stdout, "table", listfmt.Cols{
//	    Header: []string{"FAMILY", "STATUS", "DESCRIPTION"},
//	    Rows:   [][]string{{"codex", "ready", "..."}, ...},
//	})
//
// `format` is parsed once by the caller — either via
// listfmt.Parse(argv) which strips `--format X` from a flag
// slice, or by the caller's own arg parser. listfmt itself is
// pure rendering.
package listfmt

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Format enumerates the supported output shapes.
type Format string

const (
	FormatTable Format = "table" // human-readable, padded columns
	FormatTSV   Format = "tsv"   // tab-separated, no header padding — pipe-friendly
	FormatJSON  Format = "json"  // array of objects keyed by header
)

// DefaultFormat is what every list command falls back to when no
// `--format` flag is given. Table is the right default for
// interactive shell use; pipes / scripts can opt into tsv or json.
const DefaultFormat = FormatTable

// Cols is a small column-row container the renderer takes. Header
// names should be UPPERCASE for table mode (matches existing
// clawtool list output convention) and stay UPPERCASE for tsv too
// — JSON mode lower-cases them to produce idiomatic keys.
type Cols struct {
	Header []string
	Rows   [][]string
}

// Render writes cols to w in the requested format. Unknown format
// falls back to the table renderer with a stderr-quality warning
// — a typo in --format should still produce useful output, not a
// silent empty pipe.
func Render(w io.Writer, format Format, cols Cols) error {
	switch format {
	case FormatTSV:
		return renderTSV(w, cols)
	case FormatJSON:
		return renderJSON(w, cols)
	case FormatTable, "":
		return renderTable(w, cols)
	default:
		// Unknown format = degraded fallback with a hint
		// instead of silent empty output. Callers that want
		// strict validation should call ParseFormat first
		// and surface the typo themselves.
		fmt.Fprintf(w, "(unknown --format %q; rendering as table)\n", format)
		return renderTable(w, cols)
	}
}

// ParseFormat normalises a string into a known Format. Empty,
// unknown values, and the defaults all collapse to FormatTable.
// Callers that want to reject unknowns can compare against
// IsKnown() first.
func ParseFormat(s string) Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tsv":
		return FormatTSV
	case "json":
		return FormatJSON
	case "table", "":
		return FormatTable
	default:
		return FormatTable
	}
}

// IsKnown reports whether s parses to a Format other than the
// fallback. Useful when the caller wants to reject `--format
// xml` with a usage error rather than silently degrading.
func IsKnown(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "table", "tsv", "json", "":
		return true
	default:
		return false
	}
}

// ExtractFlag pulls `--format <value>` (or `--format=<value>`)
// out of argv and returns (format, residual argv, error). Empty
// argv → (DefaultFormat, argv, nil). Unknown value is preserved
// verbatim — the caller decides whether to error or degrade.
//
// Repeated `--format` is allowed; the last one wins (matches
// most CLI conventions where late flags override early ones).
func ExtractFlag(argv []string) (Format, []string, error) {
	out := make([]string, 0, len(argv))
	format := DefaultFormat
	i := 0
	for i < len(argv) {
		a := argv[i]
		switch {
		case a == "--format":
			if i+1 >= len(argv) {
				return format, argv, fmt.Errorf("--format requires a value (table | tsv | json)")
			}
			format = ParseFormat(argv[i+1])
			i += 2
		case strings.HasPrefix(a, "--format="):
			format = ParseFormat(strings.TrimPrefix(a, "--format="))
			i++
		default:
			out = append(out, a)
			i++
		}
	}
	return format, out, nil
}

// renderTable prints a header line + each row, padded so columns
// align. Width per column = max of header + every row cell. Same
// shape the existing CLI list commands hand-rolled, just lifted
// into a reusable spot.
func renderTable(w io.Writer, cols Cols) error {
	if len(cols.Header) == 0 {
		return nil
	}
	widths := make([]int, len(cols.Header))
	for i, h := range cols.Header {
		if len(h) > widths[i] {
			widths[i] = len(h)
		}
	}
	for _, row := range cols.Rows {
		for i := 0; i < len(cols.Header) && i < len(row); i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	writeRow := func(cells []string) {
		var b strings.Builder
		for i, c := range cells {
			if i >= len(widths) {
				break
			}
			if i == len(widths)-1 {
				b.WriteString(c) // last column: no trailing pad
			} else {
				b.WriteString(c)
				b.WriteString(strings.Repeat(" ", widths[i]-len(c)+2))
			}
		}
		b.WriteByte('\n')
		fmt.Fprint(w, b.String())
	}
	writeRow(cols.Header)
	for _, row := range cols.Rows {
		writeRow(row)
	}
	return nil
}

// renderTSV writes header + each row tab-separated, one row per
// line. Pipe-friendly: `clawtool bridge list --format tsv | awk
// '$2=="ready"{print $1}'` Just Works.
func renderTSV(w io.Writer, cols Cols) error {
	if _, err := fmt.Fprintln(w, strings.Join(cols.Header, "\t")); err != nil {
		return err
	}
	for _, row := range cols.Rows {
		if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// renderJSON writes an array of objects. Header names lower-cased
// for idiomatic JSON keys (FAMILY → family); rows shorter than
// the header get nil for missing tail cells; longer rows are
// truncated.
func renderJSON(w io.Writer, cols Cols) error {
	keys := make([]string, len(cols.Header))
	for i, h := range cols.Header {
		keys[i] = strings.ToLower(h)
	}
	out := make([]map[string]string, 0, len(cols.Rows))
	for _, row := range cols.Rows {
		obj := make(map[string]string, len(keys))
		for i, k := range keys {
			if i < len(row) {
				obj[k] = row[i]
			} else {
				obj[k] = ""
			}
		}
		out = append(out, obj)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
