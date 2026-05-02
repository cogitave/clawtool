// Package sources — concrete IdeaSource implementations.
//
// adr_questions: parses every ADR markdown under wiki/decisions/ for
// a "## Open questions" section and emits one Idea per numbered or
// bulleted item. Wiki is gitignored on this repo per operator
// policy, so a missing wiki/ directory is a silent no-op rather
// than an error — the source returns an empty slice and lets the
// orchestrator move on.
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

	"github.com/cogitave/clawtool/internal/ideator"
)

// ADRQuestions implements IdeaSource. Construct via NewADRQuestions.
type ADRQuestions struct{}

// NewADRQuestions returns a ready-to-use ADR question miner.
func NewADRQuestions() *ADRQuestions { return &ADRQuestions{} }

// Name returns the canonical source name used by --source filters.
func (ADRQuestions) Name() string { return "adr_questions" }

// Scan walks repoRoot/wiki/decisions/*.md. Missing directory is a
// silent no-op (wiki/ is gitignored on this repo). Each line under
// "## Open questions" that begins with `-`, `*`, or a numbered list
// marker becomes one Idea. The DedupeKey is sha1(adr-path + question
// text) so an unedited file produces stable IDs across runs.
func (a ADRQuestions) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	root := filepath.Join(repoRoot, "wiki", "decisions")
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // wiki/ may not exist on this host
		}
		return nil, fmt.Errorf("adr_questions: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

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
		rel, _ := filepath.Rel(repoRoot, path)
		ideas = append(ideas, parseADRQuestions(rel, f)...)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, ctx.Err()) {
		return ideas, fmt.Errorf("adr_questions: walk: %w", walkErr)
	}
	return ideas, nil
}

// parseADRQuestions does the line-by-line section detection. Split
// out so unit tests can drive it with strings.NewReader without a
// real file.
func parseADRQuestions(adrPath string, r *os.File) []ideator.Idea {
	var (
		ideas      []ideator.Idea
		inSection  bool
		lineNumber int
		title      = inferADRTitle(adrPath)
	)
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 4096), 1<<20)
	for scan.Scan() {
		lineNumber++
		line := scan.Text()
		trimmed := strings.TrimSpace(line)
		if isHeader(trimmed) {
			inSection = isOpenQuestionsHeader(trimmed)
			continue
		}
		if !inSection {
			continue
		}
		question, ok := stripListMarker(trimmed)
		if !ok || question == "" {
			continue
		}
		evidence := fmt.Sprintf("%s:%d", adrPath, lineNumber)
		hash := sha1.Sum([]byte(adrPath + "|" + question))
		ideas = append(ideas, ideator.Idea{
			Title:             "open question: " + truncate(question, 60),
			Summary:           fmt.Sprintf("Open question raised in ADR %s — resolve and either close the question or fold it into a follow-up ADR.", title),
			Evidence:          evidence,
			SuggestedPriority: 4,
			SuggestedPrompt: fmt.Sprintf(
				"Resolve the open question raised in %s:\n\n  %s\n\n"+
					"Investigate the codebase, decide an answer, then either edit the ADR's"+
					" 'Open questions' block to record the resolution or split the question"+
					" into a follow-up ADR with its own decision body.",
				adrPath, question),
			DedupeKey: "adr_questions:" + hex.EncodeToString(hash[:]),
		})
	}
	return ideas
}

// isHeader detects any markdown header (one or more `#`).
func isHeader(line string) bool {
	return strings.HasPrefix(line, "#")
}

// isOpenQuestionsHeader matches "## Open questions" /
// "### Open Questions" / etc, case-insensitive.
func isOpenQuestionsHeader(line string) bool {
	low := strings.ToLower(strings.TrimLeft(line, "# "))
	low = strings.TrimSpace(low)
	return low == "open questions" || low == "open question"
}

// stripListMarker pulls the leading "- " / "* " / "1. " marker off
// the line. Returns (body, true) when the line is a list item, else
// (line, false).
func stripListMarker(line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "- "):
		return strings.TrimSpace(strings.TrimPrefix(line, "- ")), true
	case strings.HasPrefix(line, "* "):
		return strings.TrimSpace(strings.TrimPrefix(line, "* ")), true
	}
	// Numbered list: "1. ..." / "12. ..."
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if i == 0 {
			return line, false
		}
		// First non-digit must be ". " for it to qualify.
		if r == '.' && i+1 < len(line) && line[i+1] == ' ' {
			return strings.TrimSpace(line[i+2:]), true
		}
		return line, false
	}
	return line, false
}

// inferADRTitle pulls a short label from the ADR filename
// ("035-self-direction.md" → "035 Self-direction").
func inferADRTitle(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "-", " ")
	if len(base) > 0 {
		base = strings.ToUpper(base[:1]) + base[1:]
	}
	return base
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
