// Package runtime hosts recipes for the `runtime` category — dev
// environment + container scaffolding. The first recipe drops a
// devcontainer.json tuned to the repo's primary language so a user
// can open the repo in Codespaces or Remote-SSH and have a working
// dev environment without further setup.
package runtime

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/go.json assets/node.json assets/python.json assets/rust.json
var devcontainerAssets embed.FS

const devcontainerPath = ".devcontainer/devcontainer.json"

// language is the discriminator the recipe uses to pick its asset.
// Mirrored from internal/setup/recipes/ci/gh_actions.go to keep the
// detection logic consistent across categories — same probes, same
// priority order. A future refactor (v0.11) could hoist this to a
// shared helper if a third recipe needs it.
type language string

const (
	langGo     language = "go"
	langNode   language = "node"
	langPython language = "python"
	langRust   language = "rust"
)

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

type devcontainerRecipe struct{}

func (devcontainerRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "devcontainer",
		Category:    setup.CategoryRuntime,
		Description: "Codespaces / Remote-SSH dev environment — drops .devcontainer/devcontainer.json with language-detected base image, VS Code extensions, and post-create hook.",
		Upstream:    "https://containers.dev",
		Stability:   setup.StabilityStable,
	}
}

// Detect treats the file as the canonical signal. We don't try to
// validate the JSON shape — once the marker is present (we stamp it
// inside an unused `_` key) we consider the file ours.
func (devcontainerRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	path := filepath.Join(repo, devcontainerPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		if _, ok := detectLanguage(repo); !ok {
			return setup.StatusAbsent, "no language manifest found (go.mod / package.json / requirements.txt / pyproject.toml / Cargo.toml)", nil
		}
		return setup.StatusAbsent, ".devcontainer/devcontainer.json not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "devcontainer.json exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

func (devcontainerRecipe) Prereqs() []setup.Prereq { return nil }

func (devcontainerRecipe) Apply(_ context.Context, repo string, _ setup.Options) error {
	lang, ok := detectLanguage(repo)
	if !ok {
		return fmt.Errorf("no language manifest detected in %s; devcontainer recipe needs Go/Node/Python/Rust", repo)
	}

	path := filepath.Join(repo, devcontainerPath)
	if existing, err := setup.ReadIfExists(path); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", devcontainerPath)
	}

	body, err := devcontainerAssets.ReadFile("assets/" + string(lang) + ".json")
	if err != nil {
		return fmt.Errorf("read embedded asset for %q: %w", lang, err)
	}
	return setup.WriteAtomic(path, body, 0o644)
}

func (devcontainerRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, devcontainerPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", devcontainerPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", devcontainerPath)
	}
	return nil
}

func init() { setup.Register(devcontainerRecipe{}) }
