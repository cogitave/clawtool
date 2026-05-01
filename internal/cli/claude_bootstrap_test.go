package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/version"
)

// init swaps in a no-network default for fetchUpdate so the test
// package never hits api.github.com. Per-test overrides assign
// fetchUpdate directly + use t.Cleanup to restore — that wins over
// this default within the test, then the package-level value
// snaps back when the test exits.
func init() {
	fetchUpdate = func() version.UpdateInfo {
		return version.UpdateInfo{HasUpdate: false}
	}
}

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

// TestClaudeBootstrap_InjectsUpgradeLineWhenAvailable confirms the
// SessionStart hook surfaces "vX → vY available" when fetchUpdate
// reports a newer release. Stub the seam so the test never hits
// GitHub.
func TestClaudeBootstrap_InjectsUpgradeLineWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}

	prev := fetchUpdate
	t.Cleanup(func() { fetchUpdate = prev })
	fetchUpdate = func() version.UpdateInfo {
		return version.UpdateInfo{HasUpdate: true, Latest: "v9.9.9", Current: "0.22.6"}
	}

	out := runBootstrap(t, dir)
	ctx := out.HookSpecificOutput.AdditionalContext
	for _, want := range []string{
		"clawtool update available",
		"0.22.6",
		"v9.9.9",
		"clawtool upgrade",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("missing %q in upgrade-line block\nfull: %s", want, ctx)
		}
	}
}

func TestClaudeBootstrap_NoUpgradeLineWhenUpToDate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}

	prev := fetchUpdate
	t.Cleanup(func() { fetchUpdate = prev })
	fetchUpdate = func() version.UpdateInfo {
		return version.UpdateInfo{HasUpdate: false, Latest: "0.22.6", Current: "0.22.6"}
	}

	out := runBootstrap(t, dir)
	if strings.Contains(out.HookSpecificOutput.AdditionalContext, "update available") {
		t.Errorf("up-to-date check leaked the upgrade banner: %s", out.HookSpecificOutput.AdditionalContext)
	}
}

func TestClaudeBootstrap_UpgradeCheckFailureSilent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}

	prev := fetchUpdate
	t.Cleanup(func() { fetchUpdate = prev })
	fetchUpdate = func() version.UpdateInfo {
		return version.UpdateInfo{Err: errors.New("network down")}
	}

	out := runBootstrap(t, dir)
	if strings.Contains(out.HookSpecificOutput.AdditionalContext, "update available") {
		t.Errorf("network failure should NOT show upgrade banner")
	}
	// But the rest of the marker block should still render.
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "clawtool is active") {
		t.Errorf("error path should not suppress the rest of the context")
	}
}

// TestClaudeBootstrap_NotOnboarded_SurfacesNudge confirms the hook
// emits a "not onboarded" banner when .clawtool/ is present but the
// global onboarded marker is absent. Lets users discover the wizard
// from inside Claude Code instead of staring at a partially-wired
// install.
func TestClaudeBootstrap_NotOnboarded_SurfacesNudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	prev := fetchUpdate
	t.Cleanup(func() { fetchUpdate = prev })
	fetchUpdate = func() version.UpdateInfo { return version.UpdateInfo{HasUpdate: false} }

	out := runBootstrap(t, dir)
	ctx := out.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "installed but not onboarded") {
		t.Errorf("missing not-onboarded nudge\nfull: %s", ctx)
	}
	if !strings.Contains(ctx, "clawtool onboard") {
		t.Errorf("nudge should reference `clawtool onboard`\nfull: %s", ctx)
	}
}

// TestClaudeBootstrap_Onboarded_SuppressesNudge confirms the hook
// stays quiet when the marker exists — once you've onboarded, the
// banner becomes noise.
func TestClaudeBootstrap_Onboarded_SuppressesNudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := writeOnboardedMarker(); err != nil {
		t.Fatalf("writeOnboardedMarker: %v", err)
	}

	prev := fetchUpdate
	t.Cleanup(func() { fetchUpdate = prev })
	fetchUpdate = func() version.UpdateInfo { return version.UpdateInfo{HasUpdate: false} }

	out := runBootstrap(t, dir)
	if strings.Contains(out.HookSpecificOutput.AdditionalContext, "not onboarded") {
		t.Errorf("onboarded marker should suppress the nudge: %s", out.HookSpecificOutput.AdditionalContext)
	}
}

// TestClaudeBootstrap_UnknownEventEmitsEmpty asserts forward-compat
// for events we don't yet implement (SessionEnd, etc.) — emit empty
// additionalContext rather than refusing so Claude Code's hook chain
// stays unblocked.
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

// runBootstrapEvent is a flexible variant of runBootstrap that lets a
// test pick the --event flavour and the stdin body (so tests can ship
// the Claude Code session_id payload). Returns the parsed hook
// output. Skips assertion of hookEventName so tests covering both
// SessionStart + UserPromptSubmit can share the helper.
func runBootstrapEvent(t *testing.T, cwd, event, stdin string) hookOutput {
	t.Helper()
	t.Chdir(cwd)
	out := &bytes.Buffer{}
	app := &App{
		Stdout: out,
		Stderr: &bytes.Buffer{},
		Stdin:  strings.NewReader(stdin),
	}
	rc := app.runClaudeBootstrap([]string{"--event", event})
	if rc != 0 {
		t.Fatalf("runClaudeBootstrap exit=%d stderr=%q", rc, app.Stderr)
	}
	var got hookOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out.String())
	}
	return got
}

