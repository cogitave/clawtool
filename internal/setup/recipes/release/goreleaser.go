package release

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/goreleaser.yaml
var goreleaserConfig []byte

//go:embed assets/release-workflow.yml
var goreleaserWorkflow []byte

const (
	grConfigPath   = ".goreleaser.yaml"
	grWorkflowPath = ".github/workflows/release.yml"
)

type goreleaserRecipe struct{}

func (goreleaserRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "goreleaser",
		Category:    setup.CategoryRelease,
		Description: "GoReleaser config + GitHub Actions tag-triggered release workflow (cross-platform binaries, checksums, GitHub Release).",
		Upstream:    "https://goreleaser.com",
		Stability:   setup.StabilityStable,
	}
}

func (goreleaserRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	yamlExists, err := setup.FileExists(filepath.Join(repo, grConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	wfBody, err := setup.ReadIfExists(filepath.Join(repo, grWorkflowPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	switch {
	case !yamlExists && wfBody == nil:
		return setup.StatusAbsent, "goreleaser not configured", nil
	case yamlExists && wfBody != nil && setup.HasMarker(wfBody, setup.ManagedByMarker):
		return setup.StatusApplied, "goreleaser config + tag-triggered workflow installed", nil
	default:
		return setup.StatusPartial, "some goreleaser files exist but are not fully clawtool-managed; refusing to overwrite", nil
	}
}

// Prereqs: goreleaser binary needed for local snapshot builds. The
// CI workflow uses goreleaser/goreleaser-action which installs it
// in-runner, so the local binary is a "nice to have" not a hard
// requirement for Apply.
func (goreleaserRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{{
		Name: "goreleaser (for local `goreleaser release --snapshot` previews)",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath("goreleaser"); err != nil {
				return fmt.Errorf("goreleaser not on PATH")
			}
			return nil
		},
		Install: map[setup.Platform][]string{
			setup.PlatformDarwin: {"brew", "install", "goreleaser"},
			setup.PlatformLinux:  {"sh", "-c", "go install github.com/goreleaser/goreleaser/v2@latest"},
		},
		ManualHint: "Install GoReleaser per https://goreleaser.com/install for local snapshot builds; the CI workflow installs it in-runner anyway.",
	}}
}

func (goreleaserRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	// Workflow YAML — markable.
	wfPath := filepath.Join(repo, grWorkflowPath)
	if existing, err := setup.ReadIfExists(wfPath); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", grWorkflowPath)
	}
	// .goreleaser.yaml — markable too (YAML supports # comments).
	cfgPath := filepath.Join(repo, grConfigPath)
	if existing, err := setup.ReadIfExists(cfgPath); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", grConfigPath)
	}

	if err := setup.WriteAtomic(cfgPath, goreleaserConfig, 0o644); err != nil {
		return err
	}
	if err := setup.WriteAtomic(wfPath, goreleaserWorkflow, 0o644); err != nil {
		return err
	}
	return nil
}

func (goreleaserRecipe) Verify(_ context.Context, repo string) error {
	for _, rel := range []string{grConfigPath, grWorkflowPath} {
		b, err := setup.ReadIfExists(filepath.Join(repo, rel))
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}
		if b == nil {
			return fmt.Errorf("verify: %s missing", rel)
		}
		if !setup.HasMarker(b, setup.ManagedByMarker) {
			return fmt.Errorf("verify: clawtool marker missing in %s", rel)
		}
	}
	return nil
}

func init() { setup.Register(goreleaserRecipe{}) }
