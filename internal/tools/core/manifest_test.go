package core

import (
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/tools/registry"
	"github.com/mark3labs/mcp-go/server"
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

// TestBuildManifest_Step4FullCatalog asserts the manifest now
// covers every shipped tool — Step 4 of #173 landed (server.go
// flipped, multi-tool wrappers migrated, ToolSearch + WebSearch
// wired through Runtime). The number of specs must match the
// catalog; missing entries surface here.
func TestBuildManifest_Step4FullCatalog(t *testing.T) {
	want := []string{
		// Step 2 (newest 6)
		"Commit", "RulesCheck", "AgentNew",
		"BashOutput", "BashKill", "TaskNotify",
		// Step 3a (12 individual-Register tools)
		"Bash", "Grep", "Read", "Glob", "WebFetch", "Edit", "Write",
		"Verify", "SemanticSearch", "BrowserFetch", "BrowserScrape", "SkillNew",
		// Step 4: Runtime-dependent + multi-tool wrappers
		"ToolSearch", "WebSearch",
		"RecipeList", "RecipeStatus", "RecipeApply",
		"BridgeList", "BridgeAdd", "BridgeRemove", "BridgeUpgrade",
		"SendMessage", "AgentList",
		"TaskGet", "TaskWait", "TaskList",
		"PortalList", "PortalAsk", "PortalUse", "PortalWhich", "PortalUnset", "PortalRemove",
		"McpList", "McpNew", "McpRun", "McpBuild", "McpInstall",
		"SandboxList", "SandboxShow", "SandboxDoctor",
	}
	got := map[string]bool{}
	for _, s := range BuildManifest().Specs() {
		got[s.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("manifest missing %q — Step 4 should cover every shipped tool", name)
		}
	}
}

// TestManifestDescriptionsAreSourcedFromTools is the drift-prevention
// invariant for ADR-related to the bleve BM25 ToolSearch index. It
// guards a class of bug we caught post-v0.22.108: the description
// rewrite (commit e195b30) edited every `mcp.WithDescription(...)`
// string but did NOT update the parallel hardcoded copies in
// internal/tools/core/manifest.go, so the bleve indexer (which
// reads from the manifest via SearchDocs) kept ranking on stale
// prose for an entire release. Production Hit@1 / Hit@3 / MRR
// stayed flat instead of taking the +5pp / +10pp lift the
// counterfactual bench predicted.
//
// What it asserts: for every tool whose Register fn actually
// registers it onto an MCPServer, the manifest's spec.Description
// MUST equal the registered Tool.Description. The single source
// of truth is `mcp.WithDescription(...)`; manifest.go's hardcoded
// strings are now mirrored from there at BuildManifest time via
// Manifest.SyncDescriptionsFromRegistration. If a future contributor
// rewrites a tool's mcp.WithDescription in one place but not the
// other, this test fires before the change ships — and tells them
// which tool drifted.
//
// What it does NOT assert: companion specs in multi-tool bundles
// (e.g. RecipeStatus / RecipeApply / BridgeAdd / TaskWait / …)
// share a Register fn with their bundle's first spec, so they
// still appear in `s.ListTools()` after Apply runs — the
// invariant covers them transparently.
func TestManifestDescriptionsAreSourcedFromTools(t *testing.T) {
	m := BuildManifest()

	// Build the same throwaway-server probe that
	// SyncDescriptionsFromRegistration uses internally; if the
	// sync is doing its job, every spec.Description here equals
	// its corresponding live tool description byte for byte.
	live := m.LiveDescriptions(registry.Runtime{})
	if len(live) == 0 {
		t.Fatal("LiveDescriptions returned 0 tools — the throwaway-server probe broke")
	}

	// Index manifest specs by name for O(1) lookup.
	specs := map[string]registry.ToolSpec{}
	for _, s := range m.Specs() {
		specs[s.Name] = s
	}

	// Every registered tool must have a manifest spec, and the
	// descriptions MUST match. Drift on either side fails CI.
	for name, liveDesc := range live {
		spec, ok := specs[name]
		if !ok {
			t.Errorf("registered tool %q has no manifest spec — every shipped tool needs an entry in BuildManifest", name)
			continue
		}
		if spec.Description != liveDesc {
			t.Errorf("description drift for %q:\n  manifest spec: %q\n  registered  : %q",
				name, truncateForTest(spec.Description, 200), truncateForTest(liveDesc, 200))
		}
	}

	// Inverse direction — every manifest spec with a non-nil
	// Register fn MUST have produced a registered tool. If a
	// Register fn somehow failed silently (didn't call
	// s.AddTool), the manifest entry is a phantom and ToolSearch
	// would surface a tool the host can't actually invoke.
	for _, s := range m.Specs() {
		if s.Register == nil {
			continue
		}
		if _, ok := live[s.Name]; !ok {
			t.Errorf("manifest spec %q has Register fn but no tool registered — Register may have failed silently", s.Name)
		}
	}
}

// TestManifestDescriptions_NotEmpty is a coarse sanity check
// catching one specific failure mode of
// SyncDescriptionsFromRegistration: if the throwaway probe broke
// (e.g. mcp-go renamed ListTools), every Description would silently
// become empty, and ToolSearch would index empty docs. Asserting
// "no spec is empty after BuildManifest" surfaces that regression
// faster than any individual ranking test.
func TestManifestDescriptions_NotEmpty(t *testing.T) {
	for _, s := range BuildManifest().Specs() {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("spec %q has empty Description after BuildManifest — sync probe likely failed", s.Name)
		}
	}
}

// TestSyncDescriptionsFromRegistration_DetectsDrift is the unit
// test for the sync mechanism itself. It builds a manifest with an
// intentionally-wrong Description, runs sync, and asserts the
// description got overwritten with the canonical one from the
// live mcp.Tool. Catches regressions where the sync silently
// no-ops (e.g. typo on Spec.Description field, ListTools API
// rename in mcp-go).
func TestSyncDescriptionsFromRegistration_DetectsDrift(t *testing.T) {
	m := registry.New()
	m.Append(registry.ToolSpec{
		Name:        "Bash",
		Description: "STALE DESCRIPTION — should be overwritten by sync",
		Keywords:    []string{"shell"},
		Category:    registry.CategoryShell,
		Gate:        "Bash",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBash(s)
		},
	})

	m.SyncDescriptionsFromRegistration(registry.Runtime{})

	got := m.Specs()[0].Description
	if strings.Contains(got, "STALE DESCRIPTION") {
		t.Fatalf("sync did not overwrite stale Description: %q", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("sync overwrote with empty string — probe broken")
	}
}

// truncateForTest returns at most n bytes of s, suffixed with ellipsis
// when truncation occurred. Used by description-mismatch error
// messages to keep test failure output readable when one side is
// very long. Renamed off the unexported `truncate` helper that
// already lives in websearch_brave.go — same package.
func truncateForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
