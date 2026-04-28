package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookOutput mirrors the JSON shape claude-bootstrap emits so the
// tests can decode and assert on additionalContext directly without
// fragile string matching against keys.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func runBootstrap(t *testing.T, cwd string) hookOutput {
	t.Helper()
	t.Chdir(cwd)
	out := &bytes.Buffer{}
	app := &App{
		Stdout: out,
		Stderr: &bytes.Buffer{},
		Stdin:  strings.NewReader("{}"),
	}
	rc := app.runClaudeBootstrap([]string{"--event", "session-start"})
	if rc != 0 {
		t.Fatalf("runClaudeBootstrap exit=%d stderr=%q", rc, app.Stderr)
	}
	var got hookOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out.String())
	}
	if got.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", got.HookSpecificOutput.HookEventName)
	}
	return got
}

func TestClaudeBootstrap_NoMarker_EmptyContext(t *testing.T) {
	dir := t.TempDir()
	out := runBootstrap(t, dir)
	if out.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("expected empty context outside .clawtool/ scope, got %q", out.HookSpecificOutput.AdditionalContext)
	}
}

func TestClaudeBootstrap_DetectsClawtoolMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runBootstrap(t, dir)
	ctx := out.HookSpecificOutput.AdditionalContext
	if ctx == "" {
		t.Fatal("expected non-empty additionalContext when .clawtool/ marker present")
	}
	for _, want := range []string{
		"clawtool is active",
		"mcp__clawtool__",
		"continue",
		"fresh task",
		"context-aware",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context missing %q\nfull context: %s", want, ctx)
		}
	}
}

func TestClaudeBootstrap_WalksUpToFindMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	out := runBootstrap(t, deep)
	if out.HookSpecificOutput.AdditionalContext == "" {
		t.Fatal("walking up from nested cwd should still find .clawtool/ marker")
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, root) {
		t.Errorf("expected detected root path %q in context, got %q", root, out.HookSpecificOutput.AdditionalContext)
	}
}

func TestClaudeBootstrap_ListsDetectedMarkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".clawtool", "rules.toml"), []byte("# rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wiki", "log.md"), []byte("# log"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# claude"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runBootstrap(t, dir)
	ctx := out.HookSpecificOutput.AdditionalContext
	for _, want := range []string{
		"wiki/ — project knowledge base",
		"wiki/log.md — last updated",
		".clawtool/rules.toml",
		"CLAUDE.md — project memory",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context missing marker %q\nfull context: %s", want, ctx)
		}
	}
}

// TestClaudeBootstrap_AlwaysEmitsValidJSON asserts the hook always
// produces parseable JSON. Claude Code's hook chain refuses to
// continue if a `command` hook emits non-JSON; the tests double as
// a regression guard against accidental fmt.Print* calls leaking
// into stdout.
func TestClaudeBootstrap_AlwaysEmitsValidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}, Stdin: strings.NewReader("")}
	rc := app.runClaudeBootstrap([]string{"--event", "session-start"})
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out.String())
	}
	if _, ok := v["hookSpecificOutput"]; !ok {
		t.Errorf("missing hookSpecificOutput key: %s", out.String())
	}
}

// TestClaudeBootstrap_UnknownEventEmitsEmpty asserts forward-compat
// for events we don't yet implement (UserPromptSubmit, SessionEnd,
// etc.) — emit empty additionalContext rather than refusing so
// Claude Code's hook chain stays unblocked.
func TestClaudeBootstrap_UnknownEventEmitsEmpty(t *testing.T) {
	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}, Stdin: strings.NewReader("")}
	rc := app.runClaudeBootstrap([]string{"--event", "future-event"})
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var got hookOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\nraw: %s", err, out.String())
	}
	if got.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("unknown event should produce empty context, got %q", got.HookSpecificOutput.AdditionalContext)
	}
}
