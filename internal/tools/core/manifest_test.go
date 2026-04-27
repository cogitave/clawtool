package core

import (
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/tools/registry"
)

// TestBuildManifest_PanicFreeAndPopulated asserts BuildManifest
// returns a non-empty manifest without tripping any of the
// load-time guards (duplicate name, empty name, invalid category).
// A panic here usually means a spec was added with a typo'd
// Category or a copy-pasted Name.
func TestBuildManifest_PanicFreeAndPopulated(t *testing.T) {
	m := BuildManifest()
	if m == nil {
		t.Fatal("BuildManifest returned nil")
	}
	if len(m.Specs()) == 0 {
		t.Fatal("BuildManifest returned empty manifest")
	}
}

// TestBuildManifest_Step2Specs asserts the six tools we migrated
// in Step 2 of #173 are all present, in the right category, with
// non-empty descriptions and at least one keyword.
func TestBuildManifest_Step2Specs(t *testing.T) {
	want := map[string]registry.Category{
		"Commit":     registry.CategoryCheckpoint,
		"RulesCheck": registry.CategoryCheckpoint,
		"AgentNew":   registry.CategoryAuthoring,
		"BashOutput": registry.CategoryShell,
		"BashKill":   registry.CategoryShell,
		"TaskNotify": registry.CategoryDispatch,
	}
	m := BuildManifest()
	got := map[string]registry.ToolSpec{}
	for _, s := range m.Specs() {
		got[s.Name] = s
	}
	for name, wantCat := range want {
		spec, ok := got[name]
		if !ok {
			t.Errorf("manifest missing %q", name)
			continue
		}
		if spec.Category != wantCat {
			t.Errorf("%q category = %q, want %q", name, spec.Category, wantCat)
		}
		if strings.TrimSpace(spec.Description) == "" {
			t.Errorf("%q has empty Description", name)
		}
		if len(spec.Keywords) == 0 {
			t.Errorf("%q has no Keywords", name)
		}
		if spec.Register == nil {
			t.Errorf("%q has nil Register — Step 2 tools should all be wired", name)
		}
	}
}

// TestBuildManifest_BashCompanionsShareGate asserts BashOutput +
// BashKill both gate on the parent "Bash" key — disabling Bash
// must hide the companions or the surface lies about what's
// callable.
func TestBuildManifest_BashCompanionsShareGate(t *testing.T) {
	m := BuildManifest()
	for _, s := range m.Specs() {
		if s.Name == "BashOutput" || s.Name == "BashKill" {
			if s.Gate != "Bash" {
				t.Errorf("%q gate = %q, want %q (companion to Bash)", s.Name, s.Gate, "Bash")
			}
		}
	}
}
