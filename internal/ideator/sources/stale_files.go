// Package sources — stale_files source.
//
// Surfaces .go files whose newest commit is older than
// DefaultStaleAge (default 90 days). Files this old are review
// candidates: doc rot, slow regressions, dead-but-still-used helpers
// the deadcode source can't catch (because they're reachable but
// nobody touches them).
//
// This source exists specifically so the Ideator never goes silent.
// When the eight signal-driven sources (CI failures, deadcode, etc.)
// converge to zero, stale_files keeps the loop productive by
// surfacing existing-code review opportunities. The operator can
// always skip the proposed item if it's not interesting; the cost
// is one autopilot row, the win is a never-dry signal feed.
//
// `git log -1 --format=%ct -- <path>` per file would be O(N) shells.
// Instead we run `git ls-files -z '*.go'` once + a single
// `git log --name-only --format=%ct%n --since=<cutoff> -- *.go`
// pass and treat any file NOT printed in that window as stale.
//
// Missing git / non-repo cwd → silent no-op (cheap-on-fail).
package sources

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/ideator"
)

// DefaultStaleAge is how long a file must go untouched before it
// surfaces. 90 days is roughly one quarter — short enough that
// real maintenance gaps appear, long enough that active
// development doesn't ring the bell for every helper.
const DefaultStaleAge = 90 * 24 * time.Hour

// DefaultStaleMaxIdeas caps how many stale files we report per run.
// 5 is small enough to not drown the operator, large enough that
// the loop stays productive across multiple ideate cycles.
const DefaultStaleMaxIdeas = 5

// StaleFiles implements IdeaSource for "untouched .go files".
type StaleFiles struct {
	// GitBinary lets tests inject a stub; defaults to "git".
	GitBinary string
	// MinAge overrides DefaultStaleAge.
	MinAge time.Duration
	// MaxIdeas caps the result set; default DefaultStaleMaxIdeas.
	MaxIdeas int
	// Now lets tests pin "current time" for deterministic cutoffs.
	Now func() time.Time
	// Pattern restricts the file scan; defaults to "*.go".
	Pattern string
	// SkipPaths drops files whose path contains any of these
	// substrings. Default skips test fixtures + generated code.
	SkipPaths []string
}

// NewStaleFiles returns a ready-to-use stale-files miner.
func NewStaleFiles() *StaleFiles {
	return &StaleFiles{
		GitBinary: "git",
		MinAge:    DefaultStaleAge,
		MaxIdeas:  DefaultStaleMaxIdeas,
		Pattern:   "*.go",
		SkipPaths: []string{
			"/testdata/",
			"_test.go", // tests change less often by design
			".pb.go",
			"_generated.go",
			"vendor/",
		},
	}
}

// Name returns the canonical source name.
func (StaleFiles) Name() string { return "stale_files" }

