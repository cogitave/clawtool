// Package commits hosts recipes for the `commits` category — commit-
// time discipline. The first recipe drops a Conventional Commits PR
// title check workflow so merges into the default branch require
// well-formed commit/PR titles.
package commits

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/commit-format.yml
var commitFormatYML []byte

const commitFormatPath = ".github/workflows/commit-format.yml"

// commitFormatCIRecipe drops a GitHub Actions workflow that fails
// the PR check unless the title matches the Conventional Commits
// 1.0 grammar. Wraps amannn/action-semantic-pull-request — the
// canonical, maintained Action for this job — so we never reinvent
// the parser.
type commitFormatCIRecipe struct{}

func (commitFormatCIRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "conventional-commits-ci",
		Category:    setup.CategoryCommits,
		Description: "GitHub Actions workflow that fails PRs whose titles aren't Conventional Commits.",
		Upstream:    "https://github.com/amannn/action-semantic-pull-request",
		Stability:   setup.StabilityStable,
		// Core: every shipping repo wants Conventional Commits CI
		// the moment it has a .github/ directory.
		Core: true,
	}
}

func (commitFormatCIRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	path := filepath.Join(repo, commitFormatPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, ".github/workflows/commit-format.yml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "workflow file exists but is not clawtool-managed; Apply will refuse to overwrite without force", nil
}

func (commitFormatCIRecipe) Prereqs() []setup.Prereq { return nil }

func (commitFormatCIRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, commitFormatPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if b != nil && !setup.HasMarker(b, setup.ManagedByMarker) && !setup.IsForced(opts) {
		// Conservative: refuse to overwrite a file we didn't
		// write. The wizard surfaces this as a partial-applied
		// status; a future --force flag (v0.10) overrides.
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", commitFormatPath)
	}
	return setup.WriteAtomic(path, commitFormatYML, 0o644)
}

func (commitFormatCIRecipe) Verify(_ context.Context, repo string) error {
	path := filepath.Join(repo, commitFormatPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", commitFormatPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", commitFormatPath)
	}
	return nil
}

func init() { setup.Register(commitFormatCIRecipe{}) }
