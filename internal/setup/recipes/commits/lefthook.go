package commits

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/lefthook.yml
var lefthookYML []byte

//go:embed assets/commitlintrc.json
var commitlintJSON []byte

const (
	lefthookPath   = "lefthook.yml"
	commitlintPath = ".commitlintrc.json"
)

// lefthookRecipe wires Conventional Commits enforcement locally
// via lefthook (the polyglot Go-binary Git-hook manager) + a
// commitlint config it shells out to. Pairs with the existing
// conventional-commits-ci recipe — CI is the gate, lefthook is
// the early warning.
type lefthookRecipe struct{}

func (lefthookRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "lefthook",
		Category:    setup.CategoryCommits,
		Description: "lefthook (polyglot Git hooks) + commitlint config — rejects bad commit subjects before they reach CI.",
		Upstream:    "https://github.com/evilmartians/lefthook",
		Stability:   setup.StabilityStable,
	}
}

func (lefthookRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	lhB, err := setup.ReadIfExists(filepath.Join(repo, lefthookPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	clExists, err := setup.FileExists(filepath.Join(repo, commitlintPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	switch {
	case lhB == nil && !clExists:
		return setup.StatusAbsent, "lefthook + commitlint not configured", nil
	case lhB != nil && setup.HasMarker(lhB, setup.ManagedByMarker) && clExists:
		return setup.StatusApplied, "lefthook.yml + .commitlintrc.json present (lefthook clawtool-managed)", nil
	default:
		return setup.StatusPartial, "some commit-hook files exist but aren't fully clawtool-managed; refusing to overwrite", nil
	}
}

// Prereqs surfaces lefthook + npx (commitlint runs via npx). Both
// soft — the recipe's Apply just drops config; install offers
// land via the wizard's prereq prompter.
func (lefthookRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{
		{
			Name: "lefthook binary (for `lefthook install`)",
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("lefthook"); err != nil {
					return fmt.Errorf("lefthook not on PATH")
				}
				return nil
			},
			Install: map[setup.Platform][]string{
				setup.PlatformDarwin: {"brew", "install", "lefthook"},
				setup.PlatformLinux:  {"sh", "-c", "go install github.com/evilmartians/lefthook@latest"},
			},
			ManualHint: "Install lefthook per https://lefthook.dev/installation/. After Apply, run `lefthook install` once to activate the hooks in this repo.",
		},
		{
			Name: "npx (Node.js, for commitlint)",
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("npx"); err != nil {
					return fmt.Errorf("npx not on PATH")
				}
				return nil
			},
			Install: map[setup.Platform][]string{
				setup.PlatformDarwin: {"brew", "install", "node"},
				setup.PlatformLinux:  {"sh", "-c", "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt install -y nodejs"},
			},
			ManualHint: "Install Node.js 18+ from https://nodejs.org so the commit-msg hook can call `npx commitlint`. The hook degrades gracefully if npx isn't found.",
		},
	}
}

func (lefthookRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	lhPath := filepath.Join(repo, lefthookPath)
	if existing, err := setup.ReadIfExists(lhPath); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite (pass force=true to override)", lefthookPath)
	}
	clPath := filepath.Join(repo, commitlintPath)
	if exists, err := setup.FileExists(clPath); err != nil {
		return err
	} else if exists && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists; clawtool refuses to overwrite an existing commitlint config (pass force=true to override)", commitlintPath)
	}
	if err := setup.WriteAtomic(lhPath, lefthookYML, 0o644); err != nil {
		return err
	}
	return setup.WriteAtomic(clPath, commitlintJSON, 0o644)
}

func (lefthookRecipe) Verify(_ context.Context, repo string) error {
	lhB, err := setup.ReadIfExists(filepath.Join(repo, lefthookPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if lhB == nil {
		return fmt.Errorf("verify: %s missing", lefthookPath)
	}
	if !setup.HasMarker(lhB, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", lefthookPath)
	}
	if exists, _ := setup.FileExists(filepath.Join(repo, commitlintPath)); !exists {
		return fmt.Errorf("verify: %s missing", commitlintPath)
	}
	return nil
}

func init() { setup.Register(lefthookRecipe{}) }
