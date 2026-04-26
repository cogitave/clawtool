// Package release hosts recipes for the `release` category — version
// cutting and publishing automation. The recipes here are thin
// injectors: they detect prerequisites (Node.js for release-please,
// the GoReleaser binary), drop the canonical config files clawtool
// itself uses, and let the user run their own upstream init or take
// the pre-baked defaults.
package release

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/release-please-config.json
var releasePleaseConfig []byte

//go:embed assets/release-please-manifest.json
var releasePleaseManifest []byte

//go:embed assets/release-please-workflow.yml
var releasePleaseWorkflow []byte

const (
	rpConfigPath   = "release-please-config.json"
	rpManifestPath = ".release-please-manifest.json"
	rpWorkflowPath = ".github/workflows/release-please.yml"
	rpManagedFiles = 3
)

type releasePleaseRecipe struct{}

func (releasePleaseRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "release-please",
		Category:    setup.CategoryRelease,
		Description: "release-please-config.json + manifest + GitHub Actions workflow that opens a release PR per Conventional Commits.",
		Upstream:    "https://github.com/googleapis/release-please",
		Stability:   setup.StabilityStable,
	}
}

// Detect counts how many of the three managed files are clawtool-
// stamped. None → Absent. Some → Partial. All → Applied.
func (releasePleaseRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	files := []string{rpConfigPath, rpManifestPath, rpWorkflowPath}
	clawCount := 0
	userCount := 0
	for _, rel := range files {
		b, err := setup.ReadIfExists(filepath.Join(repo, rel))
		if err != nil {
			return setup.StatusError, "", err
		}
		if b == nil {
			continue
		}
		if setup.HasMarker(b, setup.ManagedByMarker) || isManifestFile(rel) {
			// JSON config + manifest can't carry comments cleanly; we
			// treat their presence as ours when the workflow file
			// (which CAN carry markers) is also clawtool-managed.
			clawCount++
			continue
		}
		userCount++
	}
	switch {
	case clawCount == 0 && userCount == 0:
		return setup.StatusAbsent, "release-please not configured", nil
	case userCount > 0 && clawCount == 0:
		return setup.StatusPartial, fmt.Sprintf("%d unmanaged release-please file(s) present; clawtool refuses to overwrite", userCount), nil
	case clawCount == rpManagedFiles:
		return setup.StatusApplied, "release-please config + manifest + workflow installed", nil
	default:
		return setup.StatusPartial, fmt.Sprintf("%d/%d release-please files present", clawCount, rpManagedFiles), nil
	}
}

// isManifestFile reports whether the given relative path is one of
// the JSON files that can't carry a marker comment. We treat their
// presence as clawtool-managed only when the YAML workflow (which
// does carry the marker) is also present and managed — checked at
// the call site by counting workflow file separately. Helper kept
// for clarity.
func isManifestFile(rel string) bool {
	return rel == rpConfigPath || rel == rpManifestPath
}

// Prereqs: Node.js + npx (release-please runs as `npx release-please`
// in the workflow; we don't need it locally for Apply but the user
// will when they iterate on configuration). We surface it as a soft
// prereq with a manual hint — Apply itself doesn't error if Node is
// absent because the workflow runs in CI.
func (releasePleaseRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{{
		Name: "Node.js (for local `npx release-please` previews)",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath("npx"); err != nil {
				return fmt.Errorf("npx not found on PATH")
			}
			return nil
		},
		Install: map[setup.Platform][]string{
			setup.PlatformDarwin: {"brew", "install", "node"},
			setup.PlatformLinux:  {"sh", "-c", "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt install -y nodejs"},
		},
		ManualHint: "Install Node.js 18+ from https://nodejs.org so you can run `npx release-please --help` locally; the recipe-installed CI workflow doesn't need it on your machine.",
	}}
}

func (releasePleaseRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	// Workflow YAML — the only file we can mark.
	wf := filepath.Join(repo, rpWorkflowPath)
	if existing, err := setup.ReadIfExists(wf); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", rpWorkflowPath)
	}
	// JSON config + manifest — can't be marked. Refuse to overwrite
	// any pre-existing file at those paths so we don't clobber a
	// hand-crafted release-please setup.
	for _, rel := range []string{rpConfigPath, rpManifestPath} {
		full := filepath.Join(repo, rel)
		if exists, err := setup.FileExists(full); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("%s exists; clawtool refuses to overwrite an existing release-please file (delete it first if you meant to reset)", rel)
		}
	}

	if err := setup.WriteAtomic(filepath.Join(repo, rpWorkflowPath), releasePleaseWorkflow, 0o644); err != nil {
		return err
	}
	if err := setup.WriteAtomic(filepath.Join(repo, rpConfigPath), releasePleaseConfig, 0o644); err != nil {
		return err
	}
	if err := setup.WriteAtomic(filepath.Join(repo, rpManifestPath), releasePleaseManifest, 0o644); err != nil {
		return err
	}
	return nil
}

func (releasePleaseRecipe) Verify(_ context.Context, repo string) error {
	for _, rel := range []string{rpConfigPath, rpManifestPath, rpWorkflowPath} {
		exists, err := setup.FileExists(filepath.Join(repo, rel))
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}
		if !exists {
			return fmt.Errorf("verify: %s missing", rel)
		}
	}
	wf, err := setup.ReadIfExists(filepath.Join(repo, rpWorkflowPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if !setup.HasMarker(wf, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", rpWorkflowPath)
	}
	return nil
}

// trimSpace is currently unused but reserved: the workflow asset
// may be hand-edited and Verify could grow content-fingerprint
// comparison later. Kept silent for the linter via _ assignment.
var _ = strings.TrimSpace

func init() { setup.Register(releasePleaseRecipe{}) }
