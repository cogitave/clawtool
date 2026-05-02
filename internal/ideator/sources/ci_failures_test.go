package sources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCIFailures_StubGH writes a tiny shell script that mimics
// `gh run list` and confirms the source converts the JSON into
// ideas.
func TestCIFailures_StubGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub gh test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()
	stubPath := filepath.Join(dir, "gh")
	stub := `#!/bin/sh
cat <<'JSON'
[
  {"databaseId": 9001, "name": "ci.yml", "headSha": "abcdef0123456789abcdef0123456789abcdef01", "conclusion": "failure", "event": "push", "createdAt": "2026-04-30T12:00:00Z"},
  {"databaseId": 9002, "name": "release.yml", "headSha": "1234567890abcdef1234567890abcdef12345678", "conclusion": "failure", "event": "push", "createdAt": "2026-04-30T13:00:00Z"}
]
JSON
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	src := NewCIFailures()
	src.GHBinary = stubPath
	// The legacy stub returns the same JSON for every gh invocation
	// (failures + greens), and there's no real git repo at `dir`.
	// Disable the new superseded-run probes so this test only
	// exercises the original parse path.
	src.MaxCommitsBehind = 0
	src.SkipSupersededByGreen = false
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 2 {
		t.Fatalf("Scan returned %d ideas, want 2", len(ideas))
	}
	for _, idea := range ideas {
		if !strings.HasPrefix(idea.Title, "CI failure:") {
			t.Fatalf("Title: %q", idea.Title)
		}
		if idea.SuggestedPriority < 5 {
			t.Fatalf("priority too low: %d", idea.SuggestedPriority)
		}
		if idea.DedupeKey == "" {
			t.Fatalf("DedupeKey empty")
		}
	}
}

// TestCIFailures_MissingBinaryIsNoOp confirms a missing gh binary
// returns no ideas + no error (cheap-on-fail).
func TestCIFailures_MissingBinaryIsNoOp(t *testing.T) {
	src := NewCIFailures()
	src.GHBinary = "/nonexistent/path/to/gh-binary-that-cannot-exist"
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestCIFailures_SkipsSupersededRuns covers the two superseded-run
// drop paths added in v0.22.120:
//
//  1. A failure whose head sha is more than MaxCommitsBehind commits
//     behind HEAD is dropped (stub git returns "200").
//  2. A failure whose workflow has had a later green run is dropped
//     (stub gh returns a success row whose createdAt is newer than
//     the failure).
//
// The test wires both stubs at once and asserts only the
// non-superseded run survives.
func TestCIFailures_SkipsSupersededRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub gh/git test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()

	// Stub gh that returns failure rows for `--status failure` and a
	// later success row for `--status success` of the
	// "stale-workflow" name. The fresh-workflow has zero green
	// history (empty array).
	ghPath := filepath.Join(dir, "gh")
	ghStub := `#!/bin/sh
# Inspect args to decide which fixture to print.
status=""
workflow=""
while [ $# -gt 0 ]; do
  case "$1" in
    --status) status="$2"; shift 2 ;;
    --workflow) workflow="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ "$status" = "success" ]; then
  if [ "$workflow" = "stale-workflow" ]; then
    cat <<'JSON'
[
  {"createdAt": "2026-05-01T10:00:00Z", "headSha": "feedfacefeedfacefeedfacefeedfacefeedface"}
]
JSON
  else
    echo "[]"
  fi
  exit 0
fi
# Default: failure list.
cat <<'JSON'
[
  {"databaseId": 7001, "name": "stale-workflow", "headSha": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "conclusion": "failure", "event": "push", "createdAt": "2026-04-30T09:00:00Z"},
  {"databaseId": 7002, "name": "far-behind-workflow", "headSha": "0badc0de0badc0de0badc0de0badc0de0badc0de", "conclusion": "failure", "event": "push", "createdAt": "2026-05-01T08:00:00Z"},
  {"databaseId": 7003, "name": "fresh-workflow", "headSha": "1eaf1eaf1eaf1eaf1eaf1eaf1eaf1eaf1eaf1eaf", "conclusion": "failure", "event": "push", "createdAt": "2026-05-01T11:00:00Z"}
]
JSON
`
	if err := os.WriteFile(ghPath, []byte(ghStub), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}

	// Stub git that returns 200 for the far-behind sha and 0 for
	// everything else.
	gitPath := filepath.Join(dir, "git")
	gitStub := `#!/bin/sh
