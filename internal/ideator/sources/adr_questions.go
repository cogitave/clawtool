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

// utf8BOM is the byte-order-mark editors prepend to UTF-8 files.
// Built from raw bytes so the source file itself doesn't trip Go's
// "illegal byte order mark" lexer guard.
var utf8BOM = string([]byte{0xEF, 0xBB, 0xBF})

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
//
// Section tracking is "depth-aware": entering an `## Open questions`
// section keeps `inSection` true through any deeper subheader
// (`###`, `####`, …). It resets only when a header at the same or
// shallower depth fires — that's the next sibling section. Without
// this, real-world ADRs that nest a sub-header inside the open
// questions block (e.g. ADR-035 puts each layer under its own
// `### Layer 1 — ...`) silently emit zero questions.
func parseADRQuestions(adrPath string, r *os.File) []ideator.Idea {
	var (
		ideas        []ideator.Idea
		inSection    bool
		sectionDepth int // # count of the header that opened the section
		lineNumber   int
		title        = inferADRTitle(adrPath)
	)
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 4096), 1<<20)
	for scan.Scan() {
		lineNumber++
		line := scan.Text()
		// Strip a leading UTF-8 BOM on the very first line — editors
		// / Windows tools sometimes save .md files with one and the
		// unstripped BOM made the header detection silently miss the
		// first header.
		if lineNumber == 1 {
			line = strings.TrimPrefix(line, utf8BOM)
		}
		trimmed := strings.TrimSpace(line)
		if depth := headerDepth(trimmed); depth > 0 {
			if isOpenQuestionsHeader(trimmed) {
				inSection = true
				sectionDepth = depth
				continue
			}
			// `### Resolved questions` (or any depth) under an
			// open-questions block closes the block regardless
			// of depth — it's by convention a sibling-in-spirit
			// even when authors nest it. Belt-and-braces with
			// the `hasResolutionMarker` per-item filter below;
			// either fence catches the post-2026-05-02 wave.
			if inSection && isResolvedQuestionsHeader(trimmed) {
				inSection = false
				continue
			}
			// A deeper header inside the section is treated as a
			// sub-section: keep inSection. Only same-or-shallower
			// headers close the block.
			if inSection && depth <= sectionDepth {
				inSection = false
			}
			continue
		}
		if !inSection {
			continue
		}
		question, ok := stripListMarker(trimmed)
		if !ok || question == "" {
			continue
		}
		// Drop items whose head bears an inline-resolution marker.
		// After the 2026-05-02 ADR resolution wave, several ADRs
		// keep already-answered questions in place under
		// "## Open questions" annotated with `*Resolved.*` /
		// `— Resolved (2026-…)` / `-- Resolved` etc. so the
		// historical numbering stays stable. Surfacing those as
		// fresh open questions sends the Ideator chasing
		// ghosts. Match against the FIRST 200 chars of the item
		// so we filter only sentence-level prominence — the word
		// "resolved" deep inside prose is not a marker.
		if hasResolutionMarker(question) {
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

// headerDepth returns the number of leading `#` characters on the
// line (1 for `# Foo`, 2 for `## Bar`, …). Returns 0 when the line
// is not an ATX header — guards against e.g. fenced code blocks
// containing `# comment` lines.
func headerDepth(line string) int {
	depth := 0
	for depth < len(line) && line[depth] == '#' {
		depth++
	}
	if depth == 0 {
		return 0
	}
	// ATX headers require a space (or newline / EOF) after the
	// hashes. `#tag` is not a header.
	if depth < len(line) && line[depth] != ' ' && line[depth] != '\t' {
		return 0
	}
	return depth
}

// isOpenQuestionsHeader matches "## Open questions" /
// "### Open Questions" / "## Open Questions / Risks" / etc.
// Case-insensitive; permissive about trailing qualifiers because
// real ADRs use them ("Open questions (deferred)", "Open
// observations", "Open questions / risks").
func isOpenQuestionsHeader(line string) bool {
	low := strings.ToLower(strings.TrimLeft(line, "# "))
	low = strings.TrimSpace(low)
	if low == "open questions" || low == "open question" {
		return true
	}
	// Match "open questions <suffix>" — e.g. "open questions /
	// risks", "open questions (deferred)", "open questions —
	// 2026-04". The suffix must start with a word-boundary
	// character so we don't accidentally swallow "open
	// questionsworth" — vanishingly unlikely but cheap to guard.
	for _, prefix := range []string{"open questions ", "open question "} {
		if strings.HasPrefix(low, prefix) {
			return true
		}
	}
	return false
}

// isResolvedQuestionsHeader matches "## Resolved questions" /
// "### Resolved questions" / "## Resolved Questions / Notes" /
// any depth and any trailing qualifier. Used to close an open-
// questions block early when the author parks resolved entries
// under a nested sub-header rather than promoting them to a
// sibling section. Mirrors `isOpenQuestionsHeader` style — keep
// the two predicates symmetric so future header-shape tweaks land
// in lockstep.
func isResolvedQuestionsHeader(line string) bool {
	low := strings.ToLower(strings.TrimLeft(line, "# "))
	low = strings.TrimSpace(low)
	if low == "resolved questions" || low == "resolved question" {
		return true
	}
	for _, prefix := range []string{"resolved questions ", "resolved question "} {
		if strings.HasPrefix(low, prefix) {
			return true
		}
	}
	return false
}

// stripListMarker pulls the leading "- " / "* " / "+ " / "1. " /
// "1) " marker off the line. Returns (body, true) when the line is
// a list item, else (line, false). Also handles "- 1. foo" (a
// numbered item nested inside a bulleted list — the leading bullet
// is stripped first, then the numbered marker).
func stripListMarker(line string) (string, bool) {
	// Bulleted list — "- foo", "* foo", "+ foo".
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, marker) {
			body := strings.TrimSpace(strings.TrimPrefix(line, marker))
			// Permit "- 1. foo" / "- 1) foo" by recursively
			// stripping a numbered marker from the inner body.
			if inner, ok := stripNumberedMarker(body); ok {
				return inner, true
			}
			return body, true
		}
	}
	if inner, ok := stripNumberedMarker(line); ok {
		return inner, true
	}
	return line, false
}

