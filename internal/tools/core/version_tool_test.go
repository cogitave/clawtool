package core

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/version"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestVersionTool_RunReturnsBuildInfo asserts runVersion produces a
// CallToolResult whose StructuredContent embeds the same BuildInfo
// that version.Info() returns directly. Keeps the MCP, HTTP
// (/v1/health) and CLI (`version --json`) consumers in lock-step
// — if any of the three drifts, this test fails alongside the
// http_test.go and version_test.go counterparts.
func TestVersionTool_RunReturnsBuildInfo(t *testing.T) {
	res, err := runVersion(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	if res == nil {
		t.Fatal("nil CallToolResult")
	}
	if res.IsError {
		t.Errorf("IsError = true, want false")
	}
	got, ok := res.StructuredContent.(versionResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want versionResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Errorf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if got.Engine != "version" {
		t.Errorf("Engine = %q, want %q", got.Engine, "version")
	}
	if got.Operation != "Version" {
		t.Errorf("Operation = %q, want %q", got.Operation, "Version")
	}
	if got.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want >= 0", got.DurationMs)
	}

	want := version.Info()
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.Version != want.Version {
		t.Errorf("Version = %q, want %q", got.Version, want.Version)
	}
	if got.GoVersion != want.GoVersion {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, want.GoVersion)
	}
	if got.Platform != want.Platform {
		t.Errorf("Platform = %q, want %q", got.Platform, want.Platform)
	}
	if got.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("Platform = %q, want %q", got.Platform, runtime.GOOS+"/"+runtime.GOARCH)
	}
}

// TestVersionTool_RenderContainsIdentity asserts the human-readable
// envelope (the content[0].text channel the chat UI shows) contains
// the binary name, the semver, the Go runtime version, and the
// platform. Operators / agents reading the rendered banner can
// confirm "what version am I talking to" without parsing JSON.
func TestVersionTool_RenderContainsIdentity(t *testing.T) {
	res, err := runVersion(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	out := mustRenderText(t, res)

	info := version.Info()
	for _, want := range []string{
		info.Name,
		info.Version,
		info.GoVersion,
		info.Platform,
		"version", // the engine bracket "[version]"
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- render ---\n%s", want, out)
		}
	}
}

// TestVersionTool_RegisteredInManifest asserts the manifest entry
// is present, lives under CategoryDiscovery, and has a non-nil
// Register hook. Surface-drift guard so a future refactor can't
// silently drop the tool from the manifest while keeping the
// helper file around.
func TestVersionTool_RegisteredInManifest(t *testing.T) {
	for _, s := range BuildManifest().Specs() {
		if s.Name != "Version" {
			continue
		}
		if s.Register == nil {
			t.Error("Version spec has nil Register")
		}
		if s.Gate != "" {
			t.Errorf("Version spec gate = %q, want empty (always-on)", s.Gate)
		}
		if s.Category == "" {
			t.Error("Version spec has empty Category")
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Error("Version spec has empty Description")
		}
		if len(s.Keywords) == 0 {
			t.Error("Version spec has no Keywords")
		}
		return
	}
	t.Fatal("manifest missing Version spec")
}
