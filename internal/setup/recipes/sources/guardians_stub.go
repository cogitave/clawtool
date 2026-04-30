// guardians-stub recipe — drops a pre_send rule template that
// exercises clawtool's `guardians_check(plan_arg)` predicate.
//
// metareflection/guardians (https://github.com/metareflection/guardians,
// MIT) implements Erik Meijer's "Guardians of the Agents: Formal
// Verification of AI Workflows" (CACM, January 2026): taint-tracking
// + Z3-SAT verification of an agent's drafted plan before SendMessage
// dispatches. Phase 1 (this commit) ships:
//
//   - the `guardians_check(plan_arg)` predicate in internal/rules,
//     wired as an always-true stub so the surface contract exists;
//   - this recipe, which drops a sample rules.toml fragment under
//     .clawtool/rules/ that operators can fold into their main
//     rules file or load directly.
//
// Phase 2 will flip the predicate to invoke the Z3-SAT engine,
// gated behind a `clawtool_guardians` build tag so the Z3 cgo
// dependency stays opt-in. The rule shape doesn't change between
// phases — the same `guardians_check("plan")` call routes through
// the stub today and the real engine tomorrow.
package sources

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/guardians-stub.toml
var guardiansStubTemplate []byte

// guardiansStubPath is project-scoped under .clawtool/rules/ —
// same convention as the rest of the rules engine. Operators load
// it via their main rules.toml `include` (when that lands) or by
// concatenating it into the project rules file today.
const guardiansStubPath = ".clawtool/rules/guardians-stub.toml"

type guardiansStubRecipe struct{}

func (guardiansStubRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "guardians-stub",
		Category:    setup.CategoryAgents,
		Description: "Pre-send taint+Z3 plan verification rule template (metareflection/guardians, phase-1 stub).",
		Upstream:    "https://github.com/metareflection/guardians",
		Stability:   setup.StabilityExperimental,
		// Not Core: phase-1 ships the rule template only — the
		// predicate is a stub, so the recipe stays opt-in until
		// phase-2 lands the Z3 engine behind a build tag.
		Core: false,
	}
}

func (guardiansStubRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, guardiansStubPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, ".clawtool/rules/guardians-stub.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, ".clawtool/rules/guardians-stub.toml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is intentionally empty: phase-1 ships the rule template
// only — Z3 / cgo isn't a dependency yet, and the predicate is a
// pure-Go stub registered alongside the rest of the rule engine.
func (guardiansStubRecipe) Prereqs() []setup.Prereq { return nil }

func (guardiansStubRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, guardiansStubPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", guardiansStubPath)
	}
	return setup.WriteAtomic(path, guardiansStubTemplate, 0o644)
}

func (guardiansStubRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, guardiansStubPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", guardiansStubPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", guardiansStubPath)
	}
	return nil
}

func init() { setup.Register(guardiansStubRecipe{}) }
