// Package quality hosts recipes for the `quality` category — code
// quality enforcement (lint, format, test scaffolds). Two recipes
// open the category in v0.10: prettier (cross-language formatter)
// and golangci-lint (Go-specific meta-linter).
package quality

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/prettierrc.json
var prettierConfig []byte

//go:embed assets/prettierignore
var prettierIgnore []byte

//go:embed assets/golangci.yml
var golangciConfig []byte

const (
	prettierConfigPath  = ".prettierrc.json"
	prettierIgnorePath  = ".prettierignore"
	golangciConfigPath  = ".golangci.yml"
	prettierMarkerToken = "// managed-by: clawtool"
)

// ── prettier ───────────────────────────────────────────────────────

type prettierRecipe struct{}

func (prettierRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "prettier",
		Category:    setup.CategoryQuality,
		Description: "Cross-language formatter (JS/TS/JSON/Markdown/YAML/CSS). Drops .prettierrc.json + .prettierignore.",
		Upstream:    "https://prettier.io",
		Stability:   setup.StabilityStable,
	}
}

// Detect treats both files as a unit. Applied iff both exist with
// markers (where applicable — JSON config can't carry a marker, so
// we treat its presence as ours when .prettierignore carries the
// marker).
func (prettierRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	cfgExists, err := setup.FileExists(filepath.Join(repo, prettierConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	ign, err := setup.ReadIfExists(filepath.Join(repo, prettierIgnorePath))
	if err != nil {
		return setup.StatusError, "", err
	}
	switch {
	case !cfgExists && ign == nil:
		return setup.StatusAbsent, "no prettier config in repo", nil
	case cfgExists && ign != nil && setup.HasMarker(ign, setup.ManagedByMarker):
		return setup.StatusApplied, "prettier config + ignore both clawtool-managed", nil
	default:
		return setup.StatusPartial, "prettier files present but not fully clawtool-managed; refusing to overwrite without a clean slate", nil
	}
}

// Prereqs surfaces npx for local `npx prettier --check` previews.
func (prettierRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{{
		Name: "npx (Node.js, for local `npx prettier --check`)",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath("npx"); err != nil {
				return fmt.Errorf("npx not on PATH")
			}
			return nil
		},
		Install: map[setup.Platform][]string{
			setup.PlatformDarwin: {"brew", "install", "node"},
			setup.PlatformLinux:  {"sh", "-c", "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt install -y nodejs"},
		},
		ManualHint: "Install Node.js 18+ from https://nodejs.org so you can run prettier locally; Prettier itself doesn't need to be globally installed (use `npx prettier`).",
	}}
}

func (prettierRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	cfgPath := filepath.Join(repo, prettierConfigPath)
	if exists, err := setup.FileExists(cfgPath); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%s exists; clawtool refuses to overwrite an existing prettier config (delete it first if you meant to reset)", prettierConfigPath)
	}
	ignPath := filepath.Join(repo, prettierIgnorePath)
	if existing, err := setup.ReadIfExists(ignPath); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", prettierIgnorePath)
	}
	if err := setup.WriteAtomic(cfgPath, prettierConfig, 0o644); err != nil {
		return err
	}
	return setup.WriteAtomic(ignPath, prettierIgnore, 0o644)
}

func (prettierRecipe) Verify(_ context.Context, repo string) error {
	for _, rel := range []string{prettierConfigPath, prettierIgnorePath} {
		exists, err := setup.FileExists(filepath.Join(repo, rel))
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}
		if !exists {
			return fmt.Errorf("verify: %s missing", rel)
		}
	}
	ign, _ := setup.ReadIfExists(filepath.Join(repo, prettierIgnorePath))
	if !setup.HasMarker(ign, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", prettierIgnorePath)
	}
	return nil
}

// ── golangci-lint ──────────────────────────────────────────────────

type golangciRecipe struct{}

func (golangciRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "golangci-lint",
		Category:    setup.CategoryQuality,
		Description: "golangci-lint v2 config — conservative defaults across errcheck/govet/staticcheck/gosec/revive + gofmt/goimports formatters.",
		Upstream:    "https://golangci-lint.run",
		Stability:   setup.StabilityStable,
	}
}

func (golangciRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, golangciConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		// Soft hint if there's no go.mod so the wizard can suggest
		// skipping rather than installing on the wrong stack.
		if exists, _ := setup.FileExists(filepath.Join(repo, "go.mod")); !exists {
			return setup.StatusAbsent, "no go.mod in repo — golangci-lint targets Go projects only", nil
		}
		return setup.StatusAbsent, ".golangci.yml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, ".golangci.yml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs: the golangci-lint binary for local lint preview.
func (golangciRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{{
		Name: "golangci-lint binary (for local `golangci-lint run` previews)",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath("golangci-lint"); err != nil {
				return fmt.Errorf("golangci-lint not on PATH")
			}
			return nil
		},
		Install: map[setup.Platform][]string{
			setup.PlatformDarwin: {"brew", "install", "golangci-lint"},
			setup.PlatformLinux:  {"sh", "-c", "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"},
		},
		ManualHint: "Install golangci-lint per https://golangci-lint.run/welcome/install/. CI users typically install it in-runner; the local binary is only needed for `golangci-lint run` previews.",
	}}
}

func (golangciRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	if exists, _ := setup.FileExists(filepath.Join(repo, "go.mod")); !exists {
		return fmt.Errorf("no go.mod in %s; golangci-lint targets Go projects only", repo)
	}
	path := filepath.Join(repo, golangciConfigPath)
	if existing, err := setup.ReadIfExists(path); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", golangciConfigPath)
	}
	return setup.WriteAtomic(path, golangciConfig, 0o644)
}

func (golangciRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, golangciConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", golangciConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", golangciConfigPath)
	}
	return nil
}

func init() {
	setup.Register(prettierRecipe{})
	setup.Register(golangciRecipe{})
}
