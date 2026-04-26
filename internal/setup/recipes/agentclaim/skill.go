package agentclaim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/setup"
)

// skillRecipe installs a standalone Claude Code skill — a SKILL.md
// dropped under ~/.claude/skills/<name>/SKILL.md so Claude Code
// loads it like any other skill, without needing a full plugin
// marketplace. Used for community-shared skills (e.g. Karpathy's
// LLM Wiki notetaking pattern) that authors publish as a single
// markdown file.
//
// Two install modes:
//
//   1. Embedded (Body non-empty): clawtool ships the SKILL.md
//      inline. No network. Used for community skills we want to
//      bundle for reliability.
//
//   2. URL (URL non-empty): clawtool downloads the SKILL.md at
//      Apply time. The URL must point to raw markdown (e.g. a raw
//      GitHub gist). Useful for skills the author updates often
//      where bundling would freeze a stale copy.
//
// Body wins if both are set.
type skillRecipe struct {
	name        string
	description string
	upstream    string

	// Body is the verbatim SKILL.md content. Empty → use URL.
	Body string

	// URL is the location of a raw SKILL.md to download. Used when
	// Body is empty. clawtool refuses to install if the response
	// isn't text/markdown or text/plain.
	URL string
}

func (s skillRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        s.name,
		Category:    setup.CategoryAgents,
		Description: s.description,
		Upstream:    s.upstream,
		Stability:   setup.StabilityBeta,
	}
}

// skillsRoot returns the directory standalone skills live in.
// Honors $CLAUDE_HOME (uncommon, used by some installer setups)
// then falls back to $HOME/.claude/skills.
func skillsRoot() string {
	if x := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); x != "" {
		return filepath.Join(x, "skills")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".claude/skills"
	}
	return filepath.Join(home, ".claude", "skills")
}

func (s skillRecipe) skillFile() string {
	return filepath.Join(skillsRoot(), s.name, "SKILL.md")
}

func (s skillRecipe) Detect(_ context.Context, _ string) (setup.Status, string, error) {
	path := s.skillFile()
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "skill not installed", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, fmt.Sprintf("%s exists but is not clawtool-managed; refusing to overwrite", path), nil
}

func (s skillRecipe) Prereqs() []setup.Prereq { return nil }

// skillHTTPClient is package-level so tests can swap it.
var skillHTTPClient = &http.Client{Timeout: 10 * time.Second}

func (s skillRecipe) Apply(ctx context.Context, _ string, opts setup.Options) error {
	body := []byte(s.Body)
	if len(body) == 0 && s.URL != "" {
		downloaded, err := s.fetchURL(ctx)
		if err != nil {
			return fmt.Errorf("fetch skill body: %w", err)
		}
		body = downloaded
	}
	if len(body) == 0 {
		return errors.New("skill recipe has neither embedded Body nor URL — broken registration")
	}

	// Stamp the marker if the upstream content doesn't already
	// carry it. We respect existing comments — most SKILL.md files
	// open with frontmatter, so we prepend a single HTML-comment
	// line that survives the YAML parser.
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		body = append([]byte("<!-- "+setup.ManagedByMarker+" -->\n"), body...)
	}

	path := s.skillFile()
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite (pass force=true to override)", path)
	}
	return setup.WriteAtomic(path, body, 0o644)
}

func (s skillRecipe) Verify(_ context.Context, _ string) error {
	b, err := setup.ReadIfExists(s.skillFile())
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", s.skillFile())
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", s.skillFile())
	}
	return nil
}

func (s skillRecipe) fetchURL(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "clawtool-skill-fetch")
	resp, err := skillHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !(strings.HasPrefix(ct, "text/markdown") ||
		strings.HasPrefix(ct, "text/plain") ||
		strings.HasPrefix(ct, "text/x-markdown")) {
		return nil, fmt.Errorf("response content-type %q is not markdown/plain", ct)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap on a SKILL.md
}

// ── shipped skill recipes ──────────────────────────────────────────

// karpathyLLMWikiSkill is the embedded copy of Andrej Karpathy's
// "LLM Wiki" notetaking pattern, adapted as a Claude Code skill.
// It teaches the model to file insights, decisions, and questions
// into a markdown wiki layout the user can navigate later.
//
// Distinct from the `brain` recipe (which wraps claude-obsidian +
// the Obsidian app). This is plain markdown the user reads in
// any editor; brain is the Obsidian-bound integration.
const karpathyLLMWikiSkill = `---
name: karpathy-llm-wiki
description: >
  When working with the user across multiple sessions, treat their
  knowledge as a persistent markdown wiki. Each insight, decision,
  open question, or research finding lands as its own page under
  ~/wiki/<topic>.md. Cross-link with [[wikilinks]]. Append to
  ~/wiki/log.md on every meaningful interaction. Keep pages short
  and editable — the user owns the wiki, you only contribute to it.

  Per Karpathy's "LLM Wiki" pattern (2024–2026): the wiki is the
  long-term memory, the chat is the short-term scratchpad, and
  every interaction either adds a page, updates one, or pulls
  context from one.

  Triggers on: "save this", "note this", "what do we know about",
  "log this decision", "add to wiki", "what did we decide", "find
  the page on", "search my notes".
---

# Karpathy LLM Wiki

You have access to a markdown wiki under ~/wiki/. Treat it as the
user's long-term memory across sessions.

## When the user shares a fact, insight, or decision

1. Pick a short kebab-case topic name (e.g. ` + "`go-error-handling`" + `).
2. Write or update ~/wiki/<topic>.md with a clear H1 + body.
3. Cross-link any other topic mentioned with ` + "`[[other-topic]]`" + `.
4. Append a one-line entry to ~/wiki/log.md:
   - format: ` + "`- 2026-04-26 14:30 — added [[<topic>]]: <one-line summary>`" + `

## When the user asks a question

1. Search ~/wiki/ for relevant pages (Grep + a quick read pass).
2. If found: cite the page (` + "`[[<topic>]]`" + `) and answer from it.
3. If not found: answer from general knowledge AND offer to file
   the new finding as a wiki page.

## Wiki conventions

- One topic per page. If a page grows past ~200 lines, split it.
- Every page opens with a one-paragraph "What this is" summary.
- Open questions go under an "## Open" section so they're easy to
  scan across pages.
- Decisions go under "## Decisions" with the rationale + date.

## What NOT to do

- Don't reformat or "improve" pages the user wrote unless they ask.
- Don't add Co-Authored-By or AI-attribution footers anywhere.
- Don't move existing files; treat the wiki as append-mostly.
- If the wiki doesn't exist (~/wiki/ is empty), create
  ~/wiki/log.md and ~/wiki/index.md and seed them with a one-line
  README. Don't generate filler.
`

func init() {
	setup.Register(skillRecipe{
		name:        "karpathy-llm-wiki",
		description: "Karpathy's \"LLM Wiki\" pattern — drops a SKILL.md that teaches Claude to file insights into ~/wiki/. Plain markdown; no Obsidian dependency.",
		upstream:    "https://github.com/karpathy/llm-wiki", // canonical pointer; Karpathy has discussed this pattern publicly
		Body:        karpathyLLMWikiSkill,
	})
}
