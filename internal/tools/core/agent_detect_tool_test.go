package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/mark3labs/mcp-go/mcp"
)

// mkAgentDetectReq is a tiny shim that fabricates an MCP
// CallToolRequest with the given `agent` argument. Mirrors the
// pattern in tasknotify_tool_test.go's mkNotifyReq.
func mkAgentDetectReq(agent string) mcp.CallToolRequest {
	args := map[string]any{}
	if agent != "" {
		args["agent"] = agent
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "AgentDetect",
			Arguments: args,
		},
	}
}

// TestAgentDetect_DetectedAndClaimed exercises the happy path:
// claude-code adapter is detected (parent settings dir exists)
// AND has been claimed (marker file written by Claim) → exit 0.
func TestAgentDetect_DetectedAndClaimed(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)
	t.Cleanup(func() { agents.SetClaudeCodeSettingsPath("") })

	adp, err := agents.Find("claude-code")
	if err != nil {
		t.Fatalf("find claude-code: %v", err)
	}
	if _, err := adp.Claim(agents.Options{}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	res, err := runAgentDetect(context.Background(), mkAgentDetectReq("claude-code"))
	if err != nil {
		t.Fatalf("runAgentDetect: %v", err)
	}
	got, ok := res.StructuredContent.(agentDetectResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want agentDetectResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Errorf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if got.Adapter != "claude-code" {
		t.Errorf("Adapter = %q, want claude-code", got.Adapter)
	}
	if !got.Detected {
		t.Error("Detected = false, want true (parent dir exists)")
	}
	if !got.Claimed {
		t.Error("Claimed = false, want true (after seed claim)")
	}
	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", got.ExitCode)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "detected and claimed") {
		t.Errorf("render missing 'detected and claimed': %q", out)
	}
}

// TestAgentDetect_DetectedNotClaimed — fresh settings dir, no
// claim marker → exit 1, banner suggests next step.
func TestAgentDetect_DetectedNotClaimed(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)
	t.Cleanup(func() { agents.SetClaudeCodeSettingsPath("") })

	res, err := runAgentDetect(context.Background(), mkAgentDetectReq("claude-code"))
	if err != nil {
		t.Fatalf("runAgentDetect: %v", err)
	}
	got, ok := res.StructuredContent.(agentDetectResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want agentDetectResult", res.StructuredContent)
	}
	if got.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", got.ExitCode)
	}
	if !got.Detected {
		t.Error("Detected = false, want true (parent dir exists)")
	}
	if got.Claimed {
		t.Error("Claimed = true, want false (no claim seeded)")
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "NOT claimed") || !strings.Contains(out, "claim claude-code") {
		t.Errorf("render missing not-claimed phrasing or next-step hint: %q", out)
	}
}

// TestAgentDetect_NotDetected points the adapter at a settings
// path under a non-existent parent dir → Detected=false → exit 2.
func TestAgentDetect_NotDetected(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "settings.json")
	agents.SetClaudeCodeSettingsPath(missing)
	t.Cleanup(func() { agents.SetClaudeCodeSettingsPath("") })

	res, err := runAgentDetect(context.Background(), mkAgentDetectReq("claude-code"))
	if err != nil {
		t.Fatalf("runAgentDetect: %v", err)
	}
	got, ok := res.StructuredContent.(agentDetectResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want agentDetectResult", res.StructuredContent)
	}
	if got.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", got.ExitCode)
	}
	if got.Detected {
		t.Error("Detected = true, want false (parent dir missing)")
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "not detected") {
		t.Errorf("render missing not-detected phrasing: %q", out)
	}
}

// TestAgentDetect_UnknownAgent rejects names not in the registry
// — same shape as the CLI verb's exit-1 + helpful message.
func TestAgentDetect_UnknownAgent(t *testing.T) {
	res, err := runAgentDetect(context.Background(), mkAgentDetectReq("not-real-agent"))
	if err != nil {
		t.Fatalf("runAgentDetect: %v", err)
	}
	got, ok := res.StructuredContent.(agentDetectResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want agentDetectResult", res.StructuredContent)
	}
	if !got.IsError() {
		t.Error("expected IsError() = true on unknown agent")
	}
	if !strings.Contains(got.ErrorReason, "not-real-agent") {
		t.Errorf("ErrorReason should name the unknown agent: %q", got.ErrorReason)
	}
}

// TestAgentDetect_MissingArgument fails cleanly when the `agent`
// arg is omitted — MCP tool error result, not a panic.
func TestAgentDetect_MissingArgument(t *testing.T) {
	res, err := runAgentDetect(context.Background(), mkAgentDetectReq(""))
	if err != nil {
		t.Fatalf("runAgentDetect should not return Go error on missing arg; got %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError = true on missing arg")
	}
}

// TestAgentDetect_RegisteredInManifest pins the surface-drift
// guard: the AgentDetect tool MUST be in the manifest with a
// non-nil Register hook + non-empty description / keywords.
func TestAgentDetect_RegisteredInManifest(t *testing.T) {
	for _, s := range BuildManifest().Specs() {
		if s.Name != "AgentDetect" {
			continue
		}
		if s.Register == nil {
			t.Error("AgentDetect spec has nil Register")
		}
		if s.Gate != "" {
			t.Errorf("AgentDetect gate = %q, want empty (always-on)", s.Gate)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Error("AgentDetect spec has empty Description")
		}
		if len(s.Keywords) == 0 {
			t.Error("AgentDetect spec has no Keywords")
		}
		return
	}
	t.Fatal("manifest missing AgentDetect spec")
}
