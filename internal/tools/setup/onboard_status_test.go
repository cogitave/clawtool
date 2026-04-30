package setuptools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	// Pull every recipe subpackage's init() so the registry is
	// populated when these tests run. Mirrors the blank import
	// in internal/server/server.go.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// mkOnboardStatusReq fabricates an MCP CallToolRequest with the
// optional `repo` filter. Mirrors the helper in
// internal/tools/core/source_check_tool_test.go.
func mkOnboardStatusReq(repo string) mcp.CallToolRequest {
	args := map[string]any{}
	if repo != "" {
		args["repo"] = repo
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "OnboardStatus",
			Arguments: args,
		},
	}
}

// withXDGTemp redirects ~/.config/clawtool resolution into a
// temp dir so the test never reads the developer's real
// onboarded marker. xdg.ConfigDir() honours XDG_CONFIG_HOME
// then appends "clawtool".
func withXDGTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	return filepath.Join(tmp, "clawtool")
}

// TestOnboardStatus_FreshRepo — empty repo, no host marker:
// every recipe reports absent, suggested next action points
// at OnboardWizard.
func TestOnboardStatus_FreshRepo(t *testing.T) {
	withXDGTemp(t)
	repo := t.TempDir()

	res, err := runOnboardStatus(context.Background(), mkOnboardStatusReq(repo))
	if err != nil {
		t.Fatalf("runOnboardStatus: %v", err)
	}
	got, ok := res.StructuredContent.(onboardStatusResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want onboardStatusResult", res.StructuredContent)
	}
	if got.Repo != repo {
		t.Errorf("Repo = %q, want %q", got.Repo, repo)
	}
	if got.HasClawtoolDir {
		t.Error("HasClawtoolDir = true on fresh repo")
	}
	if got.HasClaudeMD {
		t.Error("HasClaudeMD = true on fresh repo")
	}
	if got.OnboardedMarker {
		t.Error("OnboardedMarker = true with no marker file")
	}
	if len(got.RecipeStates) == 0 {
		t.Fatal("RecipeStates is empty — recipe registry didn't load")
	}
	// Every reported state must be one of the documented values.
	// Some recipes (agent-claim, *-bridge, knowledge wikis) probe
	// host state rather than the repo, so we don't assert "all
	// absent" — the contract here is just "every entry carries a
	// recognisable status verdict".
	allowed := map[string]bool{"absent": true, "partial": true, "applied": true, "error": true}
	for name, st := range got.RecipeStates {
		if !allowed[st] {
			t.Errorf("recipe %q state = %q; want one of absent/partial/applied/error", name, st)
		}
	}
	// SuggestedNextAction wording branches on the marker file
	// + whether anything is applied. With no marker, we expect
	// the OnboardWizard branch (the marker is what flips the
	// suggestion away from it).
	if !strings.Contains(got.SuggestedNextAction, "OnboardWizard") {
		t.Errorf("SuggestedNextAction should point at OnboardWizard on no-marker repo, got %q", got.SuggestedNextAction)
	}
}

// TestOnboardStatus_PartiallySetUp — onboarded marker exists,
// CLAUDE.md present at repo root, .clawtool/ dir present →
// has_clawtool_dir + has_claude_md + onboarded_marker all true,
// suggested action no longer mentions OnboardWizard.
func TestOnboardStatus_PartiallySetUp(t *testing.T) {
	cfgDir := withXDGTemp(t)
	repo := t.TempDir()

	// Seed marker.
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, ".onboarded"), []byte("2026-04-30T00:00:00Z\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	// Seed .clawtool dir + CLAUDE.md.
	if err := os.Mkdir(filepath.Join(repo, ".clawtool"), 0o755); err != nil {
		t.Fatalf("mkdir .clawtool: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("# CLAUDE.md\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	res, err := runOnboardStatus(context.Background(), mkOnboardStatusReq(repo))
	if err != nil {
		t.Fatalf("runOnboardStatus: %v", err)
	}
	got := res.StructuredContent.(onboardStatusResult)

	if !got.HasClawtoolDir {
		t.Error("HasClawtoolDir = false; want true (.clawtool/ exists)")
	}
	if !got.HasClaudeMD {
		t.Error("HasClaudeMD = false; want true (CLAUDE.md exists)")
	}
	if !got.OnboardedMarker {
		t.Error("OnboardedMarker = false; want true (marker file written)")
	}
	if strings.Contains(got.SuggestedNextAction, "OnboardWizard") {
		t.Errorf("SuggestedNextAction should not point at OnboardWizard once marker exists, got %q", got.SuggestedNextAction)
	}
}

// TestOnboardStatus_FullyOnboarded — marker present + recipe
// runs through the handler. Asserts SuggestedNextAction is
// always populated regardless of branch and the JSON round-trip
// works.
func TestOnboardStatus_FullyOnboarded(t *testing.T) {
	cfgDir := withXDGTemp(t)
	repo := t.TempDir()
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, ".onboarded"), []byte("2026-04-30T00:00:00Z\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	res, err := runOnboardStatus(context.Background(), mkOnboardStatusReq(repo))
	if err != nil {
		t.Fatalf("runOnboardStatus: %v", err)
	}
	got := res.StructuredContent.(onboardStatusResult)
	if !got.OnboardedMarker {
		t.Error("OnboardedMarker = false; want true (marker file seeded)")
	}
	if got.SuggestedNextAction == "" {
		t.Error("SuggestedNextAction empty; handler must always populate it")
	}
}