# Args look like: rev-list --count <sha>..HEAD
for arg in "$@"; do
  case "$arg" in
    0badc0de*..HEAD) echo 200; exit 0 ;;
  esac
done
echo 0
`
	if err := os.WriteFile(gitPath, []byte(gitStub), 0o755); err != nil {
		t.Fatalf("write git stub: %v", err)
	}

	src := NewCIFailures()
	src.GHBinary = ghPath
	src.GitBinary = gitPath
	src.MaxCommitsBehind = 20

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Only fresh-workflow should survive: stale-workflow superseded
	// by green, far-behind-workflow superseded by distance.
	if len(ideas) != 1 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 1: %v", len(ideas), titles)
	}
	if !strings.Contains(ideas[0].Title, "fresh-workflow") {
		t.Fatalf("surviving idea = %q, want contains fresh-workflow", ideas[0].Title)
	}
}

// TestCIFailures_DropsUnreachableSha covers the third superseded-run
// drop path: a failure whose head sha is no longer present locally
// (force-pushed / history-rewritten away) is dropped because re-running
// the workflow on a vanished commit can't help.
func TestCIFailures_DropsUnreachableSha(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub gh/git test relies on /bin/sh; skip on Windows runners")
	}
	dir := t.TempDir()

	// Stub gh: one failure on a now-vanished sha, one on a live one.
	// Both workflows have no green history.
	ghPath := filepath.Join(dir, "gh")
	ghStub := `#!/bin/sh
status=""
while [ $# -gt 0 ]; do
  case "$1" in
    --status) status="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ "$status" = "success" ]; then
  echo "[]"
  exit 0
fi
cat <<'JSON'
[
  {"databaseId": 8001, "name": "vanished-workflow", "headSha": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "conclusion": "failure", "event": "push", "createdAt": "2026-05-01T09:00:00Z"},
  {"databaseId": 8002, "name": "live-workflow", "headSha": "1eaf1eaf1eaf1eaf1eaf1eaf1eaf1eaf1eaf1eaf", "conclusion": "failure", "event": "push", "createdAt": "2026-05-01T10:00:00Z"}
]
JSON
`
	if err := os.WriteFile(ghPath, []byte(ghStub), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}

	// Stub git:
	//  - rev-parse --verify HEAD → 0 (repo is healthy).
	//  - cat-file -e deadbeef* → 1 (object missing).
	//  - cat-file -e everything else → 0 (object present).
	//  - merge-base --is-ancestor → 0 (reachable).
	//  - rev-list --count → echo 0.
	gitPath := filepath.Join(dir, "git")
	gitStub := `#!/bin/sh
case "$1" in
  rev-parse) exit 0 ;;
  cat-file)
    sha="$3"
    case "$sha" in
      deadbeef*) exit 1 ;;
      *) exit 0 ;;
    esac
    ;;
  merge-base) exit 0 ;;
  rev-list) echo 0; exit 0 ;;
esac
exit 0
`
	if err := os.WriteFile(gitPath, []byte(gitStub), 0o755); err != nil {
		t.Fatalf("write git stub: %v", err)
	}

	src := NewCIFailures()
	src.GHBinary = ghPath
	src.GitBinary = gitPath
	src.MaxCommitsBehind = 20

	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		titles := make([]string, len(ideas))
		for i, idea := range ideas {
			titles[i] = idea.Title
		}
		t.Fatalf("Scan returned %d ideas, want 1 (vanished should be dropped): %v", len(ideas), titles)
	}
	if !strings.Contains(ideas[0].Title, "live-workflow") {
		t.Fatalf("surviving idea = %q, want contains live-workflow", ideas[0].Title)
	}
}
