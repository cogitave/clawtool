package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingRepoConfigReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatalf("LoadRepoConfig on empty dir: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil config")
	}
	if len(c.Recipes) != 0 {
		t.Errorf("missing file should produce empty Recipes, got %d", len(c.Recipes))
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := &RepoConfig{
		Clawtool: ClawtoolMeta{Version: "0.9.0"},
		Recipes: []RecipeEntry{
			{
				Name:            "license",
				Category:        CategoryGovernance,
				AppliedAt:       time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC),
				UpstreamVersion: "MIT-1.0",
			},
		},
	}
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Clawtool.Version != "0.9.0" {
		t.Errorf("Version: got %q want 0.9.0", got.Clawtool.Version)
	}
	if len(got.Recipes) != 1 || got.Recipes[0].Name != "license" {
		t.Errorf("Recipes round-trip failed: %+v", got.Recipes)
	}
	if got.Recipes[0].Category != CategoryGovernance {
		t.Errorf("Category lost: %q", got.Recipes[0].Category)
	}
}

func TestSaveRefusesEmptyVersion(t *testing.T) {
	dir := t.TempDir()
	c := &RepoConfig{}
	if err := c.Save(dir); err == nil {
		t.Fatal("Save should refuse empty Version")
	}
}

func TestUpsertInsertsInNameOrder(t *testing.T) {
	c := &RepoConfig{Clawtool: ClawtoolMeta{Version: "0.9.0"}}
	c.UpsertRecipe(RecipeEntry{Name: "release-please", Category: CategoryRelease})
	c.UpsertRecipe(RecipeEntry{Name: "codeowners", Category: CategoryGovernance})
	c.UpsertRecipe(RecipeEntry{Name: "license", Category: CategoryGovernance})
	c.UpsertRecipe(RecipeEntry{Name: "dependabot", Category: CategorySupplyChain})

	want := []string{"codeowners", "dependabot", "license", "release-please"}
	if len(c.Recipes) != len(want) {
		t.Fatalf("got %d recipes, want %d", len(c.Recipes), len(want))
	}
	for i, n := range want {
		if c.Recipes[i].Name != n {
			t.Errorf("position %d: got %q, want %q", i, c.Recipes[i].Name, n)
		}
	}
}

func TestUpsertReplacesExisting(t *testing.T) {
	c := &RepoConfig{Clawtool: ClawtoolMeta{Version: "0.9.0"}}
	first := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	c.UpsertRecipe(RecipeEntry{
		Name:            "release-please",
		Category:        CategoryRelease,
		AppliedAt:       first,
		UpstreamVersion: "v4.5.0",
	})

	second := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	c.UpsertRecipe(RecipeEntry{
		Name:            "release-please",
		Category:        CategoryRelease,
		AppliedAt:       second,
		UpstreamVersion: "v4.6.0",
	})

	if len(c.Recipes) != 1 {
		t.Fatalf("upsert should not add duplicate; got %d entries", len(c.Recipes))
	}
	if !c.Recipes[0].AppliedAt.Equal(second) {
		t.Errorf("AppliedAt not refreshed: got %v", c.Recipes[0].AppliedAt)
	}
	if c.Recipes[0].UpstreamVersion != "v4.6.0" {
		t.Errorf("UpstreamVersion not replaced: got %q", c.Recipes[0].UpstreamVersion)
	}
}

func TestUpsertSetsAppliedAtIfZero(t *testing.T) {
	c := &RepoConfig{Clawtool: ClawtoolMeta{Version: "0.9.0"}}
	c.UpsertRecipe(RecipeEntry{Name: "x", Category: CategoryRelease})
	if c.Recipes[0].AppliedAt.IsZero() {
		t.Error("UpsertRecipe should default AppliedAt to time.Now().UTC()")
	}
}

func TestRemoveRecipe(t *testing.T) {
	c := &RepoConfig{Clawtool: ClawtoolMeta{Version: "0.9.0"}}
	c.UpsertRecipe(RecipeEntry{Name: "a", Category: CategoryRelease})
	c.UpsertRecipe(RecipeEntry{Name: "b", Category: CategoryRelease})
	c.RemoveRecipe("a")
	if len(c.Recipes) != 1 || c.Recipes[0].Name != "b" {
		t.Errorf("Remove failed: %+v", c.Recipes)
	}
	c.RemoveRecipe("nonexistent")
	if len(c.Recipes) != 1 {
		t.Errorf("Remove of nonexistent should be no-op; got %+v", c.Recipes)
	}
}

func TestHasRecipe(t *testing.T) {
	c := &RepoConfig{}
	c.UpsertRecipe(RecipeEntry{Name: "license", Category: CategoryGovernance})
	if !c.HasRecipe("license") {
		t.Error("HasRecipe should return true for present recipe")
	}
	if c.HasRecipe("ghost") {
		t.Error("HasRecipe should return false for absent recipe")
	}
}

func TestRecipesByCategory(t *testing.T) {
	c := &RepoConfig{Clawtool: ClawtoolMeta{Version: "0.9.0"}}
	c.UpsertRecipe(RecipeEntry{Name: "license", Category: CategoryGovernance})
	c.UpsertRecipe(RecipeEntry{Name: "codeowners", Category: CategoryGovernance})
	c.UpsertRecipe(RecipeEntry{Name: "dependabot", Category: CategorySupplyChain})

	g := c.RecipesByCategory()
	if len(g[CategoryGovernance]) != 2 {
		t.Errorf("governance: got %d, want 2", len(g[CategoryGovernance]))
	}
	if len(g[CategorySupplyChain]) != 1 {
		t.Errorf("supply-chain: got %d, want 1", len(g[CategorySupplyChain]))
	}
	if _, ok := g[CategoryRuntime]; ok {
		t.Error("empty categories should not appear in the map")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	// Pre-populate the file so we can detect mid-write corruption.
	dir := t.TempDir()
	path := filepath.Join(dir, RepoConfigName)
	if err := os.WriteFile(path, []byte("[clawtool]\nversion = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &RepoConfig{Clawtool: ClawtoolMeta{Version: "0.9.0"}}
	c.UpsertRecipe(RecipeEntry{Name: "license", Category: CategoryGovernance})
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No leftover .new sidecar.
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Errorf(".new sidecar should be gone after rename, got err=%v", err)
	}
	got, err := LoadRepoConfig(dir)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.Clawtool.Version != "0.9.0" {
		t.Errorf("write got swallowed: %+v", got.Clawtool)
	}
}
