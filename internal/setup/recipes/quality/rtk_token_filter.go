// Package quality — rtk-token-filter recipe.
//
// rtk (https://github.com/rtk-ai/rtk, ~38k★, Apache-2.0) is a CLI
// proxy that compresses common Bash command output before it hits
// the LLM context — measured 60-90% token savings on `git status`,
// `ls -R`, `grep`, etc. This recipe ships the project-local
// allowlist (`<repo>/.clawtool/rtk-rewrite-list.toml`) that the
// `internal/rules` pre_tool_use rewrite helper consults when a
// Bash dispatch matches.
//
// Recipe ships config only. Operators install rtk themselves
// (`cargo install rtk` or platform packages); the rule helper
// detects rtk's presence on PATH and no-ops when it's missing, so
// the recipe is safe to apply regardless of install state.
//
// TOML format (documented inline in the asset too):
//
//	description = "..."
//	commands    = ["git", "ls", "grep", ...]
//
// First-token-of-Bash matching only — no flags, no paths.
package quality

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/rtk-rewrite-list.toml
var rtkRewriteListConfig []byte

// rtkRewriteListPath is project-scoped under .clawtool/ — same
// directory the rules engine and other clawtool config live in,
// so a project sweep (`rg managed-by:.clawtool`) finds it.
const rtkRewriteListPath = ".clawtool/rtk-rewrite-list.toml"

type rtkTokenFilterRecipe struct{}

func (rtkTokenFilterRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "rtk-token-filter",
		Category:    setup.CategoryQuality,
		Description: "rtk CLI proxy allowlist — names which Bash commands the pre_tool_use rewrite rule pipes through rtk for 60-90% output-compression token savings.",
		Upstream:    "https://github.com/rtk-ai/rtk",
		Stability:   setup.StabilityBeta,
		// Core even though Beta — the per-call token savings are
		// large enough that the operator wants this on by default
		// for any new project. Beta status reflects the recipe's
		// soak time, not the cost / benefit ratio.
		Core: true,
	}
}

func (rtkTokenFilterRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, rtkRewriteListPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, ".clawtool/rtk-rewrite-list.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, ".clawtool/rtk-rewrite-list.toml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is intentionally empty: the recipe ships config only.
// Operators install rtk via the upstream's recommended path
// (`cargo install rtk`, prebuilt release, or distro package); we
// don't gate Apply on its presence because the rewrite helper
// detects rtk-on-PATH at dispatch time and silently skips when
// absent — so the recipe is safe to apply even on a host without
// rtk yet.
func (rtkTokenFilterRecipe) Prereqs() []setup.Prereq { return nil }

func (rtkTokenFilterRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, rtkRewriteListPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", rtkRewriteListPath)
	}
	return setup.WriteAtomic(path, rtkRewriteListConfig, 0o644)
}

func (rtkTokenFilterRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, rtkRewriteListPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", rtkRewriteListPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", rtkRewriteListPath)
	}
	return nil
}

func init() { setup.Register(rtkTokenFilterRecipe{}) }
