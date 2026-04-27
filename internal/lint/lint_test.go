package lint

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLint_SkipsUnsupportedExtension(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.unknown")
	if err := os.WriteFile(path, []byte("anything"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := r.Lint(context.Background(), path)
	if err != nil {
		t.Fatalf("unsupported extension should return nil/nil; got err=%v", err)
	}
	if findings != nil {
		t.Errorf("unsupported extension should yield zero findings; got %d", len(findings))
	}
}

func TestLint_GracefulSkipWhenLinterAbsent(t *testing.T) {
	// Force PATH to a tempdir so no linter binary is reachable.
	old := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", old) })
	os.Setenv("PATH", t.TempDir())

	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := r.Lint(context.Background(), path)
	if err != nil {
		t.Errorf("missing linter binary should be a graceful skip, not an error; got %v", err)
	}
	if findings != nil {
		t.Errorf("missing linter should yield nil findings; got %v", findings)
	}
}

func TestLint_RoutesByExtension(t *testing.T) {
	// White-box test: hit the runner's internal extension map. We
	// don't run the actual linter (binary may be absent in CI); we
	// just verify the routing matches.
	r := New().(*runner)
	cases := map[string]string{
		".go":      "golangci-lint",
		".js":      "eslint",
		".jsx":     "eslint",
		".ts":      "eslint",
		".tsx":     "eslint",
		".mjs":     "eslint",
		".cjs":     "eslint",
		".py":      "ruff",
		".unknown": "",
	}
	for ext, wantTool := range cases {
		got := r.byExt[ext]
		if wantTool == "" {
			if got != nil {
				t.Errorf("ext %q: expected nil adapter; got tool=%s", ext, got.tool)
			}
			continue
		}
		if got == nil {
			t.Errorf("ext %q: expected adapter %q; got nil", ext, wantTool)
			continue
		}
		if got.tool != wantTool {
			t.Errorf("ext %q: tool=%s, want %s", ext, got.tool, wantTool)
		}
	}
}

func TestParseGolangciLint_Valid(t *testing.T) {
	a := adapterGolangciLint()
	out := []byte(`{"Issues":[{"FromLinter":"errcheck","Text":"unchecked error","Severity":"error","Pos":{"Filename":"x.go","Line":42,"Column":3}}]}`)
	findings, err := a.parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	f := findings[0]
	if f.LineNumber != 42 || f.Column != 3 || f.Severity != "error" || f.Message != "unchecked error" {
		t.Errorf("parse mismatch: %+v", f)
	}
}

func TestParseGolangciLint_Empty(t *testing.T) {
	a := adapterGolangciLint()
	findings, err := a.parse(nil)
	if err != nil {
		t.Errorf("empty output should parse cleanly; got %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("empty output should yield 0 findings; got %d", len(findings))
	}
}

func TestParseESLint_Valid(t *testing.T) {
	a := adapterESLint()
	out := []byte(`[{"filePath":"x.js","messages":[{"line":3,"column":1,"severity":2,"message":"missing semi"}]}]`)
	findings, err := a.parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Severity != "error" || findings[0].Message != "missing semi" {
		t.Errorf("eslint parse mismatch: %+v", findings)
	}
}

func TestParseRuff_Valid(t *testing.T) {
	a := adapterRuff()
	out := []byte(`[{"code":"E501","message":"line too long","location":{"row":7,"column":80}}]`)
	findings, err := a.parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].LineNumber != 7 || findings[0].Column != 80 {
		t.Errorf("ruff parse mismatch: %+v", findings)
	}
	if findings[0].Message != "E501: line too long" {
		t.Errorf("ruff should prefix code: got %q", findings[0].Message)
	}
}

func TestIsEnabled_DefaultOn(t *testing.T) {
	if !IsEnabled(nil) {
		t.Error("IsEnabled(nil) should default to true")
	}
	on := true
	if !IsEnabled(&on) {
		t.Error("IsEnabled(&true) should be true")
	}
	off := false
	if IsEnabled(&off) {
		t.Error("IsEnabled(&false) should be false")
	}
}

func TestDisabledRunner_AlwaysEmpty(t *testing.T) {
	r := Disabled()
	findings, err := r.Lint(context.Background(), "anything.go")
	if err != nil {
		t.Errorf("disabled runner should never error; got %v", err)
	}
	if findings != nil {
		t.Errorf("disabled runner should never return findings; got %v", findings)
	}
}
