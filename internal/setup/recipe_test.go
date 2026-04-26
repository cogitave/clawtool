package setup

import (
	"context"
	"strings"
	"testing"
)

// fakeRecipe is the minimal valid Recipe used to drive registry tests.
type fakeRecipe struct {
	meta RecipeMeta
}

func (f fakeRecipe) Meta() RecipeMeta { return f.meta }
func (f fakeRecipe) Detect(context.Context, string) (Status, string, error) {
	return StatusAbsent, "", nil
}
func (f fakeRecipe) Prereqs() []Prereq                            { return nil }
func (f fakeRecipe) Apply(context.Context, string, Options) error { return nil }
func (f fakeRecipe) Verify(context.Context, string) error         { return nil }

func newFake(name string, cat Category) fakeRecipe {
	return fakeRecipe{meta: RecipeMeta{
		Name:        name,
		Category:    cat,
		Description: "test recipe",
		Upstream:    "https://example.com/" + name,
	}}
}

func TestCategoriesAreFrozen(t *testing.T) {
	got := Categories()
	want := []Category{
		CategoryGovernance,
		CategoryCommits,
		CategoryRelease,
		CategoryCI,
		CategoryQuality,
		CategorySupplyChain,
		CategoryKnowledge,
		CategoryAgents,
		CategoryRuntime,
	}
	if len(got) != len(want) {
		t.Fatalf("Categories(): got %d categories, want %d — taxonomy is frozen at v1.0", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Categories()[%d] = %q, want %q (order matters — repo-maturity walk)", i, got[i], want[i])
		}
	}
}

func TestCategoryDescriptionsCoverAll(t *testing.T) {
	desc := CategoryDescriptions()
	for _, c := range Categories() {
		if d, ok := desc[c]; !ok || strings.TrimSpace(d) == "" {
			t.Errorf("category %q has no description", c)
		}
	}
}

func TestCategoryValid(t *testing.T) {
	for _, c := range Categories() {
		if !c.Valid() {
			t.Errorf("Categories() returned %q but Valid() said no", c)
		}
	}
	if Category("not-a-category").Valid() {
		t.Error("unknown category should not be Valid()")
	}
}

func TestCategoryMustValidPanicsOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustValid should panic on unknown category")
		}
	}()
	Category("nope").MustValid()
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest()
	r := newFake("license", CategoryGovernance)
	Register(r)

	got := Lookup("license")
	if got == nil {
		t.Fatal("Lookup returned nil after Register")
	}
	if got.Meta().Name != "license" {
		t.Errorf("Lookup returned wrong recipe: %+v", got.Meta())
	}
	if Lookup("ghost") != nil {
		t.Error("Lookup should return nil for unknown recipe")
	}
}

func TestRegisterRefusesEmptyUpstream(t *testing.T) {
	resetForTest()
	r := fakeRecipe{meta: RecipeMeta{
		Name:        "scratch",
		Category:    CategoryRelease,
		Description: "tries to skip wrap-don't-reinvent",
		Upstream:    "",
	}}
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for empty Upstream")
		}
		msg, _ := v.(string)
		if !strings.Contains(msg, "Upstream") {
			t.Errorf("panic message should mention Upstream: %q", msg)
		}
	}()
	Register(r)
}

func TestRegisterRefusesEmptyName(t *testing.T) {
	resetForTest()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty Name")
		}
	}()
	Register(fakeRecipe{meta: RecipeMeta{
		Name:        "",
		Category:    CategoryRelease,
		Description: "x",
		Upstream:    "https://example.com",
	}})
}

func TestRegisterRefusesEmptyDescription(t *testing.T) {
	resetForTest()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty Description")
		}
	}()
	Register(fakeRecipe{meta: RecipeMeta{
		Name:        "x",
		Category:    CategoryRelease,
		Description: "  ",
		Upstream:    "https://example.com",
	}})
}

func TestRegisterRefusesUnknownCategory(t *testing.T) {
	resetForTest()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown category")
		}
	}()
	Register(fakeRecipe{meta: RecipeMeta{
		Name:        "x",
		Category:    Category("made-up"),
		Description: "x",
		Upstream:    "https://example.com",
	}})
}

func TestRegisterRefusesDuplicateName(t *testing.T) {
	resetForTest()
	Register(newFake("dup", CategoryGovernance))
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for duplicate name")
		}
		msg, _ := v.(string)
		if !strings.Contains(msg, "already registered") {
			t.Errorf("panic should mention duplicate: %q", msg)
		}
	}()
	Register(newFake("dup", CategoryGovernance))
}

func TestAllSortedByCategoryThenName(t *testing.T) {
	resetForTest()
	// Register in scrambled order; All() must return canonical order.
	Register(newFake("zebra", CategoryAgents))
	Register(newFake("apple", CategoryGovernance))
	Register(newFake("mango", CategoryRelease))
	Register(newFake("kiwi", CategoryRelease))
	Register(newFake("date", CategoryGovernance))

	got := All()
	want := []string{"apple", "date", "kiwi", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("All() returned %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Meta().Name != name {
			t.Errorf("All()[%d] = %q, want %q", i, got[i].Meta().Name, name)
		}
	}
}

func TestInCategoryFilters(t *testing.T) {
	resetForTest()
	Register(newFake("license", CategoryGovernance))
	Register(newFake("codeowners", CategoryGovernance))
	Register(newFake("dependabot", CategorySupplyChain))

	gov := InCategory(CategoryGovernance)
	if len(gov) != 2 {
		t.Fatalf("InCategory(governance) = %d, want 2", len(gov))
	}
	for _, r := range gov {
		if r.Meta().Category != CategoryGovernance {
			t.Errorf("recipe %q has wrong category %q", r.Meta().Name, r.Meta().Category)
		}
	}

	if got := InCategory(CategoryRuntime); len(got) != 0 {
		t.Errorf("InCategory(runtime) should be empty, got %d", len(got))
	}
}

func TestIsForced(t *testing.T) {
	if IsForced(nil) {
		t.Error("nil opts should not be forced")
	}
	if IsForced(Options{}) {
		t.Error("empty opts should not be forced")
	}
	if !IsForced(Options{"force": true}) {
		t.Error("opts[force]=true should be forced")
	}
	if IsForced(Options{"force": false}) {
		t.Error("opts[force]=false should not be forced")
	}
	if IsForced(Options{"force": "true"}) {
		t.Error("opts[force]=string-true should not satisfy bool — typed Get is strict")
	}
}

func TestGetOption(t *testing.T) {
	opts := Options{
		"vault_path": "~/Documents/MyVault",
		"strict":     true,
		"count":      3,
	}
	if v, ok := GetOption[string](opts, "vault_path"); !ok || v != "~/Documents/MyVault" {
		t.Errorf("GetOption[string]: got %q, %v", v, ok)
	}
	if v, ok := GetOption[bool](opts, "strict"); !ok || !v {
		t.Errorf("GetOption[bool]: got %v, %v", v, ok)
	}
	if _, ok := GetOption[string](opts, "missing"); ok {
		t.Error("GetOption should return ok=false for missing keys")
	}
	if _, ok := GetOption[string](opts, "count"); ok {
		t.Error("GetOption should return ok=false on type mismatch")
	}
	if _, ok := GetOption[string](nil, "x"); ok {
		t.Error("GetOption on nil Options should be ok=false")
	}
}
