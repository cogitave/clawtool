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

// TestRenderOrHint_TableEmptyEmitsHint pins the table-mode
// branch: no rows + format=table → write hint + newline, skip
// Render. This keeps the actionable next-step in front of an
// interactive operator who runs `clawtool * list` on a fresh
// box.
func TestRenderOrHint_TableEmptyEmitsHint(t *testing.T) {
	var buf bytes.Buffer
	cols := Cols{Header: []string{"NAME", "STATUS"}}
	if err := RenderOrHint(&buf, FormatTable, cols, "(no items configured)"); err != nil {
		t.Fatalf("RenderOrHint: %v", err)
	}
	out := buf.String()
	if out != "(no items configured)\n" {
		t.Errorf("table-empty output = %q, want %q", out, "(no items configured)\n")
	}
	// Specifically: the header should NOT have been rendered.
	if strings.Contains(out, "NAME") {
		t.Errorf("table-empty should suppress header; got %q", out)
	}
}

// TestRenderOrHint_JSONEmptyRoutesRender pins the JSON branch:
// no rows + format=json → delegate to Render → emit `[]\n`. The
// human hint must not leak into the byte stream.
func TestRenderOrHint_JSONEmptyRoutesRender(t *testing.T) {
	var buf bytes.Buffer
	cols := Cols{Header: []string{"NAME", "STATUS"}}
	if err := RenderOrHint(&buf, FormatJSON, cols, "(no items configured)"); err != nil {
		t.Fatalf("RenderOrHint: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body != "[]" {
		t.Errorf("json-empty output = %q, want %q", body, "[]")
	}
	if strings.Contains(buf.String(), "no items configured") {
		t.Error("hint should not leak into JSON stream")
	}
}

// TestRenderOrHint_TSVEmptyRoutesRender pins the TSV branch: no
// rows + format=tsv → delegate to Render → emit a single
// header line, no human banner. Pipe consumers can `awk
// 'NR>1{...}'` cleanly.
func TestRenderOrHint_TSVEmptyRoutesRender(t *testing.T) {
	var buf bytes.Buffer
	cols := Cols{Header: []string{"NAME", "STATUS"}}
	if err := RenderOrHint(&buf, FormatTSV, cols, "(no items configured)"); err != nil {
		t.Fatalf("RenderOrHint: %v", err)
	}
	body := strings.TrimRight(buf.String(), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 header line; got %d: %q", len(lines), body)
	}
	if lines[0] != "NAME\tSTATUS" {
		t.Errorf("tsv header = %q, want %q", lines[0], "NAME\tSTATUS")
	}
	if strings.Contains(buf.String(), "no items configured") {
		t.Error("hint should not leak into TSV stream")
	}
}

// TestRenderOrHint_NonEmptyDelegates verifies that when rows
// are present the hint is ignored (regardless of format) and
// Render is called with the populated cols. Sanity check so a
// future bug where the hint accidentally prints over real data
// trips the test.
func TestRenderOrHint_NonEmptyDelegates(t *testing.T) {
	for _, format := range []Format{FormatTable, FormatTSV, FormatJSON} {
		var buf bytes.Buffer
		if err := RenderOrHint(&buf, format, sample, "should-not-appear"); err != nil {
			t.Fatalf("format=%s: %v", format, err)
		}
		out := buf.String()
		if strings.Contains(out, "should-not-appear") {
			t.Errorf("format=%s leaked hint into populated stream: %q", format, out)
		}
		if !strings.Contains(out, "codex") {
			t.Errorf("format=%s missing data row: %q", format, out)
		}
	}
}

// TestRenderOrHint_MultilineHint confirms the hint can carry
// embedded newlines for two-line pointers (skill list ships
// "no skills" + "try `skill new`"). The trailing newline is
// added by RenderOrHint, not by the caller.
func TestRenderOrHint_MultilineHint(t *testing.T) {
	var buf bytes.Buffer
	cols := Cols{Header: []string{"X"}}
	hint := "line one\nline two"
	if err := RenderOrHint(&buf, FormatTable, cols, hint); err != nil {
		t.Fatalf("RenderOrHint: %v", err)
	}
	if buf.String() != "line one\nline two\n" {
		t.Errorf("multiline hint = %q, want %q", buf.String(), "line one\nline two\n")
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
