// Package ci hosts recipes for the `ci` category — PR/push pipeline
// scaffolding. The first recipe drops a language-aware GitHub
// Actions test workflow tuned to the repo's primary language.
package ci

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/go.yml assets/node.yml assets/python.yml assets/rust.yml
var workflowAssets embed.FS

const ghActionsTestPath = ".github/workflows/test.yml"

// language is the discriminator for which embedded workflow ships.
type language string

const (
	langGo     language = "go"
	langNode   language = "node"
	langPython language = "python"
	langRust   language = "rust"
)

// detectLanguage probes manifest files in repo and returns the first
// match. Order matters: most-narrow first so a Go repo with a stray
// package.json (e.g. for prettier) doesn't get classified as Node.
func detectLanguage(repo string) (language, bool) {
	probes := []struct {
		path string
		lang language
	}{
		{"go.mod", langGo},
		{"Cargo.toml", langRust},
		{"package.json", langNode},
		{"requirements.txt", langPython},
		{"pyproject.toml", langPython},
	}
	for _, p := range probes {
		if exists, _ := setup.FileExists(filepath.Join(repo, p.path)); exists {
			return p.lang, true
		}
	}
	return "", false
}

type ghActionsTestRecipe struct{}

func (ghActionsTestRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "gh-actions-test",
		Category:    setup.CategoryCI,
		Description: "Language-aware GitHub Actions test workflow (auto-detects Go / Node / Python / Rust from manifest files).",
		Upstream:    "https://docs.github.com/en/actions/writing-workflows",
		Stability:   setup.StabilityStable,
	}
}

func (ghActionsTestRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	path := filepath.Join(repo, ghActionsTestPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		if _, ok := detectLanguage(repo); !ok {
			return setup.StatusAbsent, "no language manifest found (go.mod / package.json / requirements.txt / pyproject.toml / Cargo.toml)", nil
		}
		return setup.StatusAbsent, ".github/workflows/test.yml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "test.yml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

func (ghActionsTestRecipe) Prereqs() []setup.Prereq { return nil }

func (ghActionsTestRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	lang, ok := detectLanguage(repo)
	if !ok {
		return fmt.Errorf("no language manifest detected in %s (drop a go.mod / package.json / requirements.txt / pyproject.toml / Cargo.toml first, or skip this recipe)", repo)
	}

	path := filepath.Join(repo, ghActionsTestPath)
	if existing, err := setup.ReadIfExists(path); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", ghActionsTestPath)
	}

	body, err := workflowAssets.ReadFile("assets/" + string(lang) + ".yml")
	if err != nil {
		return fmt.Errorf("read embedded asset for %q: %w", lang, err)
	}
	return setup.WriteAtomic(path, body, 0o644)
}

func (ghActionsTestRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, ghActionsTestPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", ghActionsTestPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", ghActionsTestPath)
	}
	return nil
}

func init() { setup.Register(ghActionsTestRecipe{}) }
