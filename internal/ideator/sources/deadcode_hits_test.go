package sources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDeadcodeHits_ParsesOutput stubs the `deadcode` binary with a
// shell script that prints a small fixture report and confirms each
// surviving line becomes an Idea with the canonical priority,
// title prefix, evidence, and dedupe key.
func TestDeadcodeHits_ParsesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deadcode test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stubPath := filepath.Join(dir, "deadcode")
	stub := `#!/bin/sh
cat <<'EOF'
internal/foo/bar.go:42:6: unreachable func: Foo
internal/baz/qux.go:7:21: unreachable func: bazHelper
EOF
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	src := NewDeadcodeHits()
	src.Binary = stubPath
	// Drop the default skip fragments so the fixture paths flow
	// through; the filter behaviour gets its own test below.
	src.SkipPathFragments = nil

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 2 {
		t.Fatalf("Scan returned %d ideas, want 2", len(ideas))
	}
	first := ideas[0]
	if first.Title != "unreachable: Foo" {
		t.Fatalf("Title = %q, want %q", first.Title, "unreachable: Foo")
	}
	if first.SuggestedPriority != 3 {
		t.Fatalf("SuggestedPriority = %d, want 3", first.SuggestedPriority)
	}
	if first.Evidence != "internal/foo/bar.go:42" {
		t.Fatalf("Evidence = %q, want %q", first.Evidence, "internal/foo/bar.go:42")
	}
	if !strings.Contains(first.SuggestedPrompt, "delete or wire up") {
		t.Fatalf("SuggestedPrompt = %q", first.SuggestedPrompt)
	}
	if !strings.HasPrefix(first.DedupeKey, "deadcode_hits:") {
		t.Fatalf("DedupeKey = %q", first.DedupeKey)
	}
	if ideas[1].Title != "unreachable: bazHelper" {
		t.Fatalf("ideas[1].Title = %q", ideas[1].Title)
	}
}

// TestDeadcodeHits_SkipsTestFiles confirms `_test.go`, `*_gen.go`,
// and the default path-fragment filters drop findings before they
// reach the orchestrator.
func TestDeadcodeHits_SkipsTestFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub deadcode test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stubPath := filepath.Join(dir, "deadcode")
	stub := `#!/bin/sh
cat <<'EOF'
internal/foo/bar_test.go:11:6: unreachable func: testFixture
internal/foo/baz_gen.go:5:6: unreachable func: GeneratedHelper
internal/mcpgen/go_adapter.go:99:6: unreachable func: scaffoldFn
internal/checkpoint/resolve.go:67:6: unreachable func: Resolve
internal/foo/real.go:42:6: unreachable func: realDead
EOF
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	src := NewDeadcodeHits()
	src.Binary = stubPath

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 1: %v", len(ideas), titles)
	}
	if ideas[0].Title != "unreachable: realDead" {
		t.Fatalf("surviving idea = %q, want unreachable: realDead", ideas[0].Title)
	}
}

// TestDeadcodeHits_BinaryNotFoundIsNoOp confirms a missing
// `deadcode` binary returns an empty slice + nil error
// (cheap-on-fail) rather than poisoning the orchestrator pass.
func TestDeadcodeHits_BinaryNotFoundIsNoOp(t *testing.T) {
	src := NewDeadcodeHits()
	src.Binary = "/nonexistent/path/to/deadcode-binary-that-cannot-exist"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}
