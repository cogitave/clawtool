package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/daemon"
)

type runCall struct {
	bin  string
	args []string
}

type fakeHostEnv struct {
	calls       []runCall
	rmFails     bool
	addFails    bool
	addNotFound bool
}

func withFakeMCPHost(t *testing.T, home string, env *fakeHostEnv) func() {
	t.Helper()
	prevExec := mcpHostExecutable
	prevPath := mcpHostExecPath
	prevRun := mcpHostRun
	prevHome := mcpHostHomeDir
	prevDaemon := daemonEnsure
	prevToken := daemonToken

	mcpHostExecutable = func() (string, error) { return "/abs/clawtool", nil }
	mcpHostExecPath = func(bin string) (string, error) { return "/abs/" + bin, nil }
	mcpHostHomeDir = func() (string, error) { return home, nil }
	mcpHostRun = func(bin string, args []string) ([]byte, error) {
		env.calls = append(env.calls, runCall{bin: bin, args: append([]string{}, args...)})
		switch {
		case env.addFails && len(args) > 1 && args[1] == "add":
			return []byte("name already exists"), errors.New("exit 1")
		case env.rmFails && len(args) > 1 && args[1] == "remove":
			if env.addNotFound {
				return []byte("not found"), errors.New("exit 1")
			}
			return []byte("permission denied"), errors.New("exit 1")
		default:
			return []byte("ok"), nil
		}
	}
	daemonEnsure = func(_ context.Context) (*daemon.State, error) {
		return &daemon.State{Version: 1, PID: 99999, Port: 38127, TokenFile: filepath.Join(home, ".config/clawtool/listener-token")}, nil
	}
	daemonToken = func() (string, error) { return "deadbeef", nil }

	return func() {
		mcpHostExecutable = prevExec
		mcpHostExecPath = prevPath
		mcpHostRun = prevRun
		mcpHostHomeDir = prevHome
		daemonEnsure = prevDaemon
		daemonToken = prevToken
	}
}

// helpers — return adapter pre-set to a specific mode so tests can
// exercise both paths without depending on package-level init().
func newCodexHTTPAdapter() *mcpHostAdapter {
	return &mcpHostAdapter{cfg: mcpHostBinary{
		name: "codex", binary: "codex", configDir: ".codex",
		mode:        mcpHostModeSharedHTTP,
		addArgsHTTP: codexAddArgsHTTP, addArgsStdio: codexAddArgsStdio, rmArgs: codexRmArgs,
	}}
}

func newCodexStdioAdapter() *mcpHostAdapter {
	return &mcpHostAdapter{cfg: mcpHostBinary{
		name: "codex", binary: "codex", configDir: ".codex",
		mode:         mcpHostModeStdio,
		addArgsHTTP:  codexAddArgsHTTP,
		addArgsStdio: codexAddArgsStdio,
		rmArgs:       codexRmArgs,
	}}
}

func newGeminiHTTPAdapter() *mcpHostAdapter {
	return &mcpHostAdapter{cfg: mcpHostBinary{
		name: "gemini", binary: "gemini", configDir: ".gemini",
		mode:         mcpHostModeSharedHTTP,
		addArgsHTTP:  geminiAddArgsHTTP,
		addArgsStdio: geminiAddArgsStdio,
		rmArgs:       geminiRmArgs,
	}}
}