// stripNumberedMarker peels "1. foo" / "12) foo" off the front of
// the line. Returns (body, true) when matched, else (line, false).
func stripNumberedMarker(line string) (string, bool) {
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if i == 0 {
			return line, false
		}
		// First non-digit must be "." or ")" followed by a space.
		if (r == '.' || r == ')') && i+1 < len(line) && (line[i+1] == ' ' || line[i+1] == '\t') {
			return strings.TrimSpace(line[i+2:]), true
		}
		return line, false
	}
	return line, false
}

// resolutionMarkerScanLen is the byte window at the head of a list
// item we inspect for sentence-level resolution markers. 200 is wide
// enough to span a `**Bold prefix** — *Resolved.*` opener (operator's
// canonical pattern) without dragging the whole prose body in.
const resolutionMarkerScanLen = 200

// hasResolutionMarker returns true when the head of `item` carries
// a sentence-level "this is already resolved" annotation. Matching
// only the first 200 chars keeps the predicate honest: questions
// that merely *mention* the word "resolved" deep in their prose
// stay surfaced. The literal-period form `Resolved.` is required
// for the bare-word case so a question like "Should the spec be
// considered resolved when…" doesn't get filtered.
//
// Markers (any one trips the filter):
//
//   - `Resolved.`     — italic / inline state marker, e.g.
//     `**Foo** — *Resolved.* explanation`
//   - `— Resolved`    — em-dash separator, e.g.
//     `### Foo — Resolved (2026-05-02)` content list-item form,
//     `- **Foo? — Resolved (2026-05-02)**.`
//   - `-- Resolved`   — double-hyphen ASCII fallback
//   - `(Resolved 2026-` — date-anchored marker used by the wave
//     of resolutions filed on 2026-05-02
func hasResolutionMarker(item string) bool {
	head := item
	if len(head) > resolutionMarkerScanLen {
		head = head[:resolutionMarkerScanLen]
	}
	if strings.Contains(head, "Resolved.") {
		return true
	}
	if strings.Contains(head, "— Resolved") || strings.Contains(head, "-- Resolved") {
		return true
	}
	if strings.Contains(head, "(Resolved 2026-") {
		return true
	}
	return false
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
