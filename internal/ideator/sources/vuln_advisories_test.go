package sources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVulnAdvisories_StubGovulncheck writes a tiny shell stub that
// emits a govulncheck-shaped JSON stream and verifies the source
// joins findings against advisory metadata + dedupes by (osv, module).
func TestVulnAdvisories_StubGovulncheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub govulncheck test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "govulncheck")
	// Two findings on the same (osv, module) collapse to one Idea.
	// One finding on a third-party module gets priority 6, stdlib
	// finding gets priority 8.
	body := `#!/bin/sh
cat <<'JSON'
{"config":{"protocol_version":"v1.0.0"}}
{"osv":{"id":"GO-2026-4599","aliases":["CVE-2026-27137"],"summary":"Incorrect enforcement of email constraints in crypto/x509"}}
{"osv":{"id":"GO-2025-0001","aliases":["CVE-2025-1111"],"summary":"Path traversal in golang.org/x/net"}}
{"finding":{"osv":"GO-2026-4599","fixed_version":"v1.26.1","trace":[{"module":"stdlib","version":"v1.26.0"}]}}
{"finding":{"osv":"GO-2026-4599","fixed_version":"v1.26.1","trace":[{"module":"stdlib","version":"v1.26.0"}]}}
{"finding":{"osv":"GO-2025-0001","fixed_version":"v0.53.0","trace":[{"module":"golang.org/x/net","version":"v0.52.0"}]}}
JSON
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	src := NewVulnAdvisories()
	src.Binary = stub
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 2 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 2 (stdlib finding deduped): %v", len(ideas), titles)
	}
	// Stdlib idea sorts first (priority 8 > 6).
	if !strings.Contains(ideas[0].Title, "GO-2026-4599") {
		t.Errorf("ideas[0].Title = %q, want contains GO-2026-4599", ideas[0].Title)
	}
	if ideas[0].SuggestedPriority != 8 {
		t.Errorf("stdlib idea priority = %d, want 8", ideas[0].SuggestedPriority)
	}
	if !strings.Contains(ideas[0].SuggestedPrompt, "CVE-2026-27137") {
		t.Errorf("stdlib prompt missing CVE alias: %q", ideas[0].SuggestedPrompt)
	}
	if ideas[1].SuggestedPriority != 6 {
		t.Errorf("third-party idea priority = %d, want 6", ideas[1].SuggestedPriority)
	}
	if !strings.HasPrefix(ideas[0].DedupeKey, "vuln_advisories:GO-2026-4599:stdlib") {
		t.Errorf("DedupeKey = %q, want prefix vuln_advisories:GO-2026-4599:stdlib", ideas[0].DedupeKey)
	}
}

// TestVulnAdvisories_MissingBinaryIsNoOp confirms a missing
// govulncheck binary returns no ideas + no error (cheap-on-fail).
func TestVulnAdvisories_MissingBinaryIsNoOp(t *testing.T) {
	src := NewVulnAdvisories()
	src.Binary = "/nonexistent/path/to/govulncheck-that-cannot-exist"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestVulnAdvisories_DropsStdlibFixedByWorkflowPin proves that when
// the workflow GO_VERSION is already at-or-past the advisory's
// fixed_version, the source drops it. This is the primary fix for
// the "local Go is older than CI's pin → 12 ghost vulns" loop.
func TestVulnAdvisories_DropsStdlibFixedByWorkflowPin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()

	// Workflow pin = 1.26.2. Two advisories: one fixed in 1.26.1
	// (covered → dropped), one fixed in 1.27.0 (not covered → kept).
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wf := `name: CI
on: push
env:
  GO_VERSION: "1.26.2"
jobs:
  test:
    runs-on: ubuntu-latest
`
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(wf), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	stub := filepath.Join(dir, "govulncheck")
	body := `#!/bin/sh
cat <<'JSON'
{"osv":{"id":"GO-2026-COVERED","summary":"covered by pin"}}
{"osv":{"id":"GO-2026-UNCOVERED","summary":"future fix not yet pinned"}}
{"finding":{"osv":"GO-2026-COVERED","fixed_version":"v1.26.1","trace":[{"module":"stdlib","version":"v1.26.0"}]}}
{"finding":{"osv":"GO-2026-UNCOVERED","fixed_version":"v1.27.0","trace":[{"module":"stdlib","version":"v1.26.0"}]}}
JSON
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	src := NewVulnAdvisories()
	src.Binary = stub
	// repoRoot=dir means readWorkflowGoVersion finds dir/.github/workflows/ci.yml.
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 1 (covered dropped, uncovered kept): %v", len(ideas), titles)
	}
	if !strings.Contains(ideas[0].Title, "GO-2026-UNCOVERED") {
		t.Errorf("surviving idea = %q, want contains GO-2026-UNCOVERED", ideas[0].Title)
	}
}

// TestStdlibFixedByPin covers the version-comparison helper directly.
func TestStdlibFixedByPin(t *testing.T) {
	cases := []struct {
		fixed, pin string
		want       bool
	}{
		{"v1.26.1", "1.26.2", true},  // pin newer
		{"v1.26.2", "1.26.2", true},  // equal
		{"v1.27.0", "1.26.2", false}, // pin older
		{"v1.26.0", "1.26", true},    // pin missing patch (treated as .0+)
		{"v2.0.0", "1.99.0", false},  // major bump
		{"go1.26.1", "1.26.2", true}, // alt prefix
		{"", "1.26.2", false},        // unparseable fixed → fail-open
		{"v1.26.1", "", false},       // empty pin → fail-open (caller skips)
	}
	for _, tc := range cases {
		got := stdlibFixedByPin(tc.fixed, tc.pin)
		if got != tc.want {
			t.Errorf("stdlibFixedByPin(%q, %q) = %v, want %v", tc.fixed, tc.pin, got, tc.want)
		}
	}
}

// TestVulnAdvisories_SkipsFindingsWithoutModule ensures findings
// with empty traces are skipped rather than emitting an Idea with
// blank module evidence.
func TestVulnAdvisories_SkipsFindingsWithoutModule(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "govulncheck")
	body := `#!/bin/sh
cat <<'JSON'
{"osv":{"id":"GO-2026-9999","summary":"empty trace test"}}
{"finding":{"osv":"GO-2026-9999","fixed_version":"v1.0.0","trace":[]}}
JSON
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	src := NewVulnAdvisories()
	src.Binary = stub
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0 (empty-trace finding dropped)", len(ideas))
	}
}
