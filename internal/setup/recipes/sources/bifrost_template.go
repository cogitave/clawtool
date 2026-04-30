// Package sources — recipes for AI source / gateway integrations.
//
// First entry: bifrost-template. maximhq/bifrost
// (https://github.com/maximhq/bifrost, Apache-2.0) is a Go-native
// AI gateway with unified failover, semantic caching, and budget
// governance. The portal layer registers a `bifrost` driver
// (internal/portal/bifrost.go) that surfaces in `clawtool portal
// list` as `bifrost (deferred)` until phase 2 lands the
// bifrost/core dependency behind the `clawtool_bifrost` build
// tag.
//
// This recipe ships the YAML config template the driver will read
// once phase 2 lands. Dropping it today is safe — nothing reads
// the file yet, but operators get a head start on writing their
// provider chain + cache settings.
package sources

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/bifrost.yaml.template
var bifrostTemplate []byte

// bifrostTemplatePath is project-scoped under .clawtool/ — same
// directory the rules engine and other clawtool config live in,
// so a project sweep (`rg managed-by:.clawtool`) finds it.
const bifrostTemplatePath = ".clawtool/bifrost.yaml.template"

type bifrostTemplateRecipe struct{}

func (bifrostTemplateRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "bifrost-template",
		Category:    setup.CategoryAgents,
		Description: "Bifrost AI-gateway config template — provider chain, semantic cache, daily budget cap (phase 2 reads it).",
		Upstream:    "https://github.com/maximhq/bifrost",
		Stability:   setup.StabilityExperimental,
		// Not Core: phase 1 is foundation-only (no runtime
		// dep), so the recipe is opt-in until phase 2 lands
		// the bifrost/core build-tagged adapter.
		Core: false,
	}
}

func (bifrostTemplateRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, bifrostTemplatePath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, ".clawtool/bifrost.yaml.template not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, ".clawtool/bifrost.yaml.template exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is intentionally empty: phase-1 ships the template
// file only — bifrost/core is not yet a Go dependency, and
// running the bifrost binary at the operator's edge isn't
// required until phase 2.
func (bifrostTemplateRecipe) Prereqs() []setup.Prereq { return nil }

func (bifrostTemplateRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, bifrostTemplatePath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", bifrostTemplatePath)
	}
	return setup.WriteAtomic(path, bifrostTemplate, 0o644)
}

func (bifrostTemplateRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, bifrostTemplatePath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", bifrostTemplatePath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", bifrostTemplatePath)
	}
	return nil
}

func init() { setup.Register(bifrostTemplateRecipe{}) }
