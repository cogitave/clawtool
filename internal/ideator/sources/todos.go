// Package sources — todos source.
//
// Walks repoRoot for *.go files (using a Go-native walker — no
// shell-out, no rg dependency) and emits one Idea per TODO / FIXME /
// XXX comment. DedupeKey is sha1(path + line + comment text) so the
// same comment surfaces the same id across runs even when other
// comments above it shift line numbers.
package sources

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cogitave/clawtool/internal/ideator"
)

// TODOs implements IdeaSource by mining TODO/FIXME/XXX comments
// from *.go files.
type TODOs struct {
	// MaxIdeas caps the source's emit count so a repo with hundreds
	// of TODOs doesn't drown the orchestrator's top-K. Default 50.
	MaxIdeas int
	// SkipDirs are directory names skipped during the walk
	// (default: vendor, node_modules, .git, .clawtool, dist, bin).
	SkipDirs []string
}

// NewTODOs returns a ready-to-use TODO/FIXME/XXX miner with sane
// skip defaults.
func NewTODOs() *TODOs {
	return &TODOs{
		MaxIdeas: 50,
		SkipDirs: []string{".git", "vendor", "node_modules", ".clawtool", "dist", "bin", "wiki", "testdata"},
	}
}

// Name returns the canonical source name.
func (TODOs) Name() string { return "todos" }

// todoLine matches `// TODO: ...`, `// FIXME(arda): ...`,
// `// XXX ...` etc. Captures the marker (TODO/FIXME/XXX) and the
// trailing comment body.
var todoLine = regexp.MustCompile(`(?i)//\s*(TODO|FIXME|XXX)\b\s*(?:\([^)]*\))?\s*:?\s*(.*)`)

// Scan walks repoRoot in-process. Cheap-on-fail: read errors on
// individual files become silent skips so one weird file doesn't
// hide every other TODO.
func (t TODOs) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	skip := make(map[string]struct{}, len(t.SkipDirs))
	for _, d := range t.SkipDirs {
		skip[d] = struct{}{}
	}
	max := t.MaxIdeas
	if max <= 0 {
		max = 50
	}

	var ideas []ideator.Idea
	walkErr := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, drop := skip[d.Name()]; drop {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		// Skip generated and test fixture files: marker comment
		// `Code generated ... DO NOT EDIT.` lives at the top.
		if isGoGenerated(path) {
			return nil
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(repoRoot, path)
		ideas = append(ideas, scanTODOLines(rel, f)...)
		if len(ideas) >= max {
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, ctx.Err()) {
		return ideas, fmt.Errorf("todos: walk: %w", walkErr)
	}
	if len(ideas) > max {
		ideas = ideas[:max]
	}
	return ideas, nil
}

// scanTODOLines is the per-file inner loop. Pulled out so tests can
// hit it directly without spinning up a tmpdir.
func scanTODOLines(relPath string, f *os.File) []ideator.Idea {
	var out []ideator.Idea
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 4096), 1<<20)
	lineNumber := 0
	for scan.Scan() {
		lineNumber++
		m := todoLine.FindStringSubmatch(scan.Text())
		if m == nil {
			continue
		}
		marker := strings.ToUpper(m[1])
		body := strings.TrimSpace(m[2])
		if body == "" {
			body = "(no body — see comment)"
		}
		evidence := fmt.Sprintf("%s:%d", relPath, lineNumber)
		hash := sha1.Sum([]byte(relPath + "|" + body))
		priority := 3
		if marker == "FIXME" {
			priority = 5 // fixme implies a known bug, not a wishlist
		}
		out = append(out, ideator.Idea{
			Title:             marker + ": " + truncate(body, 60),
			Summary:           fmt.Sprintf("%s comment in %s — investigate, decide whether the work is still needed, and either land the change or remove the comment.", marker, relPath),
			Evidence:          evidence,
			SuggestedPriority: priority,
			SuggestedPrompt: fmt.Sprintf(
				"Address the %s comment at %s:\n\n  %s\n\nDecide whether the work is still relevant. If yes, land the fix"+
					" and remove the comment. If no, delete the comment with a brief"+
					" justification in the commit message.",
				marker, evidence, body),
			DedupeKey: "todos:" + hex.EncodeToString(hash[:]),
		})
	}
	return out
}

// isGoGenerated reads the first 4 KiB of path looking for the
// `Code generated ... DO NOT EDIT.` marker the Go toolchain agrees
// on. Generated files are skipped because their TODOs are owned by
// the generator, not human-actionable.
func isGoGenerated(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	return strings.Contains(string(buf[:n]), "Code generated") && strings.Contains(string(buf[:n]), "DO NOT EDIT")
}
