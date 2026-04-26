package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// RepoConfigName is the canonical filename. Lives in the repo root,
// committed to git. Keeps the user-global config (~/.config/clawtool)
// uncoupled from per-repo state.
const RepoConfigName = ".clawtool.toml"

// RepoConfig is the on-disk record of which recipes were applied to
// this repo. Recipes are stored flat (queryable, easy to diff) but
// the wizard groups them by category for human reading.
type RepoConfig struct {
	Clawtool ClawtoolMeta  `toml:"clawtool"`
	Recipes  []RecipeEntry `toml:"recipe,omitempty"`
}

// ClawtoolMeta is the toolchain stamp. Helps future migrations know
// which schema version they're reading.
type ClawtoolMeta struct {
	// Version is the clawtool semver that wrote this file.
	Version string `toml:"version"`
}

// RecipeEntry is one applied-recipe row. Schema is forward-only —
// new optional fields are free to add; renames need a migration.
type RecipeEntry struct {
	Name            string         `toml:"name"`
	Category        Category       `toml:"category"`
	AppliedAt       time.Time      `toml:"applied_at"`
	UpstreamVersion string         `toml:"upstream_version,omitempty"`
	Options         map[string]any `toml:"options,omitempty"`
}

// LoadRepoConfig reads .clawtool.toml from repoRoot. A missing file
// returns an empty config with no error — Apply paths can call
// AppendRecipe + Save without an explicit init step.
func LoadRepoConfig(repoRoot string) (*RepoConfig, error) {
	path := filepath.Join(repoRoot, RepoConfigName)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &RepoConfig{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c RepoConfig
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes c to repoRoot/.clawtool.toml atomically (temp+rename).
// Mode 0644 — the file is meant to be committed.
func (c *RepoConfig) Save(repoRoot string) error {
	if strings.TrimSpace(c.Clawtool.Version) == "" {
		return errors.New("RepoConfig.Clawtool.Version must be set before Save")
	}
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", repoRoot, err)
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	path := filepath.Join(repoRoot, RepoConfigName)
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// HasRecipe reports whether a recipe with the given name has been
// recorded as applied. Wizard uses this to decide which checkboxes
// to pre-check.
func (c *RepoConfig) HasRecipe(name string) bool {
	for _, r := range c.Recipes {
		if r.Name == name {
			return true
		}
	}
	return false
}

// FindRecipe returns the entry for name, or nil.
func (c *RepoConfig) FindRecipe(name string) *RecipeEntry {
	for i := range c.Recipes {
		if c.Recipes[i].Name == name {
			return &c.Recipes[i]
		}
	}
	return nil
}

// UpsertRecipe records that recipe `name` was applied. If an entry
// already exists, AppliedAt is refreshed and Options/UpstreamVersion
// are replaced. If absent, a new entry is appended in alpha order.
func (c *RepoConfig) UpsertRecipe(entry RecipeEntry) {
	if entry.AppliedAt.IsZero() {
		entry.AppliedAt = time.Now().UTC()
	}
	if existing := c.FindRecipe(entry.Name); existing != nil {
		*existing = entry
		return
	}
	// Insert in name order so diffs stay clean.
	idx := len(c.Recipes)
	for i, r := range c.Recipes {
		if r.Name > entry.Name {
			idx = i
			break
		}
	}
	c.Recipes = append(c.Recipes, RecipeEntry{})
	copy(c.Recipes[idx+1:], c.Recipes[idx:])
	c.Recipes[idx] = entry
}

// RemoveRecipe drops the entry with name from c. No-op if absent.
func (c *RepoConfig) RemoveRecipe(name string) {
	for i, r := range c.Recipes {
		if r.Name == name {
			c.Recipes = append(c.Recipes[:i], c.Recipes[i+1:]...)
			return
		}
	}
}

// RecipesByCategory groups c.Recipes by Category in walk order.
// Empty categories are not present in the result. Used by
// recipe_status MCP tool and the wizard's "already applied" header.
func (c *RepoConfig) RecipesByCategory() map[Category][]RecipeEntry {
	out := map[Category][]RecipeEntry{}
	for _, r := range c.Recipes {
		out[r.Category] = append(out[r.Category], r)
	}
	return out
}
