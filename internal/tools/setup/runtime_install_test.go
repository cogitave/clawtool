package setuptools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// installRuntimeInstallSeams swaps both runtimeInstallExec and
// runtimeInstallProbe for the test's lifetime. Mirrors
// installSpawnToolSeams's t.Cleanup discipline so a leaked stub
// never reaches the next test.
func installRuntimeInstallSeams(
	t *testing.T,
	exec func(ctx context.Context, cmd []string) (string, error),
	probe func(bin, versionArg string) (string, string, error),
) {
	t.Helper()
	prevE := runtimeInstallExec
	prevP := runtimeInstallProbe
	runtimeInstallExec = exec
	runtimeInstallProbe = probe
	t.Cleanup(func() {
		runtimeInstallExec = prevE
		runtimeInstallProbe = prevP
	})
}

// mkRuntimeInstallReq fabricates an MCP CallToolRequest with the
// supplied (optional) `runtime` + `force` arguments.
func mkRuntimeInstallReq(runtime string, force bool) mcp.CallToolRequest {
	args := map[string]any{}
	if runtime != "" {
		args["runtime"] = runtime
	}
	if force {
		args["force"] = true
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "RuntimeInstall",
			Arguments: args,
		},
	}
}

// TestRuntimeInstall_PlanLookup: every supported runtime resolves
// to a non-empty install plan; an unknown runtime returns an error.
func TestRuntimeInstall_PlanLookup(t *testing.T) {
	for _, rt := range runtimeInstallSupported {
		plan, err := runtimeInstallPlanFor(rt)
		if err != nil {
			t.Errorf("%s: planFor returned err: %v", rt, err)
			continue
		}
		if plan.Bin == "" || len(plan.Cmd) == 0 {
			t.Errorf("%s: plan missing fields: %+v", rt, plan)
		}
	}
	if _, err := runtimeInstallPlanFor("hermes"); err == nil {
		t.Errorf("expected error for unsupported runtime; got nil")
	}
}

// TestRuntimeInstall_RejectsUnknownRuntime: an unknown runtime
// label returns IsError without touching either seam.
func TestRuntimeInstall_RejectsUnknownRuntime(t *testing.T) {
	execCalls := 0
	probeCalls := 0
	installRuntimeInstallSeams(t,
		func(context.Context, []string) (string, error) { execCalls++; return "", nil },
		func(string, string) (string, string, error) { probeCalls++; return "", "", nil },
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("hermes", false))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on unknown runtime; got %+v", res)
	}
	if execCalls != 0 || probeCalls != 0 {
		t.Errorf("rejected runtime reached seams: exec=%d probe=%d", execCalls, probeCalls)
	}
	if !strings.Contains(stringifyResult(res), "unknown runtime") {
		t.Errorf("expected 'unknown runtime' in error; got %+v", res.Content)
	}
}

// TestRuntimeInstall_RejectsMissingRuntime: empty `runtime` arg
// returns IsError.
func TestRuntimeInstall_RejectsMissingRuntime(t *testing.T) {
	installRuntimeInstallSeams(t,
		func(context.Context, []string) (string, error) { return "", nil },
		func(string, string) (string, string, error) { return "", "", nil },
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("", false))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on missing runtime; got %+v", res)
	}
}

// TestRuntimeInstall_IdempotentSkip: when the binary is already on
// PATH and force=false, the tool returns Skipped=true with the
// existing version + path WITHOUT calling the exec seam.
func TestRuntimeInstall_IdempotentSkip(t *testing.T) {
	execCalls := 0
	installRuntimeInstallSeams(t,
		func(context.Context, []string) (string, error) {
			execCalls++
			return "", nil
		},
		func(bin, _ string) (string, string, error) {
			return "/usr/local/bin/" + bin, "codex 0.42.0", nil
		},
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("codex", false))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if res.IsError {
		t.Fatalf("idempotent skip should not be IsError; got %+v", res)
	}
	if execCalls != 0 {
		t.Errorf("idempotent skip ran the exec seam (%d calls)", execCalls)
	}
	got, ok := res.StructuredContent.(RuntimeInstallResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want RuntimeInstallResult", res.StructuredContent)
	}
	if !got.Skipped || !got.Installed {
		t.Errorf("expected Skipped+Installed; got %+v", got)
	}
	if got.Runtime != "codex" || got.BinaryPath != "/usr/local/bin/codex" || got.Version != "codex 0.42.0" {
		t.Errorf("unexpected result: %+v", got)
	}
}

