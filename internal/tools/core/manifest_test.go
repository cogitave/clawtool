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

// TestBuildManifest_Step3aSpecs asserts the 12 individual-Register
// tools migrated in Step 3a are all present, in the right
// category, with the right gate (empty for always-on, name-of-tool
// for gateable file/shell/web tools), and a non-nil Register fn.
func TestBuildManifest_Step3aSpecs(t *testing.T) {
	type expect struct {
		Cat  registry.Category
		Gate string
	}
	want := map[string]expect{
		// Gateable — disabling the tool's name in cfg.IsEnabled
		// hides it. Same key for tool name + gate today.
		"Bash":     {registry.CategoryShell, "Bash"},
		"Grep":     {registry.CategoryFile, "Grep"},
		"Read":     {registry.CategoryFile, "Read"},
		"Glob":     {registry.CategoryFile, "Glob"},
		"WebFetch": {registry.CategoryWeb, "WebFetch"},
		"Edit":     {registry.CategoryFile, "Edit"},
		"Write":    {registry.CategoryFile, "Write"},
		// Always-on individual tools.
		"Verify":         {registry.CategorySetup, ""},
		"SemanticSearch": {registry.CategoryDiscovery, ""},
		"BrowserFetch":   {registry.CategoryWeb, ""},
		"BrowserScrape":  {registry.CategoryWeb, ""},
		"SkillNew":       {registry.CategoryAuthoring, ""},
	}
	got := map[string]registry.ToolSpec{}
	for _, s := range BuildManifest().Specs() {
		got[s.Name] = s
	}
	for name, w := range want {
		spec, ok := got[name]
		if !ok {
			t.Errorf("manifest missing %q", name)
			continue
		}
		if spec.Category != w.Cat {
			t.Errorf("%q category = %q, want %q", name, spec.Category, w.Cat)
		}
		if spec.Gate != w.Gate {
			t.Errorf("%q gate = %q, want %q", name, spec.Gate, w.Gate)
		}
		if spec.Register == nil {
			t.Errorf("%q has nil Register — Step 3a tools should all be wired", name)
		}
		if strings.TrimSpace(spec.Description) == "" {
			t.Errorf("%q has empty Description", name)
		}
		if len(spec.Keywords) == 0 {
			t.Errorf("%q has no Keywords", name)
		}
	}
}

// TestBuildManifest_NoStep4ToolsYet documents the deferral: the
// tools that need additional Runtime dependencies (ToolSearch
// needs *search.Index; WebSearch needs *secrets.Store) and the
// multi-tool wrappers (Recipe* / Bridge* / Agent* / Task* /
// Portal* / Mcp* / Sandbox*) are EXPLICITLY not in Step 2/3a.
// They land in Step 4 alongside the server.go hookup.
//
// If a future commit accidentally adds them before the hookup
// is ready, this test fires so the author knows to land them
// together with the server.go flip.
func TestBuildManifest_NoStep4ToolsYet(t *testing.T) {
	deferred := []string{
		"ToolSearch", "WebSearch", // need Runtime additions
		"RecipeList", "RecipeStatus", "RecipeApply", // multi-tool wrapper RegisterRecipeTools
		"BridgeList", "BridgeAdd", "BridgeRemove", "BridgeUpgrade", // RegisterBridgeTools
		"SendMessage", "AgentList", // RegisterAgentTools
		"TaskGet", "TaskWait", "TaskList", // RegisterTaskTools
		"PortalList", "PortalAsk", "PortalUse", "PortalWhich", "PortalUnset", "PortalRemove", // RegisterPortalTools
		"McpList", "McpNew", "McpRun", "McpBuild", "McpInstall", // RegisterMcpTools
		"SandboxList", "SandboxShow", "SandboxDoctor", // RegisterSandboxTools
	}
	got := map[string]bool{}
	for _, s := range BuildManifest().Specs() {
		got[s.Name] = true
	}
	for _, name := range deferred {
		if got[name] {
			t.Errorf("%q is in BuildManifest but documented as Step 4 — verify the server.go hookup landed in the same commit; if so, remove the name from this test's deferred list", name)
		}
	}
}
