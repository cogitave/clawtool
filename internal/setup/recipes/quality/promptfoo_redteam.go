package quality

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/promptfooconfig.yaml
var promptfooConfig []byte

const promptfooConfigPath = "promptfooconfig.yaml"

// promptfooRedteamRecipe wraps promptfoo (https://github.com/promptfoo/promptfoo,
// ~20.7k★) — an LLM eval / red-team harness — by dropping a baseline
// promptfooconfig.yaml that drives every clawtool agent family
// through the BIAM dispatch path (`clawtool send --agent <family>`).
//
// Recipe ships config only; the operator runs `promptfoo redteam run`
// themselves. Probe text is referenced by canonical nickname so no
// copyrighted jailbreak corpus is embedded in clawtool's source.
type promptfooRedteamRecipe struct{}

func (promptfooRedteamRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "promptfoo-redteam",
		Category:    setup.CategoryQuality,
		Description: "Promptfoo redteam baseline that evaluates clawtool's BIAM-dispatched agent families against jailbreak / prompt-injection probes.",
		Upstream:    "https://github.com/promptfoo/promptfoo",
		Stability:   setup.StabilityBeta,
		// Core even though Beta — operator wants jailbreak-eval
		// scaffolding by default. Beta status reflects the recipe's
		// soak time, not its desirability.
		Core: true,
	}
}

func (promptfooRedteamRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, promptfooConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "promptfooconfig.yaml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "promptfooconfig.yaml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is intentionally empty: the recipe ships config only.
// Operators install promptfoo themselves (`npm i -g promptfoo` or
// `npx promptfoo@latest …`); we don't gate Apply on its presence
// because the config is useful as a starting point even before
// promptfoo is on PATH.
func (promptfooRedteamRecipe) Prereqs() []setup.Prereq { return nil }

func (promptfooRedteamRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, promptfooConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", promptfooConfigPath)
	}
	return setup.WriteAtomic(path, promptfooConfig, 0o644)
}

func (promptfooRedteamRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, promptfooConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", promptfooConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", promptfooConfigPath)
	}
	return nil
}

func init() { setup.Register(promptfooRedteamRecipe{}) }