// TestRuntimeInstall_ForceRunsExec: force=true bypasses the
// idempotency probe and re-runs the install command even if the
// binary is already on PATH.
func TestRuntimeInstall_ForceRunsExec(t *testing.T) {
	execCalls := 0
	var seenCmd []string
	installRuntimeInstallSeams(t,
		func(_ context.Context, cmd []string) (string, error) {
			execCalls++
			seenCmd = cmd
			return "ok", nil
		},
		func(bin, _ string) (string, string, error) {
			return "/usr/local/bin/" + bin, "gemini 1.0.0", nil
		},
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("gemini", true))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if res.IsError {
		t.Fatalf("force install flagged IsError; got %+v", res)
	}
	if execCalls != 1 {
		t.Errorf("force=true should run exec once; got %d calls", execCalls)
	}
	if len(seenCmd) < 4 || seenCmd[0] != "npm" || seenCmd[3] != "@google/gemini-cli" {
		t.Errorf("unexpected install cmd for gemini: %v", seenCmd)
	}
	got := res.StructuredContent.(RuntimeInstallResult)
	if got.Skipped {
		t.Errorf("force install should not be Skipped; got %+v", got)
	}
}

// TestRuntimeInstall_FreshInstallFlow: probe returns ENOENT first
// (binary missing), exec runs, then probe returns the new path +
// version. The result is Installed=true, Skipped=false.
func TestRuntimeInstall_FreshInstallFlow(t *testing.T) {
	execCalls := 0
	probeCalls := 0
	installRuntimeInstallSeams(t,
		func(_ context.Context, cmd []string) (string, error) {
			execCalls++
			return "added 1 package", nil
		},
		func(bin, _ string) (string, string, error) {
			probeCalls++
			if probeCalls == 1 {
				// Pre-install probe — binary missing.
				return "", "", errors.New("exec: \"" + bin + "\": executable file not found in $PATH")
			}
			// Post-install probe — binary now on PATH.
			return "/home/test/.local/bin/" + bin, bin + " 0.1.0", nil
		},
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("aider", false))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if res.IsError {
		t.Fatalf("fresh install flagged IsError; got %+v", res)
	}
	if execCalls != 1 {
		t.Errorf("expected 1 exec call; got %d", execCalls)
	}
	if probeCalls != 2 {
		t.Errorf("expected 2 probe calls (pre + post install); got %d", probeCalls)
	}
	got := res.StructuredContent.(RuntimeInstallResult)
	if !got.Installed || got.Skipped {
		t.Errorf("expected Installed && !Skipped; got %+v", got)
	}
	if got.Version != "aider 0.1.0" || got.BinaryPath != "/home/test/.local/bin/aider" {
		t.Errorf("unexpected aider result: %+v", got)
	}
}

// TestRuntimeInstall_ExecFailureSurfacesError: exec returns an
// error → tool returns IsError; the post-install probe is skipped.
func TestRuntimeInstall_ExecFailureSurfacesError(t *testing.T) {
	probeCalls := 0
	installRuntimeInstallSeams(t,
		func(context.Context, []string) (string, error) {
			return "", errors.New("npm ERR! 403 Forbidden")
		},
		func(bin, _ string) (string, string, error) {
			probeCalls++
			if probeCalls == 1 {
				return "", "", errors.New("not on PATH")
			}
			return "/should/never/reach", "", nil
		},
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("codex", false))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on exec failure; got %+v", res)
	}
	if probeCalls != 1 {
		t.Errorf("exec-failure path should skip post-install probe; got %d probe calls", probeCalls)
	}
	if !strings.Contains(stringifyResult(res), "403") {
		t.Errorf("error should surface the underlying npm message; got %+v", res.Content)
	}
}

// TestRuntimeInstall_PostInstallProbeFailureSurfacesError: exec
// succeeds but the binary is still not on PATH → IsError with a
// hint about the package manager's bin dir.
func TestRuntimeInstall_PostInstallProbeFailureSurfacesError(t *testing.T) {
	installRuntimeInstallSeams(t,
		func(context.Context, []string) (string, error) { return "ok", nil },
		func(string, string) (string, string, error) {
			return "", "", errors.New("not found")
		},
	)
	res, err := runRuntimeInstall(context.Background(), mkRuntimeInstallReq("opencode", false))
	if err != nil {
		t.Fatalf("runRuntimeInstall: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on post-probe failure; got %+v", res)
	}
	if !strings.Contains(stringifyResult(res), "PATH") {
		t.Errorf("expected PATH hint in error; got %+v", res.Content)
	}
}