// Scan walks tracked .go files, finds those whose newest commit is
// older than MinAge, and emits one Idea per stale file (capped).
func (s StaleFiles) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	gitBin := s.GitBinary
	if gitBin == "" {
		gitBin = "git"
	}
	if _, err := exec.LookPath(gitBin); err != nil {
		return nil, nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	minAge := s.MinAge
	if minAge <= 0 {
		minAge = DefaultStaleAge
	}
	maxIdeas := s.MaxIdeas
	if maxIdeas <= 0 {
		maxIdeas = DefaultStaleMaxIdeas
	}
	pattern := s.Pattern
	if pattern == "" {
		pattern = "*.go"
	}

	files, ok := listTrackedFiles(ctx, gitBin, repoRoot, pattern)
	if !ok || len(files) == 0 {
		return nil, nil
	}
	cutoff := now().Add(-minAge)
	recent, ok := recentlyTouchedFiles(ctx, gitBin, repoRoot, pattern, cutoff)
	if !ok {
		return nil, nil
	}

	type stale struct {
		path string
		age  time.Duration
	}
	var staleSet []stale
	for _, f := range files {
		if shouldSkipPath(f, s.SkipPaths) {
			continue
		}
		if recent[f] {
			continue
		}
		ts, ok := lastCommitTime(ctx, gitBin, repoRoot, f)
		if !ok {
			continue
		}
		age := now().Sub(ts)
		if age < minAge {
			continue
		}
		staleSet = append(staleSet, stale{path: f, age: age})
	}
	// Stable sort: oldest first so the most stale files surface.
	sort.SliceStable(staleSet, func(i, j int) bool {
		return staleSet[i].age > staleSet[j].age
	})
	if len(staleSet) > maxIdeas {
		staleSet = staleSet[:maxIdeas]
	}

	ideas := make([]ideator.Idea, 0, len(staleSet))
	for _, s := range staleSet {
		days := int(s.age.Hours() / 24)
		hash := sha1.Sum([]byte(s.path))
		ideas = append(ideas, ideator.Idea{
			Title:             fmt.Sprintf("Review stale file %s (%d days untouched)", filepath.Base(s.path), days),
			Summary:           fmt.Sprintf("%s hasn't changed in %d days. Skim for doc rot, dead helpers reachable only through legacy paths, or comments referencing decisions since superseded.", s.path, days),
			Evidence:          fmt.Sprintf("git log %s — newest commit %d days ago", s.path, days),
			SuggestedPriority: 2,
			SuggestedPrompt: fmt.Sprintf(
				"Audit `%s` — last modified %d days ago.\n\n"+
					"  - file: %s\n"+
					"  - days untouched: %d\n\n"+
					"Skim the file for: stale doc comments, helpers no longer reachable from active call paths, references to decisions / commits / files that have since been renamed or deleted, dead error branches that can't fire after recent refactors. "+
					"If the file is fine as-is, mark the (ideator-skip) marker on the package's doc comment so this idea doesn't re-surface; otherwise land the smallest cleanup that adds real value.",
				s.path, days, s.path, days,
			),
			DedupeKey: "stale_files:" + hex.EncodeToString(hash[:]),
		})
	}
	return ideas, nil
}

// listTrackedFiles returns repo-relative paths matching pattern,
// using `git ls-files`. Empty + false on git error / non-repo cwd.
func listTrackedFiles(ctx context.Context, gitBin, repoRoot, pattern string) ([]string, bool) {
	cmd := exec.CommandContext(ctx, gitBin, "ls-files", "-z", "--", pattern)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	parts := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, true
}

// recentlyTouchedFiles returns the set of files mentioned by
// `git log --name-only --format= --since=<cutoff> -- <pattern>`.
// The set's keys are repo-relative paths exactly as ls-files prints
// them; membership = "touched after cutoff".
func recentlyTouchedFiles(ctx context.Context, gitBin, repoRoot, pattern string, cutoff time.Time) (map[string]bool, bool) {
	since := cutoff.Format(time.RFC3339)
	cmd := exec.CommandContext(ctx, gitBin, "log",
		"--name-only", "--format=", "--since="+since, "--", pattern)
	cmd.Dir = repoRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	recent := make(map[string]bool)
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			recent[line] = true
		}
	}
	io.Copy(io.Discard, stdout)
	_ = cmd.Wait()
	return recent, true
}

// lastCommitTime returns the unix-epoch timestamp of the newest
// commit touching path. False when git can't answer (deleted file
// missed by ls-files, submodule, etc.).
func lastCommitTime(ctx context.Context, gitBin, repoRoot, path string) (time.Time, bool) {
	cmd := exec.CommandContext(ctx, gitBin, "log", "-1", "--format=%ct", "--", path)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, false
	}
	var ts int64
	if _, err := fmt.Sscanf(s, "%d", &ts); err != nil {
		return time.Time{}, false
	}
	return time.Unix(ts, 0), true
}

// shouldSkipPath returns true if the path contains any skip
// substring. Substrings (not globs) so callers can ignore "vendor/"
// or "/testdata/" without per-platform path-separator gymnastics.
func shouldSkipPath(path string, skips []string) bool {
	norm := filepath.ToSlash(path)
	for _, s := range skips {
		if strings.Contains(norm, s) {
			return true
		}
	}
	return false
}
