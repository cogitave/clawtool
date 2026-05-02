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
