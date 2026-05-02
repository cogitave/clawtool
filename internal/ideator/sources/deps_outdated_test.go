package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureGoListJSON is the canonical `go list -m -u -json all`
// stream shape: concatenated JSON objects (NOT a JSON array). This
// fixture mixes a main module, a patch update, a major update, the
// auto-managed toolchain pseudo-module, and a module without an
// available update so every code path is exercised.
const fixtureGoListJSON = `{
	"Path": "github.com/cogitave/clawtool",
	"Main": true,
	"Version": "v0.0.0"
}
{
	"Path": "github.com/foo/bar",
	"Version": "v1.2.3",
	"Update": {
		"Path": "github.com/foo/bar",
		"Version": "v1.2.4"
	}
}
{
	"Path": "github.com/baz/qux",
	"Version": "v1.0.0",
	"Update": {
		"Path": "github.com/baz/qux",
		"Version": "v2.0.0"
	}
}
{
	"Path": "golang.org/toolchain",
	"Version": "v0.0.1-go1.26.0.linux-amd64",
	"Update": {
		"Path": "golang.org/toolchain",
		"Version": "v0.0.1-go1.27.0.linux-amd64"
	}
}
{
	"Path": "github.com/already/current",
	"Version": "v3.1.0"
}
`

// writeRepoWithGoMod creates a tmp dir with a minimal go.mod that
// references the fixture modules, so buildGoModLineLookup has
// something to find.
func writeRepoWithGoMod(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	goMod := `module example.com/test

go 1.26

require (
	github.com/foo/bar v1.2.3
	github.com/baz/qux v1.0.0
	github.com/already/current v3.1.0
)
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// stubRunGoList returns a runGoList replacement that always yields
// the supplied body + nil error.
func stubRunGoList(body string) func(context.Context, string, string, time.Duration) ([]byte, error) {
	return func(_ context.Context, _ string, _ string, _ time.Duration) ([]byte, error) {
		return []byte(body), nil
	}
}

// TestDepsOutdated_ParsesGoListOutput stubs runGoList with the
// canonical fixture and asserts the source emits one Idea per
// updatable, non-toolchain, non-main module.
func TestDepsOutdated_ParsesGoListOutput(t *testing.T) {
	dir := writeRepoWithGoMod(t)
	src := NewDepsOutdated()
	src.runGoList = stubRunGoList(fixtureGoListJSON)

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 2 {
		t.Fatalf("Scan returned %d ideas, want 2 (foo/bar patch + baz/qux major)", len(ideas))
	}

	// Index by module path so the test doesn't depend on iteration
	// order (Scan preserves the JSON-stream order today, but the
	// orchestrator may sort later).
	byPath := map[string]struct {
		title    string
		priority int
		evidence string
		prompt   string
	}{}
	for _, idea := range ideas {
		// Title shape is "Bump <path> from <old> to <new>".
		fields := strings.Fields(idea.Title)
		if len(fields) < 6 || fields[0] != "Bump" {
			t.Fatalf("unexpected Title %q", idea.Title)
		}
		path := fields[1]
		byPath[path] = struct {
			title    string
			priority int
			evidence string
			prompt   string
		}{idea.Title, idea.SuggestedPriority, idea.Evidence, idea.SuggestedPrompt}
		if idea.DedupeKey == "" {
			t.Fatalf("DedupeKey empty for %s", path)
		}
		if !strings.HasPrefix(idea.DedupeKey, "deps_outdated:") {
			t.Fatalf("DedupeKey wrong prefix: %q", idea.DedupeKey)
		}
	}

	patch, ok := byPath["github.com/foo/bar"]
	if !ok {
		t.Fatalf("no idea emitted for github.com/foo/bar")
	}
	if patch.priority != 4 {
		t.Fatalf("foo/bar priority %d, want 4 (patch bump)", patch.priority)
	}
	if !strings.Contains(patch.title, "v1.2.3 to v1.2.4") {
		t.Fatalf("foo/bar title missing version transition: %q", patch.title)
	}
	if !strings.HasPrefix(patch.evidence, "go.mod:") {
		t.Fatalf("foo/bar evidence not anchored to go.mod: %q", patch.evidence)
	}

	major, ok := byPath["github.com/baz/qux"]
	if !ok {
		t.Fatalf("no idea emitted for github.com/baz/qux")
	}
	if major.priority != 2 {
		t.Fatalf("baz/qux priority %d, want 2 (major bump)", major.priority)
	}
	if !strings.Contains(major.prompt, "MAJOR") {
		t.Fatalf("baz/qux prompt missing MAJOR banner: %q", major.prompt)
	}
}

// TestDepsOutdated_SkipsToolchainModule verifies the
// golang.org/toolchain pseudo-module is filtered.
func TestDepsOutdated_SkipsToolchainModule(t *testing.T) {
	dir := writeRepoWithGoMod(t)
	src := NewDepsOutdated()
	src.runGoList = stubRunGoList(fixtureGoListJSON)

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, idea := range ideas {
		if strings.Contains(idea.Title, "golang.org/toolchain") {
			t.Fatalf("toolchain module leaked into ideas: %q", idea.Title)
		}
		if strings.Contains(idea.SuggestedPrompt, "golang.org/toolchain") {
			t.Fatalf("toolchain module leaked into prompt: %q", idea.SuggestedPrompt)
		}
	}
}

// TestDepsOutdated_MajorBumpLowerPriority asserts that crossing a
// major version line drops priority to 2 and adds a "major version
// bump" note, while patch / minor stays at 4.
func TestDepsOutdated_MajorBumpLowerPriority(t *testing.T) {
	dir := writeRepoWithGoMod(t)
	src := NewDepsOutdated()
	src.runGoList = stubRunGoList(fixtureGoListJSON)

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	var sawMajor, sawPatch bool
	for _, idea := range ideas {
		switch {
		case strings.Contains(idea.Title, "github.com/baz/qux"):
			sawMajor = true
			if idea.SuggestedPriority != 2 {
				t.Fatalf("major bump priority %d, want 2", idea.SuggestedPriority)
			}
			if !strings.Contains(idea.Summary, "major version bump") {
				t.Fatalf("major bump summary missing note: %q", idea.Summary)
			}
		case strings.Contains(idea.Title, "github.com/foo/bar"):
			sawPatch = true
			if idea.SuggestedPriority != 4 {
				t.Fatalf("patch bump priority %d, want 4", idea.SuggestedPriority)
			}
			if strings.Contains(idea.Summary, "major version bump") {
				t.Fatalf("patch bump summary should not flag major: %q", idea.Summary)
			}
		}
	}
	if !sawMajor {
		t.Fatalf("expected major-bump idea for baz/qux")
	}
	if !sawPatch {
		t.Fatalf("expected patch-bump idea for foo/bar")
	}
}

// TestDepsOutdated_SkipsIndirectDeps verifies that modules flagged
// `Indirect: true` by `go list -m -u -json all` are filtered out.
// `go list` marks a module Indirect when it's pulled in by a
// transitive dep rather than declared as a direct require in the
// main go.mod. Bumping such a module via `go get <path>@<ver>`
// would only land in go.sum (or rewrite go.mod's `// indirect`
// line, which `go mod tidy` immediately reverts), so surfacing it
// as an Idea is un-actionable noise — especially right after a
// bulk-update batch where direct deps are already current.
func TestDepsOutdated_SkipsIndirectDeps(t *testing.T) {
	const fixture = `{
		"Path": "example.com/test",
		"Main": true,
		"Version": "v0.0.0"
	}
	{
		"Path": "github.com/direct/dep",
		"Version": "v1.0.0",
		"Indirect": false,
		"Update": {
			"Path": "github.com/direct/dep",
			"Version": "v1.1.0"
		}
	}
	{
		"Path": "github.com/indirect/dep",
		"Version": "v2.0.0",
		"Indirect": true,
		"Update": {
			"Path": "github.com/indirect/dep",
			"Version": "v2.1.0"
		}
	}
	`
	dir := writeRepoWithGoMod(t)
	src := NewDepsOutdated()
	src.runGoList = stubRunGoList(fixture)

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		t.Fatalf("Scan returned %d ideas, want 1 (only the direct dep)", len(ideas))
	}
	if !strings.Contains(ideas[0].Title, "github.com/direct/dep") {
		t.Fatalf("expected idea for direct dep, got: %q", ideas[0].Title)
	}
	for _, idea := range ideas {
		if strings.Contains(idea.Title, "github.com/indirect/dep") {
			t.Fatalf("indirect module leaked into ideas: %q", idea.Title)
		}
		if strings.Contains(idea.SuggestedPrompt, "github.com/indirect/dep") {
			t.Fatalf("indirect module leaked into prompt: %q", idea.SuggestedPrompt)
		}
	}
}

// TestDepsOutdated_NoUpdatesEmpty asserts the source returns zero
// ideas when every module is already current (no Update field on
// any record).
func TestDepsOutdated_NoUpdatesEmpty(t *testing.T) {
	const allCurrent = `{
		"Path": "github.com/cogitave/clawtool",
		"Main": true,
		"Version": "v0.0.0"
	}
	{
		"Path": "github.com/foo/bar",
		"Version": "v1.2.3"
	}
	{
		"Path": "github.com/baz/qux",
		"Version": "v1.0.0"
	}
	`
	dir := writeRepoWithGoMod(t)
	src := NewDepsOutdated()
	src.runGoList = stubRunGoList(allCurrent)

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}
