// Package sources hosts recipes that wire up MCP source servers
// whose configuration ships as a templated file in the repo. The
// canonical example is googleapis/mcp-toolbox (Apache-2.0): a DB
// MCP server that reads a tools.yaml describing one or more
// database sources + parameterised SQL tools.
//
// Recipes here ship config only — the operator installs the
// upstream binary themselves. Category routes through CategoryRuntime
// because DB integration is dev-environment scaffolding.
package sources

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/tools.yaml
var mcpToolboxTemplate []byte

// mcpToolboxConfigPath is the in-repo location the recipe writes to.
// Lives under .clawtool/ so it sits next to the rest of clawtool's
// managed state and is easy to .gitignore selectively.
const mcpToolboxConfigPath = ".clawtool/mcp-toolbox/tools.yaml"

// mcpToolboxRecipe wraps googleapis/mcp-toolbox — Google's reference
// DB MCP server with built-in support for Postgres / MySQL / SQLite /
// BigQuery / Mongo / Redis / Spanner. The recipe drops a starter
// tools.yaml with commented-out Postgres + SQLite source declarations
// so the operator just fills in their connection string.
type mcpToolboxRecipe struct{}

func (mcpToolboxRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "mcp-toolbox",
		Category:    setup.CategoryRuntime,
		Description: "Drop a starter tools.yaml for googleapis/mcp-toolbox (DB MCP server: Postgres + SQLite templates). Recipe ships config only — operator installs and launches the toolbox binary themselves.",
		Upstream:    "https://github.com/googleapis/mcp-toolbox",
		Stability:   setup.StabilityBeta,
		// Opt-in: DB integration is not part of every install.
		Core: false,
	}
}

func (mcpToolboxRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, mcpToolboxConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "tools.yaml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "tools.yaml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships config only. Installing the
// upstream `toolbox` binary is documented in tools.yaml's header
// and is the operator's responsibility.
func (mcpToolboxRecipe) Prereqs() []setup.Prereq { return nil }

func (mcpToolboxRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, mcpToolboxConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", mcpToolboxConfigPath)
	}
	return setup.WriteAtomic(path, mcpToolboxTemplate, 0o644)
}

func (mcpToolboxRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, mcpToolboxConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", mcpToolboxConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", mcpToolboxConfigPath)
	}
	return nil
}

func init() { setup.Register(mcpToolboxRecipe{}) }
