package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSourceCatalog_PrintsAllEntries(t *testing.T) {
	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}}

	rc := app.runSourceCatalog(nil)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	got := out.String()

	// All 18 catalog entries should appear by name. We assert a
	// representative sample so a single rename here doesn't force
	// constant test churn — but we DO require the leading count
	// and the section structure.
	for _, want := range []string{
		"catalog entries",
		"[anthropic]",
		"[community]",
		"github",
		"context7",
		"playwright",
		"install: clawtool source add",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("catalog output missing %q\n---\n%s", want, got)
		}
	}
}

func TestSourceCatalog_DispatchedViaSource(t *testing.T) {
	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}}

	// Both spellings should reach runSourceCatalog.
	for _, verb := range []string{"catalog", "available"} {
		out.Reset()
		rc := app.runSource([]string{verb})
		if rc != 0 {
			t.Errorf("runSource %q rc = %d", verb, rc)
		}
		if !strings.Contains(out.String(), "catalog entries") {
			t.Errorf("runSource %q didn't print catalog: %q", verb, out.String())
		}
	}
}