func TestMCPHost_HTTPClaimUsesURLAndBearerEnv(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	plan, err := a.Claim(Options{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if plan.WasNoop {
		t.Error("first Claim must not be a no-op")
	}
	if len(env.calls) != 1 {
		t.Fatalf("expected 1 host invocation, got %d", len(env.calls))
	}
	got := env.calls[0]
	wantArgs := []string{
		"mcp", "add", "clawtool",
		"--url", "http://127.0.0.1:38127/mcp",
		"--bearer-token-env-var", "CLAWTOOL_TOKEN",
	}
	if got.bin != "/abs/codex" || !equalStrings(got.args, wantArgs) {
		t.Errorf("HTTP Claim args wrong:\n got %s %v\nwant /abs/codex %v", got.bin, got.args, wantArgs)
	}

	marker := filepath.Join(home, ".codex", "clawtool-mcp.lock")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker not written: %v", err)
	}
}

func TestMCPHost_StdioClaimUsesSelfPath(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexStdioAdapter()
	if _, err := a.Claim(Options{}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	got := env.calls[0]
	wantArgs := []string{"mcp", "add", "clawtool", "--", "/abs/clawtool", "serve"}
	if !equalStrings(got.args, wantArgs) {
		t.Errorf("stdio Claim args = %v, want %v", got.args, wantArgs)
	}
}

func TestMCPHost_GeminiHTTPArgsBakeTokenIntoHeader(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newGeminiHTTPAdapter()
	if _, err := a.Claim(Options{}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	got := env.calls[0]
	wantArgs := []string{
		"mcp", "add", "clawtool", "http://127.0.0.1:38127/mcp",
		"-t", "http",
		"-H", "Authorization: Bearer deadbeef",
		"-s", "user",
	}
	if !equalStrings(got.args, wantArgs) {
		t.Errorf("gemini HTTP Claim args = %v, want %v", got.args, wantArgs)
	}
}

func TestMCPHost_ClaimIsIdempotent(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	if len(env.calls) != 1 {
		t.Fatalf("first Claim should invoke once, got %d", len(env.calls))
	}
	plan, err := a.Claim(Options{})
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if !plan.WasNoop {
		t.Error("second Claim should be a no-op")
	}
	if len(env.calls) != 1 {
		t.Fatalf("second Claim must NOT invoke host (got %d total calls)", len(env.calls))
	}
}

func TestMCPHost_ClaimDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	plan, err := a.Claim(Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DryRun {
		t.Error("plan.DryRun should be true")
	}
	if len(env.calls) != 0 {
		t.Errorf("dry-run must not invoke host, got %d calls", len(env.calls))
	}
	marker := filepath.Join(home, ".codex", "clawtool-mcp.lock")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("marker must not exist after dry-run")
	}
}

func TestMCPHost_ClaimSurfacesHostError(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{addFails: true}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	_, err := a.Claim(Options{})
	if err == nil || !strings.Contains(err.Error(), "name already exists") {
		t.Errorf("Claim should surface host stderr, got %v", err)
	}
	marker := filepath.Join(home, ".codex", "clawtool-mcp.lock")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("marker must not be written when host add fails")
	}
}

func TestMCPHost_ReleaseRemovesMCPAndMarker(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	env.calls = nil
	plan, err := a.Release(Options{})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if plan.WasNoop {
		t.Error("Release after Claim should not be a no-op")
	}
	if len(env.calls) != 1 {
		t.Fatalf("expected 1 host invocation, got %d", len(env.calls))
	}
	got := env.calls[0]
	if got.bin != "/abs/codex" || !equalStrings(got.args, []string{"mcp", "remove", "clawtool"}) {
		t.Errorf("Release invoked wrong command: %s %v", got.bin, got.args)
	}
	marker := filepath.Join(home, ".codex", "clawtool-mcp.lock")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("marker must be removed after Release")
	}
}

func TestMCPHost_ReleaseWithoutClaimIsNoop(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	plan, err := a.Release(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.WasNoop {
		t.Error("Release without prior Claim must be a no-op")
	}
	if len(env.calls) != 0 {
		t.Errorf("noop release must not invoke host, got %d calls", len(env.calls))
	}
}

func TestMCPHost_ReleaseSoftFailsOnHostNotFound(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	env.rmFails = true
	env.addNotFound = true

	if _, err := a.Release(Options{}); err != nil {
		t.Fatalf("Release should soft-fail on host 'not found', got: %v", err)
	}
	marker := filepath.Join(home, ".codex", "clawtool-mcp.lock")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("marker must be removed even when host already lost the entry")
	}
}

func TestMCPHost_StatusReflectsClaim(t *testing.T) {
	home := t.TempDir()
	env := &fakeHostEnv{}
	defer withFakeMCPHost(t, home, env)()

	a := newCodexHTTPAdapter()
	s, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Claimed {
		t.Error("Status before Claim should report not claimed")
	}
	if !strings.Contains(s.Notes, "clawtool agents claim codex") {
		t.Errorf("Status should hint at the claim command, got: %q", s.Notes)
	}

	if _, err := a.Claim(Options{}); err != nil {
		t.Fatal(err)
	}
	s2, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Claimed {
		t.Error("Status after Claim should report claimed=true")
	}
	want := []string{"mcp:clawtool (shared-http)"}
	if !equalStrings(s2.DisabledByUs, want) {
		t.Errorf("DisabledByUs = %v, want %v", s2.DisabledByUs, want)
	}
}

func TestRegistry_HasCodexOpencodeGemini(t *testing.T) {
	for _, name := range []string{"codex", "opencode", "gemini"} {
		if _, err := Find(name); err != nil {
			t.Errorf("Registry missing %q: %v", name, err)
		}
	}
}
