// Package sources — adr_drafting source.
//
// Mirrors adr_questions: walks wiki/decisions/*.md, parses YAML
// frontmatter via the same line-scan approach (no yaml.v3 import
// pulled into this hot path — we only need two scalar fields:
// `status:` and `updated:`), and emits one Idea per ADR that has
// stayed in `status: drafting` for > 30 days. Stale drafts are a
// real source of decision-debt: an ADR that's been "thinking out
// loud" for over a month is either supersedable, promotable, or
// abandonable — but it shouldn't keep masquerading as in-flight.
//
// Wiki is gitignored on this repo per operator policy, so a
// missing wiki/ directory is a silent no-op rather than an error
// — the source returns an empty slice and lets the orchestrator
// move on (cheap-on-fail contract per source.go).
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
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/ideator"
)

// stalenessThreshold is the minimum age (since `updated:`) for a
// drafting ADR to surface as a stale-draft Idea. 30 days is the
// operator's quoted bar — long enough to swallow short feedback
// loops, short enough that "drafting since last quarter" never
// hides quietly in the wiki.
const stalenessThreshold = 30 * 24 * time.Hour

// nowFn is the clock injection seam for tests. Production code
// uses time.Now; tests overwrite this to drive deterministic
// staleness math regardless of when the suite runs.
var nowFn = time.Now

// ADRDrafting implements IdeaSource. Construct via NewADRDrafting.
type ADRDrafting struct{}

// NewADRDrafting returns a ready-to-use stale-drafting-ADR miner.
func NewADRDrafting() *ADRDrafting { return &ADRDrafting{} }

// Name returns the canonical source name used by --source filters.
func (ADRDrafting) Name() string { return "adr_drafting" }

// Scan walks repoRoot/wiki/decisions/*.md. Missing directory is a
// silent no-op. For each ADR with `status: drafting` whose
// `updated:` date is more than 30 days behind the wall clock, one
// Idea is emitted at priority 5 with evidence pointing at the ADR
// path. Per-file parse errors degrade gracefully: a malformed
// frontmatter block on one ADR doesn't kill the rest of the scan.
func (a ADRDrafting) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	root := filepath.Join(repoRoot, "wiki", "decisions")
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // wiki/ may not exist on this host
		}
		return nil, fmt.Errorf("adr_drafting: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	now := nowFn()
	var ideas []ideator.Idea
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil // cheap-on-fail; one unreadable file shouldn't kill the source
		}
		defer f.Close()
		fm, ok := parseADRFrontmatter(f)
		if !ok {
			return nil // no frontmatter or malformed — skip silently
		}
		if !strings.EqualFold(strings.TrimSpace(fm.status), "drafting") {
			return nil
		}
		updated, ok := parseADRDate(fm.updated)
		if !ok {
			return nil // missing/malformed `updated:` — can't compute age
		}
		age := now.Sub(updated)
		if age <= stalenessThreshold {
			return nil
		}
		days := int(age / (24 * time.Hour))
		rel, _ := filepath.Rel(repoRoot, path)
		hash := sha1.Sum([]byte(rel + "|drafting"))
		ideas = append(ideas, ideator.Idea{
			Title:             "stale drafting ADR: " + truncate(filepath.Base(rel), 60),
			Summary:           fmt.Sprintf("ADR %s has been in 'drafting' status for %d days. Decide: promote, supersede, or remove.", rel, days),
			Evidence:          rel,
			SuggestedPriority: 5,
			SuggestedPrompt: fmt.Sprintf(
				"ADR is in 'drafting' status, last updated %d days ago. "+
					"Promote to 'accepted', supersede with a newer ADR, or remove if no longer relevant.\n\n"+
					"Evidence: %s",
				days, rel),
			DedupeKey: "adr_drafting:" + hex.EncodeToString(hash[:]),
		})
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, ctx.Err()) {
		return ideas, fmt.Errorf("adr_drafting: walk: %w", walkErr)
	}
	return ideas, nil
}

// adrFrontmatter is the subset of YAML frontmatter this source
// needs. We avoid pulling yaml.v3 because the line-scan approach
// matches adr_questions.go's style and only two scalar fields are
// in play. Multi-line / nested YAML (aliases, related, sources)
// is intentionally skipped — those keys aren't read here and the
// scanner just stays inside the frontmatter block until the
// closing `---`.
type adrFrontmatter struct {
	status  string
	updated string
}

// parseADRFrontmatter reads the leading YAML frontmatter block
// (delimited by `---` lines) and pulls the `status:` and
// `updated:` scalars. Returns (frontmatter, true) when a block is
// present and at least one of the two fields was found. A file
// without frontmatter (no leading `---`) returns (zero, false).
//
// Approach mirrors adr_questions.go: bufio.Scanner, a small state
// machine, BOM-strip on line 1, no external deps. Indented keys
// (e.g. nested `related:` items) are ignored — only top-level
// `key: value` lines participate.
func parseADRFrontmatter(r *os.File) (adrFrontmatter, bool) {
	var fm adrFrontmatter
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 4096), 1<<20)
	lineNumber := 0
	inFrontmatter := false
	sawAnyField := false
	for scan.Scan() {
		lineNumber++
		line := scan.Text()
		if lineNumber == 1 {
			line = strings.TrimPrefix(line, utf8BOM)
		}
		trimmed := strings.TrimSpace(line)
		if !inFrontmatter {
			// Allow blank lines before the opening delimiter —
			// vanishingly rare but cheap to forgive.
			if trimmed == "" {
				continue
			}
			if trimmed == "---" {
				inFrontmatter = true
				continue
			}
			// First non-blank line wasn't `---`; this file has
			// no frontmatter. Bail out.
			return fm, false
		}
		// Closing delimiter ends the frontmatter block.
		if trimmed == "---" || trimmed == "..." {
			break
		}
		// Only top-level keys participate. A leading space /
		// tab marks an indented child (e.g. an `aliases:` list
		// item) which we ignore.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		key, value, ok := splitYAMLScalar(trimmed)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "status":
			fm.status = stripYAMLQuotes(value)
			sawAnyField = true
		case "updated":
			fm.updated = stripYAMLQuotes(value)
			sawAnyField = true
		}
	}
	return fm, sawAnyField
}

// splitYAMLScalar splits a `key: value` line. Returns
// (key, value, true) on success, else ("", "", false). Only
// scalar lines are recognized — `key:` with no value (block
// indicator) returns false because the scalar form is what this
// source needs.
func splitYAMLScalar(line string) (string, string, bool) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	if key == "" || value == "" {
		return "", "", false
	}
	// Drop trailing inline comments. YAML allows `# comment`
	// after a scalar; strip when preceded by whitespace so we
	// don't trash URLs.
	if hashIdx := strings.Index(value, " #"); hashIdx >= 0 {
		value = strings.TrimSpace(value[:hashIdx])
	}
	return key, value, true
}

// stripYAMLQuotes peels matching surrounding "..." or '...'
// quotes from a scalar value. Non-quoted values pass through
// unchanged.
func stripYAMLQuotes(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// parseADRDate parses the operator's canonical ADR date format
// (YYYY-MM-DD). Returns (time, true) on success, (zero, false)
// otherwise. Permissive about an optional time-of-day suffix in
// case an ADR ever upgrades to a full RFC3339 timestamp.
func parseADRDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
