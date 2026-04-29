package listfmt

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

var sample = Cols{
	Header: []string{"FAMILY", "STATUS", "DESCRIPTION"},
	Rows: [][]string{
		{"codex", "ready", "OpenAI Codex bridge"},
		{"opencode", "missing", "research-only adapter"},
	},
}

func TestRender_Table_PadsColumns(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatTable, sample); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "FAMILY") || !strings.Contains(out, "STATUS") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "codex") || !strings.Contains(out, "opencode") {
		t.Fatalf("rows missing: %q", out)
	}
	// Rough padding check: opencode (8 chars) is longer than codex
	// (5 chars) so the FAMILY column width should be ≥ 8 — a
	// "codex   ready" with multiple spaces between columns
	// suggests the padding worked.
	if !strings.Contains(out, "codex   ") {
		t.Errorf("padding looks off in: %q", out)
	}
}

func TestRender_TSV_OneRowPerLine(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatTSV, sample); err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 rows), got %d: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "\t") {
		t.Fatalf("header should be tab-separated: %q", lines[0])
	}
	cells := strings.Split(lines[1], "\t")
	if len(cells) != 3 || cells[0] != "codex" || cells[1] != "ready" {
		t.Fatalf("first row malformed: %v", cells)
	}
}

func TestRender_JSON_ArrayOfObjects(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, FormatJSON, sample); err != nil {
		t.Fatalf("Render: %v", err)
	}
	var out []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	if out[0]["family"] != "codex" || out[0]["status"] != "ready" {
		t.Fatalf("first row off: %+v", out[0])
	}
	// Header keys lower-cased for idiomatic JSON.
	if _, ok := out[0]["FAMILY"]; ok {
		t.Errorf("JSON keys should be lower-cased; got upper: %+v", out[0])
	}
}

func TestRender_UnknownFormatDegradesToTable(t *testing.T) {
	var buf bytes.Buffer
	_ = Render(&buf, Format("xml"), sample)
	out := buf.String()
	if !strings.Contains(out, "unknown --format") {
		t.Errorf("expected hint about unknown format: %q", out)
	}
	// Should still get the table content underneath.
	if !strings.Contains(out, "codex") {
		t.Errorf("table fallback missing rows: %q", out)
	}
}

func TestParseFormat_Normalisation(t *testing.T) {
	cases := map[string]Format{
		"":         FormatTable,
		"table":    FormatTable,
		"TSV":      FormatTSV,
		"  json  ": FormatJSON,
		"xml":      FormatTable, // unknown → fallback
	}
	for in, want := range cases {
		if got := ParseFormat(in); got != want {
			t.Errorf("ParseFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsKnown_OnlyAllowsKnown(t *testing.T) {
	for _, k := range []string{"table", "tsv", "json", ""} {
		if !IsKnown(k) {
			t.Errorf("%q should be known", k)
		}
	}
	for _, u := range []string{"xml", "yaml", "csv"} {
		if IsKnown(u) {
			t.Errorf("%q should NOT be known", u)
		}
	}
}

func TestExtractFlag_BothShapes(t *testing.T) {
	cases := []struct {
		in       []string
		want     Format
		residual []string
	}{
		{[]string{}, FormatTable, []string{}},
		{[]string{"--format", "tsv"}, FormatTSV, []string{}},
		{[]string{"--format=json"}, FormatJSON, []string{}},
		{[]string{"--format", "tsv", "extra"}, FormatTSV, []string{"extra"}},
		{[]string{"--format=table", "filter"}, FormatTable, []string{"filter"}},
		// Late one wins.
		{[]string{"--format", "tsv", "--format=json"}, FormatJSON, []string{}},
		// Unknown value parses to fallback.
		{[]string{"--format", "xml"}, FormatTable, []string{}},
	}
	for i, tc := range cases {
		got, residual, err := ExtractFlag(tc.in)
		if err != nil {
			t.Errorf("case %d: ExtractFlag err = %v", i, err)
			continue
		}
		if got != tc.want {
			t.Errorf("case %d: format = %q, want %q", i, got, tc.want)
		}
		if !sliceEq(residual, tc.residual) {
			t.Errorf("case %d: residual = %v, want %v", i, residual, tc.residual)
		}
	}
}

func TestExtractFlag_BareFlagWithoutValue(t *testing.T) {
	_, _, err := ExtractFlag([]string{"--format"})
	if err == nil {
		t.Errorf("expected error when --format has no value")
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
