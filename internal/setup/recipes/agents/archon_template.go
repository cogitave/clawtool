// Package agents holds setup recipes that integrate external AI-
// agent harnesses (Archon, future entrants) with clawtool's
// playbook layer. Phase 1 ships exactly one recipe — archon-template —
// which drops a sample workflow at .archon/workflows/idea-to-pr.yaml
// and lets `clawtool playbook list-archon` discover it.
package agents

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/idea-to-pr.yaml
var ideaToPRTemplate []byte

// archonTemplatePath is the on-disk location the recipe writes /
// detects. Path is relative to the repo root passed into Apply /
// Detect / Verify; the directory is auto-created if missing.
const archonTemplatePath = ".archon/workflows/idea-to-pr.yaml"

// archonTemplateRecipe wraps coleam00/Archon (~20.3k★, MIT) by
// dropping a sample DAG-workflow at .archon/workflows/idea-to-pr.yaml
// that exercises every Archon node kind clawtool's phase-1 loader
// understands (prompt + bash + loop). The recipe ships YAML only;
// execution lives in Archon's own CLI, and phase 2 of clawtool will
// add a parallel `clawtool playbook run` path.
//
// Stability: Beta because the Archon schema isn't tagged upstream
// and may shift; the loader tags unrecognised node kinds rather
// than erroring, so a schema bump never breaks `clawtool` startup.
// Core: false — operators opt in explicitly.
type archonTemplateRecipe struct{}

func (archonTemplateRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "archon-template",
		Category:    setup.CategoryAgents,
		Description: "Sample Archon DAG workflow at .archon/workflows/idea-to-pr.yaml; surfaces via `clawtool playbook list-archon`.",
		Upstream:    "https://github.com/coleam00/Archon",
		Stability:   setup.StabilityBeta,
		Core:        false,
	}
}

func (archonTemplateRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, archonTemplatePath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, archonTemplatePath + " not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, archonTemplatePath + " exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships YAML only. The operator
// installs Archon themselves (`bun install` or the standalone
// binary) when they're ready to execute the workflow. The sample
// is useful as a starting point even before Archon is on PATH.
func (archonTemplateRecipe) Prereqs() []setup.Prereq { return nil }

func (archonTemplateRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, archonTemplatePath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", archonTemplatePath)
	}
	return setup.WriteAtomic(path, ideaToPRTemplate, 0o644)
}

func (archonTemplateRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, archonTemplatePath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", archonTemplatePath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", archonTemplatePath)
	}
	return nil
}

func init() { setup.Register(archonTemplateRecipe{}) }
