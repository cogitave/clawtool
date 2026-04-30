package sources

// devrelopers/shell-mcp (MIT) — sandbox-aware shell MCP server. Per-
// directory `.shell-mcp.toml` allowlist that walks up the tree like
// git, hard non-overridable denylist (sudo, rm -rf /, fork bombs),
// and rejection of every shell metacharacter before tokenisation.
// Upstream: https://github.com/devrelopers/shell-mcp
//
// Pair with rtk_rewrite for defense-in-depth: rtk rewrites loose Bash
// invocations into structured shell-mcp calls (rewrite layer); shell-
// mcp itself enforces sandbox boundaries + allowlist (sandbox layer).
// They are orthogonal — this recipe ships sandbox config only and
// does NOT touch the rtk_rewrite pre_tool_use rule.

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/shell-mcp.toml
var shellMcpTemplate []byte

// shellMcpConfigPath is the in-repo location the recipe writes to.
// Lives under .clawtool/ alongside the rest of clawtool's managed
// state. The operator can symlink or copy this to `.shell-mcp.toml`
// at the repo root once they are happy with the allowlist.
const shellMcpConfigPath = ".clawtool/shell-mcp.toml"

type shellMcpRecipe struct{}

func (shellMcpRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "shell-mcp",
		Category:    setup.CategoryRuntime,
		Description: "Drop a starter .clawtool/shell-mcp.toml for devrelopers/shell-mcp (sandbox-aware shell execution: per-dir TOML config, denies shell metachars, hard denylist). Recipe ships config only — operator installs and launches the shell-mcp binary themselves.",
		Upstream:    "https://github.com/devrelopers/shell-mcp",
		Stability:   setup.StabilityBeta,
		// Opt-in: shell sandbox is not part of every install.
		Core: false,
	}
}

func (shellMcpRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, shellMcpConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "shell-mcp.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "shell-mcp.toml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships config only. Installing the
// upstream `shell-mcp` binary (`cargo install shell-mcp`) is
// documented in the template's header and is the operator's job.
func (shellMcpRecipe) Prereqs() []setup.Prereq { return nil }

func (shellMcpRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, shellMcpConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", shellMcpConfigPath)
	}
	return setup.WriteAtomic(path, shellMcpTemplate, 0o644)
}

func (shellMcpRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, shellMcpConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", shellMcpConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", shellMcpConfigPath)
	}
	return nil
}

func init() { setup.Register(shellMcpRecipe{}) }
