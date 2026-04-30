package governance

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/CLAUDE.md
var claudeMDTemplate string

//go:embed assets/AGENTS.md
var agentsMDTemplate string

const (
	claudeMDPath = "CLAUDE.md"
	agentsMDPath = "AGENTS.md"
)

// claudeMDRecipe drops a starter CLAUDE.md tuned to the repo's
// primary language. The template is opinionated: short rules, no
// filler, written in the same imperative voice Claude Code itself
// loads as system context.
type claudeMDRecipe struct{}

func (claudeMDRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "claude-md",
		Category:    setup.CategoryGovernance,
		Description: "Drops a CLAUDE.md (Claude Code's project-system-prompt) — language-aware starter with the conventions clawtool ships its own repo with.",
		Upstream:    "spec:https://docs.claude.com/en/docs/claude-code/memory",
		Stability:   setup.StabilityStable,
		// Core: governance scaffold every fresh repo wants.
		Core: true,
	}
}

func (claudeMDRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	return detectMarkdownFile(repo, claudeMDPath)
}

func (claudeMDRecipe) Prereqs() []setup.Prereq { return nil }

func (claudeMDRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	return applyMarkdownTemplate(repo, claudeMDPath, claudeMDTemplate, opts)
}

func (claudeMDRecipe) Verify(_ context.Context, repo string) error {
	return verifyMarkdownFile(repo, claudeMDPath)
}

// agentsMDRecipe drops a starter AGENTS.md per the agents.md spec
// (https://agents.md). Tells multi-agent setups how to coordinate
// in this repo.
type agentsMDRecipe struct{}

func (agentsMDRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "agents-md",
		Category:    setup.CategoryGovernance,
		Description: "Drops an AGENTS.md (multi-agent coordination spec) per agents.md — same content slot as CLAUDE.md but vendor-neutral.",
		Upstream:    "spec:https://agents.md",
		Stability:   setup.StabilityStable,
	}
}

func (agentsMDRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	return detectMarkdownFile(repo, agentsMDPath)
}

func (agentsMDRecipe) Prereqs() []setup.Prereq { return nil }

func (agentsMDRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	return applyMarkdownTemplate(repo, agentsMDPath, agentsMDTemplate, opts)
}

func (agentsMDRecipe) Verify(_ context.Context, repo string) error {
	return verifyMarkdownFile(repo, agentsMDPath)
}

// ── shared helpers ─────────────────────────────────────────────────

// detectMarkdownFile is the common detect for the two template
// drops. Returns Applied iff the file exists with the clawtool
// marker (HTML comment); Partial when present without marker;
// Absent otherwise.
func detectMarkdownFile(repo, rel string) (setup.Status, string, error) {
	path := filepath.Join(repo, rel)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, fmt.Sprintf("%s not present", rel), nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, fmt.Sprintf("%s exists but is not clawtool-managed; refusing to overwrite", rel), nil
}

// applyMarkdownTemplate substitutes a small set of placeholders
// before writing. The {{ project }} placeholder defaults to the
// repo directory name; {{ language }} is filled from
// detectLanguageHint or Options[lang]. Both are optional in the
// templates — they degrade to a generic phrasing if absent.
func applyMarkdownTemplate(repo, rel, tmpl string, opts setup.Options) error {
	path := filepath.Join(repo, rel)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite (pass force=true to override)", rel)
	}

	project := strings.TrimSpace(filepath.Base(repo))
	if project == "" || project == "." {
		project = "your-project"
	}
	if v, ok := setup.GetOption[string](opts, "project"); ok && strings.TrimSpace(v) != "" {
		project = v
	}

	lang, _ := setup.GetOption[string](opts, "lang")
	if strings.TrimSpace(lang) == "" {
		lang = detectLanguageHint(repo)
	}
	if strings.TrimSpace(lang) == "" {
		lang = "polyglot"
	}

	rendered := strings.NewReplacer(
		"{{ project }}", project,
		"{{ language }}", lang,
	).Replace(tmpl)
	return setup.WriteAtomic(path, []byte(rendered), 0o644)
}

func verifyMarkdownFile(repo, rel string) error {
	path := filepath.Join(repo, rel)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", rel)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", rel)
	}
	return nil
}

// detectLanguageHint probes the repo for the common manifest
// files (same priority as the ci/runtime recipes). Returns "" when
// nothing matches — callers fall back to "polyglot".
func detectLanguageHint(repo string) string {
	probes := []struct {
		path string
		lang string
	}{
		{"go.mod", "Go"},
		{"Cargo.toml", "Rust"},
		{"package.json", "Node.js / TypeScript"},
		{"requirements.txt", "Python"},
		{"pyproject.toml", "Python"},
	}
	for _, p := range probes {
		if exists, _ := setup.FileExists(filepath.Join(repo, p.path)); exists {
			return p.lang
		}
	}
	return ""
}

func init() {
	setup.Register(claudeMDRecipe{})
	setup.Register(agentsMDRecipe{})
}
