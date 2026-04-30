package sources

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/semble_config.yaml
var sembleTemplate []byte

// sembleConfigPath is the in-repo location the recipe writes to.
// Lives under .clawtool/ next to the rest of clawtool's managed
// state. Semble itself reads no config from this file — it's a
// marker + workflow note for the operator.
const sembleConfigPath = ".clawtool/semble/config.yaml"

// sembleRecipe wraps MinishLab/semble (MIT): a code-search MCP
// server that returns relevant chunks instead of grep+read output,
// cutting token cost ~98% on retrieval. The recipe ships a config
// marker only; the operator installs uv themselves and clawtool
// spawns `uvx --from "semble[mcp]" semble` per the catalog entry.
type sembleRecipe struct{}

func (sembleRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "semble",
		Category:    setup.CategoryRuntime,
		Description: "Drop a starter .clawtool/semble/config.yaml marker for MinishLab/semble (code-search MCP server, ~98% fewer tokens than grep+read). Recipe ships config only — operator installs uv and clawtool spawns uvx --from \"semble[mcp]\" semble per the catalog entry.",
		Upstream:    "https://github.com/MinishLab/semble",
		Stability:   setup.StabilityBeta,
		// Opt-in: code-search retrieval is not part of every install.
		Core: false,
	}
}

func (sembleRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, sembleConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "config.yaml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "config.yaml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships config only. uv installation
// is documented in the asset's header and is the operator's job.
func (sembleRecipe) Prereqs() []setup.Prereq { return nil }

func (sembleRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, sembleConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", sembleConfigPath)
	}
	return setup.WriteAtomic(path, sembleTemplate, 0o644)
}

func (sembleRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, sembleConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", sembleConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", sembleConfigPath)
	}
	return nil
}

func init() { setup.Register(sembleRecipe{}) }