// TestClaudeBootstrap_UserPromptSubmit_FirstFire confirms the
// user-prompt-submit event emits additionalContext + creates the
// per-session marker. This is the canonical fire site after the
// Claude Code v2.1.126 ToolUseContext regression.
func TestClaudeBootstrap_UserPromptSubmit_FirstFire(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())

	sid := "sess-first-fire-" + filepath.Base(t.TempDir())
	stdin := `{"session_id":"` + sid + `","cwd":"` + dir + `"}`
	out := runBootstrapEvent(t, dir, "user-prompt-submit", stdin)

	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q, want UserPromptSubmit", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "clawtool is active") {
		t.Errorf("expected clawtool context on first fire, got %q", out.HookSpecificOutput.AdditionalContext)
	}
	marker := bootstrapMarkerPath(sid)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("expected marker %s to exist after first fire: %v", marker, err)
	}
}

// TestClaudeBootstrap_UserPromptSubmit_Idempotent confirms that a
// second user-prompt-submit fire with the same session_id short-
// circuits to empty context (we don't want to re-inject the
// clawtool primer on every prompt).
func TestClaudeBootstrap_UserPromptSubmit_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())

	sid := "sess-idem-" + filepath.Base(t.TempDir())
	// Pre-stamp the marker so the run sees "already fired".
	marker := bootstrapMarkerPath(sid)
	if err := os.WriteFile(marker, []byte("seen"), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	stdin := `{"session_id":"` + sid + `","cwd":"` + dir + `"}`
	out := runBootstrapEvent(t, dir, "user-prompt-submit", stdin)

	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q, want UserPromptSubmit", out.HookSpecificOutput.HookEventName)
	}
	if out.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("idempotent re-fire should emit empty context, got %q", out.HookSpecificOutput.AdditionalContext)
	}
}

// TestClaudeBootstrap_SessionStart_StillWorks pins the back-compat
// path. Hosts that haven't refreshed their hooks.json should still
// see additionalContext on session-start, even after the move to
// UserPromptSubmit became canonical.
func TestClaudeBootstrap_SessionStart_StillWorks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runBootstrapEvent(t, dir, "session-start", `{"session_id":"sess-back-compat"}`)
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "clawtool is active") {
		t.Errorf("session-start should still emit context for back-compat, got %q", out.HookSpecificOutput.AdditionalContext)
	}
}

// TestClaudeBootstrap_UserPromptSubmit_EnvSessionFallback confirms the
// CLAUDE_SESSION_ID env var is the fallback when stdin doesn't carry
// the session_id (some hook runners may not pipe JSON).
func TestClaudeBootstrap_UserPromptSubmit_EnvSessionFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".clawtool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())

	sid := "sess-env-" + filepath.Base(t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", sid)

	// First fire — empty stdin, env supplies session_id.
	out1 := runBootstrapEvent(t, dir, "user-prompt-submit", "")
	if !strings.Contains(out1.HookSpecificOutput.AdditionalContext, "clawtool is active") {
		t.Errorf("first fire (env sid) should emit context, got %q", out1.HookSpecificOutput.AdditionalContext)
	}
	if _, err := os.Stat(bootstrapMarkerPath(sid)); err != nil {
		t.Errorf("marker not created via env fallback: %v", err)
	}

	// Second fire — should short-circuit.
	out2 := runBootstrapEvent(t, dir, "user-prompt-submit", "")
	if out2.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("second fire should be empty (idempotent), got %q", out2.HookSpecificOutput.AdditionalContext)
	}
}

// TestBundledHooksJSON_SessionStartHasNoBootstrap verifies the move
// off SessionStart: the bundled plugin must NOT call claude-bootstrap
// from SessionStart anymore (Claude Code v2.1.126 ToolUseContext
// regression). Peer-register MUST remain so registry discovery still
// fires immediately.
func TestBundledHooksJSON_SessionStartHasNoBootstrap(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read bundled hooks.json: %v", err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}

	// SessionStart: peer register only, no claude-bootstrap.
	ss, ok := cfg.Hooks["SessionStart"]
	if !ok || len(ss) == 0 {
		t.Fatalf("SessionStart event missing")
	}
	var sawRegister, sawBootstrap bool
	for _, m := range ss {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, "peer register") {
				sawRegister = true
			}
			if strings.Contains(h.Command, "claude-bootstrap") {
				sawBootstrap = true
			}
		}
	}
	if !sawRegister {
		t.Errorf("SessionStart must keep `peer register` for registry discovery")
	}
	if sawBootstrap {
		t.Errorf("SessionStart must NOT call claude-bootstrap (v2.1.126 ToolUseContext regression)")
	}

	// UserPromptSubmit: claude-bootstrap is the canonical fire site.
	ups, ok := cfg.Hooks["UserPromptSubmit"]
	if !ok || len(ups) == 0 {
		t.Fatalf("UserPromptSubmit event missing")
	}
	var sawUPSBootstrap bool
	for _, m := range ups {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, "claude-bootstrap --event user-prompt-submit") {
				sawUPSBootstrap = true
			}
		}
	}
	if !sawUPSBootstrap {
		t.Errorf("UserPromptSubmit must call `claude-bootstrap --event user-prompt-submit`")
	}
}
