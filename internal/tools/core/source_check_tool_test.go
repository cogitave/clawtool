package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// mkSourceCheckReq fabricates an MCP CallToolRequest with the
// optional `instance` filter argument. Mirrors the agent_detect
// test helper.
func mkSourceCheckReq(instance string) mcp.CallToolRequest {
	args := map[string]any{}
	if instance != "" {
		args["instance"] = instance
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "SourceCheck",
			Arguments: args,
		},
	}
}

// withSourceXDG redirects config + secrets resolution into a
// per-test temp directory by overriding XDG_CONFIG_HOME. The
// real resolver appends the per-app "clawtool" segment, so
// config.toml lands at <tmp>/clawtool/config.toml.
func withSourceXDG(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	return filepath.Join(tmp, "clawtool")
}

// TestSourceCheck_NoSourcesConfigured — empty config returns an
// empty entries array with `ready=false` (the all-ready summary
// flag is only true when there's at least one ready entry).
func TestSourceCheck_NoSourcesConfigured(t *testing.T) {
	withSourceXDG(t)

	res, err := runSourceCheck(context.Background(), mkSourceCheckReq(""))
	if err != nil {
		t.Fatalf("runSourceCheck: %v", err)
	}
	got, ok := res.StructuredContent.(sourceCheckResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sourceCheckResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Errorf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if got.Ready {
		t.Error("Ready = true on empty config; want false")
	}
	if len(got.Entries) != 0 {
		t.Errorf("Entries = %+v, want empty slice", got.Entries)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "no sources configured") {
		t.Errorf("render missing 'no sources configured' phrase: %q", out)
	}
}

// TestSourceCheck_AllReady — one source whose required env var
// is satisfied via the secrets store → entries[0].ready=true,
// summary ready=true, missing is empty.
func TestSourceCheck_AllReady(t *testing.T) {
	cfgDir := withSourceXDG(t)
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[sources.github]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
[sources.github.env]
GITHUB_TOKEN = "${GITHUB_TOKEN}"
`)
	writeTOML(t, filepath.Join(cfgDir, "secrets.toml"), `
[scopes.github]
GITHUB_TOKEN = "ghp_test_token"
`)

	res, err := runSourceCheck(context.Background(), mkSourceCheckReq(""))
	if err != nil {
		t.Fatalf("runSourceCheck: %v", err)
	}
	got, ok := res.StructuredContent.(sourceCheckResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sourceCheckResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Fatalf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if !got.Ready {
		t.Error("Ready = false; want true with one ready entry")
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "github" || !got.Entries[0].Ready {
		t.Fatalf("Entries = %+v, want one ready=true github entry", got.Entries)
	}
	if len(got.Entries[0].Missing) != 0 {
		t.Errorf("Missing = %v on ready entry; want empty", got.Entries[0].Missing)
	}
	out := mustRenderText(t, res)
	if !strings.Contains(out, "✓ ready") {
		t.Errorf("render missing ready glyph: %q", out)
	}
}

// TestSourceCheck_MissingSecret — same source but no secret set;
// entries[0].ready=false with Missing listing the env var name,
// summary ready=false. No secret VALUE leaks in the render.
func TestSourceCheck_MissingSecret(t *testing.T) {
	cfgDir := withSourceXDG(t)
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[sources.github]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
[sources.github.env]
GITHUB_TOKEN = "${GITHUB_TOKEN}"
`)

	// No secrets.toml — Resolve falls through to process env. Make
	// sure the variable is unset so the result is reproducible
	// regardless of the host's shell env.
	t.Setenv("GITHUB_TOKEN", "")

	res, err := runSourceCheck(context.Background(), mkSourceCheckReq(""))
	if err != nil {
		t.Fatalf("runSourceCheck: %v", err)
	}
	got, ok := res.StructuredContent.(sourceCheckResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sourceCheckResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Fatalf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if got.Ready {
		t.Error("Ready = true; want false when secret missing")
	}
	if len(got.Entries) != 1 || got.Entries[0].Ready {
		t.Fatalf("Entries = %+v, want one ready=false entry", got.Entries)
	}
	if len(got.Entries[0].Missing) != 1 || got.Entries[0].Missing[0] != "GITHUB_TOKEN" {
		t.Errorf("Missing = %v, want ['GITHUB_TOKEN']", got.Entries[0].Missing)
	}
}

// TestSourceCheck_FilterByInstance — when the operator passes
// `instance=github`, only that source is reported even though
// other sources are configured.
func TestSourceCheck_FilterByInstance(t *testing.T) {
	cfgDir := withSourceXDG(t)
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[sources.github]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-github"]
[sources.github.env]
GITHUB_TOKEN = "${GITHUB_TOKEN}"

[sources.slack]
type = "mcp"
command = ["npx", "-y", "@modelcontextprotocol/server-slack"]
[sources.slack.env]
SLACK_BOT_TOKEN = "${SLACK_BOT_TOKEN}"
`)

	res, err := runSourceCheck(context.Background(), mkSourceCheckReq("github"))
	if err != nil {
		t.Fatalf("runSourceCheck: %v", err)
	}
	got, ok := res.StructuredContent.(sourceCheckResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sourceCheckResult", res.StructuredContent)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("filtered Entries = %d, want 1; %+v", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Name != "github" {
		t.Errorf("Entries[0].Name = %q, want 'github'", got.Entries[0].Name)
	}
}

// TestSourceCheck_UnknownInstance — naming a source that isn't
// configured produces an MCP error result (not a Go error) so the
// caller can branch on IsError without a panic.
func TestSourceCheck_UnknownInstance(t *testing.T) {
	withSourceXDG(t)

	res, err := runSourceCheck(context.Background(), mkSourceCheckReq("no-such-source"))
	if err != nil {
		t.Fatalf("runSourceCheck: %v", err)
	}
	got, ok := res.StructuredContent.(sourceCheckResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sourceCheckResult", res.StructuredContent)
	}
	if !got.IsError() {
		t.Error("expected IsError() = true on unknown instance")
	}
	if !strings.Contains(got.ErrorReason, "no-such-source") {
		t.Errorf("ErrorReason should name the unknown instance: %q", got.ErrorReason)
	}
}

// TestSourceCheck_RegisteredInManifest pins the surface-drift
// guard: SourceCheck MUST be in the manifest with a non-nil
// Register hook + non-empty description / keywords. Sister of
// TestAgentDetect_RegisteredInManifest.
func TestSourceCheck_RegisteredInManifest(t *testing.T) {
	for _, s := range BuildManifest().Specs() {
		if s.Name != "SourceCheck" {
			continue
		}
		if s.Register == nil {
			t.Error("SourceCheck spec has nil Register")
		}
		if s.Gate != "" {
			t.Errorf("SourceCheck gate = %q, want empty (always-on)", s.Gate)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Error("SourceCheck spec has empty Description")
		}
		if len(s.Keywords) == 0 {
			t.Error("SourceCheck spec has no Keywords")
		}
		return
	}
	t.Fatal("manifest missing SourceCheck spec")
}

// writeTOML is a tiny helper that writes content to path,
// creating the parent dir 0700 if absent. Used by the table
// tests above to seed config.toml + secrets.toml under an
// XDG-redirected temp root.
func writeTOML(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdirAll %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
