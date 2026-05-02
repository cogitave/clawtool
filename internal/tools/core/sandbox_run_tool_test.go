package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/sandbox"
	"github.com/mark3labs/mcp-go/mcp"
)

// mkSandboxRunReq fabricates a SandboxRun MCP request. Empty
// values are omitted so absent-arg paths exercise the handler's
// own defaults rather than zero-valued inputs.
func mkSandboxRunReq(profile, command string, args []string, stdin string, timeoutMs int) mcp.CallToolRequest {
	a := map[string]any{}
	if profile != "" {
		a["profile"] = profile
	}
	if command != "" {
		a["command"] = command
	}
	if len(args) > 0 {
		raw := make([]any, 0, len(args))
		for _, s := range args {
			raw = append(raw, s)
		}
		a["args"] = raw
	}
	if stdin != "" {
		a["stdin"] = stdin
	}
	if timeoutMs > 0 {
		a["timeout_ms"] = float64(timeoutMs)
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "SandboxRun", Arguments: a},
	}
}

// withSandboxXDG seeds a per-test config.toml under a redirected
// XDG_CONFIG_HOME with one minimal sandbox profile named
// "smoke". Returns the resolved cfg dir so tests can layer extra
// fixtures.
func withSandboxXDG(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfgDir := filepath.Join(tmp, "clawtool")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[sandboxes.smoke]
description = "test profile"
[sandboxes.smoke.network]
policy = "none"
`)
	return cfgDir
}

// withSandboxRunner swaps the package-level sandboxRunner with a
// stub for the duration of the test. Restored on cleanup so
// other tests that hit the real RunOneShot stay isolated.
func withSandboxRunner(t *testing.T, stub func(context.Context, sandbox.RunRequest) (sandbox.RunResult, error)) {
	t.Helper()
	prev := sandboxRunner
	sandboxRunner = stub
	t.Cleanup(func() { sandboxRunner = prev })
}

// TestSandboxRun_HappyPath — a stubbed runner returns the
// canned result; the MCP handler surfaces it on the wire shape
// the SandboxRun tool advertises.
func TestSandboxRun_HappyPath(t *testing.T) {
	withSandboxXDG(t)

	var seen sandbox.RunRequest
	withSandboxRunner(t, func(_ context.Context, req sandbox.RunRequest) (sandbox.RunResult, error) {
		seen = req
		return sandbox.RunResult{
			Stdout:   "hello\n",
			Stderr:   "",
			ExitCode: 0,
			Engine:   "noop",
			Profile:  req.Profile.Name,
		}, nil
	})

	res, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("smoke", "echo", []string{"hello"}, "", 0))
	if err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	got, ok := res.StructuredContent.(sandboxRunResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want sandboxRunResult", res.StructuredContent)
	}
	if got.IsError() {
		t.Fatalf("ErrorReason = %q, want empty", got.ErrorReason)
	}
	if got.Stdout != "hello\n" {
		t.Errorf("Stdout = %q, want %q", got.Stdout, "hello\n")
	}
	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", got.ExitCode)
	}
	if got.Profile != "smoke" {
		t.Errorf("Profile = %q, want %q", got.Profile, "smoke")
	}
	if got.Engine != "noop" {
		t.Errorf("Engine = %q, want %q", got.Engine, "noop")
	}
	if got.Command != "echo" || len(got.Args) != 1 || got.Args[0] != "hello" {
		t.Errorf("Command/Args drift: %q %v", got.Command, got.Args)
	}
	if seen.Profile == nil || seen.Profile.Name != "smoke" {
		t.Errorf("runner didn't see resolved profile: %+v", seen.Profile)
	}
	if seen.Command != "echo" {
		t.Errorf("runner saw command=%q, want echo", seen.Command)
	}

	// Render path stays exercised so a regression in the human
	// surface is caught here, not by a downstream snapshot test.
	out := mustRenderText(t, res)
	if !strings.Contains(out, "echo hello") {
		t.Errorf("render missing command line: %q", out)
	}
	if !strings.Contains(out, "profile: smoke") {
		t.Errorf("render missing profile footer: %q", out)
	}
}

// TestSandboxRun_ProfileNotFound — referencing an undefined
// profile resolves through config.LoadOrDefault, hits the
// not-found branch, and surfaces as ErrorReason without the
// runner being touched.
func TestSandboxRun_ProfileNotFound(t *testing.T) {
	withSandboxXDG(t)

	called := false
	withSandboxRunner(t, func(_ context.Context, _ sandbox.RunRequest) (sandbox.RunResult, error) {
		called = true
		return sandbox.RunResult{}, nil
	})

	res, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("does-not-exist", "echo", nil, "", 0))
	if err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	got, ok := res.StructuredContent.(sandboxRunResult)
	if !ok {
		t.Fatalf("StructuredContent = %T", res.StructuredContent)
	}
	if !got.IsError() {
		t.Fatalf("expected error; got %+v", got)
	}
	if !strings.Contains(got.ErrorReason, "not found") {
		t.Errorf("ErrorReason = %q, want it to mention 'not found'", got.ErrorReason)
	}
	if called {
		t.Error("runner was called for a missing profile; should short-circuit before dispatch")
	}
}

// TestSandboxRun_TimeoutPropagates — timeout_ms reaches the
// runner as a time.Duration; default kicks in when omitted.
func TestSandboxRun_TimeoutPropagates(t *testing.T) {
	withSandboxXDG(t)

	var seenTimeout time.Duration
	withSandboxRunner(t, func(_ context.Context, req sandbox.RunRequest) (sandbox.RunResult, error) {
		seenTimeout = req.Timeout
		return sandbox.RunResult{Engine: "noop", Profile: req.Profile.Name}, nil
	})

	// Explicit 1500ms.
	if _, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("smoke", "true", nil, "", 1500)); err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	if seenTimeout != 1500*time.Millisecond {
		t.Errorf("seenTimeout = %s, want 1.5s", seenTimeout)
	}

	// Omitted → handler default (60s).
	if _, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("smoke", "true", nil, "", 0)); err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	if seenTimeout != 60*time.Second {
		t.Errorf("default seenTimeout = %s, want 60s", seenTimeout)
	}

	// Above-max value clamped to 600s.
	if _, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("smoke", "true", nil, "", 9_999_999)); err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	if seenTimeout != 600*time.Second {
		t.Errorf("clamped seenTimeout = %s, want 600s", seenTimeout)
	}
}

// TestSandboxRun_StdinForwarding — the optional stdin payload
// reaches the runner verbatim.
func TestSandboxRun_StdinForwarding(t *testing.T) {
	withSandboxXDG(t)

	const payload = "line one\nline two\n"

	var seenStdin string
	withSandboxRunner(t, func(_ context.Context, req sandbox.RunRequest) (sandbox.RunResult, error) {
		seenStdin = req.Stdin
		return sandbox.RunResult{Engine: "noop", Profile: req.Profile.Name}, nil
	})

	if _, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("smoke", "cat", nil, payload, 0)); err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	if seenStdin != payload {
		t.Errorf("seenStdin = %q, want %q", seenStdin, payload)
	}
}

// TestSandboxRun_MissingProfileArg — required-arg validation
// still runs even when the rest of the input is fine.
func TestSandboxRun_MissingProfileArg(t *testing.T) {
	res, err := runSandboxRun(context.Background(),
		mkSandboxRunReq("", "echo", nil, "", 0))
	if err != nil {
		t.Fatalf("runSandboxRun: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError=true on missing profile, got %+v", res)
	}
}
