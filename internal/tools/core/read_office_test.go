package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// TestRead_Xlsx builds an in-memory workbook with two sheets via excelize
// itself, persists it, then reads it through clawtool. Verifies the
// preferred engine picks up the file by extension and renders rows in
// TSV form with a sheets-list metadata field.
func TestRead_Xlsx(t *testing.T) {
	dir := t.TempDir()
	xlsxPath := filepath.Join(dir, "demo.xlsx")

	f := excelize.NewFile()
	if _, err := f.NewSheet("Numbers"); err != nil {
		t.Fatal(err)
	}
	mustSet := func(sheet, cell, val string) {
		t.Helper()
		if err := f.SetCellValue(sheet, cell, val); err != nil {
			t.Fatal(err)
		}
	}
	// Default Sheet1 is the first sheet. Add header + 2 rows.
	mustSet("Sheet1", "A1", "name")
	mustSet("Sheet1", "B1", "city")
	mustSet("Sheet1", "A2", "alpha")
	mustSet("Sheet1", "B2", "Istanbul")
	mustSet("Sheet1", "A3", "bravo")
	mustSet("Sheet1", "B3", "Berlin")

	mustSet("Numbers", "A1", "n")
	mustSet("Numbers", "A2", "1")
	mustSet("Numbers", "A3", "2")

	if err := f.SaveAs(xlsxPath); err != nil {
		t.Fatalf("save xlsx: %v", err)
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Default sheet.
	res := executeRead(ctx, xlsxPath, 1, 0, "")
	if res.Format != "xlsx" {
		t.Errorf("format = %q, want xlsx", res.Format)
	}
	if res.Engine != "excelize" {
		t.Errorf("engine = %q, want excelize", res.Engine)
	}
	if len(res.Sheets) != 2 {
		t.Errorf("sheets = %v, want 2 entries", res.Sheets)
	}
	if !strings.Contains(res.Content, "alpha\tIstanbul") {
		t.Errorf("expected TSV row 'alpha\\tIstanbul' in content:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "# sheet: Sheet1") {
		t.Errorf("expected sheet header in content:\n%s", res.Content)
	}

	// Named sheet.
	res2 := executeRead(ctx, xlsxPath, 1, 0, "Numbers")
	if !strings.Contains(res2.Content, "# sheet: Numbers") {
		t.Errorf("expected Numbers sheet header:\n%s", res2.Content)
	}
	if !strings.Contains(res2.Content, "n\n1\n2") {
		t.Errorf("Numbers sheet content unexpected:\n%s", res2.Content)
	}

	// Unknown sheet → error.
	res3 := executeRead(ctx, xlsxPath, 1, 0, "Ghost")
	if res3.ErrorReason == "" {
		t.Error("expected error for unknown sheet")
	}
	if !strings.Contains(res3.ErrorReason, "Ghost") {
		t.Errorf("error should name the missing sheet, got: %q", res3.ErrorReason)
	}
}

// TestRead_DocxWithoutEngine mirrors the PDF absent-engine path: when
// pandoc is not on PATH, clawtool surfaces a structured error pointing at
// install instructions rather than failing opaquely.
func TestRead_DocxWithoutEngine(t *testing.T) {
	if LookupEngine("pandoc").Bin != "" {
		t.Skip("pandoc is installed; skipping absent-engine test")
	}
	dir := t.TempDir()
	// Extension-only detection — content can be anything; we never reach
	// pandoc when its binary is missing.
	path := writeFile(t, dir, "demo.docx", "PK\x03\x04 not a real docx")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "docx" {
		t.Errorf("format = %q, want docx", res.Format)
	}
	if res.Engine != "pandoc" {
		t.Errorf("engine = %q, want pandoc", res.Engine)
	}
	if !strings.Contains(strings.ToLower(res.ErrorReason), "pandoc") {
		t.Errorf("error_reason should mention pandoc: %q", res.ErrorReason)
	}
}
