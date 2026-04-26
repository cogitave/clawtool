package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRead_TextWholeFile(t *testing.T) {
	dir := t.TempDir()
	body := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	path := writeFile(t, dir, "f.txt", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "text" {
		t.Errorf("format = %q, want text", res.Format)
	}
	if res.Engine != "stdlib" {
		t.Errorf("engine = %q, want stdlib", res.Engine)
	}
	if res.TotalLines != 5 {
		t.Errorf("total_lines = %d, want 5", res.TotalLines)
	}
	if res.LineEnd != 5 {
		t.Errorf("line_end = %d, want 5 (EOF default)", res.LineEnd)
	}
	want := "alpha\nbeta\ngamma\ndelta\nepsilon"
	if res.Content != want {
		t.Errorf("content = %q, want %q", res.Content, want)
	}
}

func TestRead_TextLineRange(t *testing.T) {
	dir := t.TempDir()
	body := "L1\nL2\nL3\nL4\nL5\n"
	path := writeFile(t, dir, "f.txt", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 2, 4, "")

	if res.TotalLines != 5 {
		t.Errorf("total_lines = %d, want 5", res.TotalLines)
	}
	if res.LineStart != 2 || res.LineEnd != 4 {
		t.Errorf("line_start/end = %d/%d, want 2/4", res.LineStart, res.LineEnd)
	}
	want := "L2\nL3\nL4"
	if res.Content != want {
		t.Errorf("content = %q, want %q", res.Content, want)
	}
	if !res.Truncated {
		t.Errorf("truncated should be true (asked range stopped before EOF)")
	}
}

func TestRead_TextLineStartBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	body := "only\nthree\nlines\n"
	path := writeFile(t, dir, "f.txt", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 100, 200, "")

	if res.TotalLines != 3 {
		t.Errorf("total_lines = %d, want 3", res.TotalLines)
	}
	if res.Content != "" {
		t.Errorf("content = %q, want empty when range is past EOF", res.Content)
	}
}

func TestRead_BinaryRejected(t *testing.T) {
	dir := t.TempDir()
	bin := []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01, 0x02, 0x03}
	path := writeFile(t, dir, "binary.bin", string(bin))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "binary-rejected" {
		t.Errorf("format = %q, want binary-rejected", res.Format)
	}
	if res.ErrorReason == "" {
		t.Errorf("error_reason should explain the rejection")
	}
}

func TestRead_Ipynb(t *testing.T) {
	dir := t.TempDir()
	nb := `{
		"cells": [
			{"cell_type": "markdown", "source": ["# Title\n", "intro paragraph\n"]},
			{"cell_type": "code",     "source": "print('hello')\n"}
		]
	}`
	path := writeFile(t, dir, "demo.ipynb", nb)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "ipynb" {
		t.Errorf("format = %q, want ipynb", res.Format)
	}
	if res.Engine != "ipynb-json" {
		t.Errorf("engine = %q, want ipynb-json", res.Engine)
	}
	if !strings.Contains(res.Content, "# --- cell 1 (markdown) ---") {
		t.Errorf("missing cell-1 marker:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "# --- cell 2 (code) ---") {
		t.Errorf("missing cell-2 marker:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "print('hello')") {
		t.Errorf("missing code-cell body:\n%s", res.Content)
	}
}

func TestRead_PDFWithoutEngine(t *testing.T) {
	if LookupEngine("pdftotext").Bin != "" {
		t.Skip("pdftotext is installed on this system; skipping the absent-engine test")
	}
	dir := t.TempDir()
	body := "%PDF-1.4\n... not a real pdf, only the header ...\n"
	path := writeFile(t, dir, "doc.pdf", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "pdf" {
		t.Errorf("format = %q, want pdf", res.Format)
	}
	if res.Engine != "pdftotext" {
		t.Errorf("engine = %q, want pdftotext (selected even when absent)", res.Engine)
	}
	if !strings.Contains(strings.ToLower(res.ErrorReason), "pdftotext") {
		t.Errorf("error_reason should mention pdftotext: %q", res.ErrorReason)
	}
}

func TestRead_DirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, dir, 1, 0, "")
	if res.ErrorReason == "" || !strings.Contains(res.ErrorReason, "directory") {
		t.Errorf("expected directory rejection, got %q", res.ErrorReason)
	}
}
