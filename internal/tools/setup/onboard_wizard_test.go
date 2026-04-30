package setuptools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// mkOnboardWizardReq fabricates an MCP CallToolRequest. nil
// telemetry leaves the arg out so the handler's default-true path
// runs; nil nonInteractive likewise tests the "missing required
// flag" branch.
func mkOnboardWizardReq(family string, telemetry *bool, nonInteractive *bool) mcp.CallToolRequest {
	args := map[string]any{}
	if family != "" {
		args["agent_family"] = family
	}
	if telemetry != nil {
		args["telemetry_opt_in"] = *telemetry
	}
	if nonInteractive != nil {
		args["non_interactive"] = *nonInteractive
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "OnboardWizard",
			Arguments: args,
		},
	}
}

// withWizardXDG redirects every config-path resolver into a
// per-test temp dir.
func withWizardXDG(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	return filepath.Join(tmp, "clawtool")
}

// TestOnboardWizard_InteractiveModeRefused — without
// non_interactive=true the chat path returns the documented
// error pointing at `clawtool onboard`. No marker, no config
// mutation.
func TestOnboardWizard_InteractiveModeRefused(t *testing.T) {
	cfgDir := withWizardXDG(t)

	// non_interactive missing entirely.
	res, err := runOnboardWizard(context.Background(), mkOnboardWizardReq("codex", nil, nil))
	if err != nil {
		t.Fatalf("runOnboardWizard: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError = true when non_interactive is absent")
	}
	// Marker must NOT exist.
	if _, err := os.Stat(filepath.Join(cfgDir, ".onboarded")); err == nil {
		t.Error(".onboarded marker exists; should not be created when non_interactive missing")
	}

	// non_interactive=false explicitly — same outcome.
	f := false
	res2, err := runOnboardWizard(context.Background(), mkOnboardWizardReq("codex", nil, &f))
	if err != nil {
		t.Fatalf("runOnboardWizard non_interactive=false: %v", err)
	}
	if !res2.IsError {
		t.Error("expected IsError = true when non_interactive=false")
	}
	// Error text should mention `clawtool onboard` so the
	// caller knows where to point the operator.
	body := errorContent(res2)
	if !strings.Contains(body, "clawtool onboard") {
		t.Errorf("error message should reference `clawtool onboard`, got %q", body)
	}
}

// TestOnboardWizard_PersistsMarkerAndAgentFamily — the happy
// path: non_interactive=true, valid family, telemetry default
// true. Asserts the marker is written, the JSON shape carries
// the recorded preferences, and config.toml round-trips.
func TestOnboardWizard_PersistsMarkerAndAgentFamily(t *testing.T) {
	cfgDir := withWizardXDG(t)
	tr := true

	res, err := runOnboardWizard(context.Background(), mkOnboardWizardReq("codex", nil, &tr))
	if err != nil {
		t.Fatalf("runOnboardWizard: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got IsError=true; body=%q", errorContent(res))
	}
	got, ok := res.StructuredContent.(onboardWizardResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want onboardWizardResult", res.StructuredContent)
	}
	if got.AgentFamily != "codex" {
		t.Errorf("AgentFamily = %q, want codex", got.AgentFamily)
	}
	if !got.Telemetry {
		t.Error("Telemetry = false; default should be true")
	}
	if !got.MarkerWritten {
		t.Error("MarkerWritten = false; want true")
	}
	if !strings.Contains(got.NextAction, "InitApply") {
		t.Errorf("NextAction should point at InitApply, got %q", got.NextAction)
	}

	// Marker file landed.
	if _, err := os.Stat(filepath.Join(cfgDir, ".onboarded")); err != nil {
		t.Errorf("marker file not written: %v", err)
	}

	// Config round-trip — the operator's preferences are now on
	// disk where `clawtool onboard` (and the daemon) will read
	// them on next boot.
	cfg, err := config.LoadOrDefault(filepath.Join(cfgDir, "config.toml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Telemetry.Enabled {
		t.Error("config.toml telemetry.enabled=false; want true")
	}
	if cfg.Profile.Active != "family:codex" {
		t.Errorf("profile.active = %q, want family:codex", cfg.Profile.Active)
	}
}

// TestOnboardWizard_NoneFamilyIsAccepted — agent_family="none"
// is the documented "decide later" sentinel and should
// normalise to empty in the persisted state.
func TestOnboardWizard_NoneFamilyIsAccepted(t *testing.T) {
	cfgDir := withWizardXDG(t)
	tr := true

	res, err := runOnboardWizard(context.Background(), mkOnboardWizardReq("none", nil, &tr))
	if err != nil {
		t.Fatalf("runOnboardWizard: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success on none family; body=%q", errorContent(res))
	}
	got := res.StructuredContent.(onboardWizardResult)
	if got.AgentFamily != "" {
		t.Errorf("AgentFamily = %q, want empty (none → empty normalisation)", got.AgentFamily)
	}

	// Profile.Active must NOT have been clobbered with a
	// "family:" prefix when the operator opted out of picking
	// one — leaves it at whatever Default() seeded.
	cfg, err := config.LoadOrDefault(filepath.Join(cfgDir, "config.toml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.HasPrefix(cfg.Profile.Active, "family:") {
		t.Errorf("profile.active = %q; family= prefix should not be set when agent_family=none", cfg.Profile.Active)
	}
}

// TestOnboardWizard_UnknownFamilyRejected — passing a family
// outside the documented allowlist surfaces an error result
// rather than silently writing the marker.
func TestOnboardWizard_UnknownFamilyRejected(t *testing.T) {
	cfgDir := withWizardXDG(t)
	tr := true

	res, err := runOnboardWizard(context.Background(), mkOnboardWizardReq("not-a-real-cli", nil, &tr))
	if err != nil {
		t.Fatalf("runOnboardWizard: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError = true for unknown agent_family")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, ".onboarded")); err == nil {
		t.Error(".onboarded marker was written despite invalid agent_family")
	}
}

// errorContent extracts the human-readable text from an MCP
// tool result. Both the success and error paths populate
// Content[0].Text — the IsError flag distinguishes them.
func errorContent(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	if tc, ok := r.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
