package setuptools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/mark3labs/mcp-go/mcp"
)

// recSpawnLauncher is the test recorder. Captures the plan that
// would have been launched without forking a real terminal.
type recSpawnLauncher struct {
	calls   int
	gotPlan SpawnLaunchPlan
	retTerm string
	retPID  int
	retErr  error
}

func (r *recSpawnLauncher) Launch(_ context.Context, p SpawnLaunchPlan) (string, int, error) {
	r.calls++
	r.gotPlan = p
	chosen := r.retTerm
	if chosen == "" {
		chosen = p.Terminal
	}
	return chosen, r.retPID, r.retErr
}

// installSpawnToolSeams swaps both package-level seams for the
// test's lifetime. Mirrors withStubbedHTTP's t.Cleanup discipline.
func installSpawnToolSeams(t *testing.T, l spawnLauncher, hf func(string, string, *bytes.Reader, any) error) {
	t.Helper()
	prevL := defaultSpawnLauncher
	prevH := spawnRegisterHTTP
	defaultSpawnLauncher = l
	spawnRegisterHTTP = hf
	t.Cleanup(func() {
		defaultSpawnLauncher = prevL
		spawnRegisterHTTP = prevH
	})
}

// resetSpawnToolEnv blanks every env knob autodetectSpawnTerminal
// reads so a developer's tmux session doesn't leak into the test.
func resetSpawnToolEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TMUX", "STY", "WSL_DISTRO_NAME"} {
		t.Setenv(k, "")
	}
}

// mkSpawnReq builds an MCP CallToolRequest with the supplied
// argument bag (only non-empty entries are included so default-
// behavior assertions are noise-free).
func mkSpawnReq(args map[string]any) mcp.CallToolRequest {
	clean := map[string]any{}
	for k, v := range args {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		clean[k] = v
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "Spawn",
			Arguments: clean,
		},
	}
}

// TestSpawnTool_DryRun: dry_run=true returns a SpawnPlan with
// DryRun=true; never reaches the launcher or the daemon.
func TestSpawnTool_DryRun(t *testing.T) {
	resetSpawnToolEnv(t)
	rl := &recSpawnLauncher{}
	calls := 0
	installSpawnToolSeams(t, rl, func(string, string, *bytes.Reader, any) error {
		calls++
		return nil
	})

	res, err := runSpawnTool(context.Background(), mkSpawnReq(map[string]any{
		"backend":  "codex",
		"dry_run":  true,
		"cwd":      "/tmp",
		"terminal": "tmux",
	}))
	if err != nil {
		t.Fatalf("runSpawnTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("dry-run flagged IsError; res=%+v", res)
	}
	if rl.calls != 0 || calls != 0 {
		t.Errorf("dry-run touched seams: launcher=%d daemon=%d", rl.calls, calls)
	}
	plan, ok := res.StructuredContent.(SpawnPlan)
	if !ok {
		t.Fatalf("StructuredContent = %T, want SpawnPlan", res.StructuredContent)
	}
	if !plan.DryRun || plan.Backend != "codex" || plan.Family != "codex" || plan.Terminal != "tmux" || plan.Bin != "codex" {
		t.Errorf("unexpected dry-run plan: %+v", plan)
	}
}

// TestSpawnTool_AutoDetectTerminal: with $TMUX set the dry-run
// returns terminal=tmux; with the env cleared the cascade falls
// through to "headless" on a default Linux test host.
func TestSpawnTool_AutoDetectTerminal(t *testing.T) {
	resetSpawnToolEnv(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	if got := autodetectSpawnTerminal(); got != "tmux" {
		t.Errorf("with $TMUX: got %q, want tmux", got)
	}
	t.Setenv("TMUX", "")
	t.Setenv("STY", "12345.pts-0.host")
	if got := autodetectSpawnTerminal(); got != "screen" {
		t.Errorf("with $STY: got %q, want screen", got)
	}

	// End-to-end: an unforced terminal under tmux env yields
	// terminal=tmux in the plan.
	resetSpawnToolEnv(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
	rl := &recSpawnLauncher{}
	installSpawnToolSeams(t, rl, func(string, string, *bytes.Reader, any) error { return nil })
	res, err := runSpawnTool(context.Background(), mkSpawnReq(map[string]any{
		"backend": "claude-code",
		"dry_run": true,
		"cwd":     "/tmp",
	}))
	if err != nil {
		t.Fatalf("runSpawnTool: %v", err)
	}
	plan := res.StructuredContent.(SpawnPlan)
	if plan.Terminal != "tmux" {
		t.Errorf("autodetect didn't pick tmux; plan=%+v", plan)
	}
}

// TestSpawnTool_RegistersPeer: a real (non-dry-run) call posts
// /v1/peers/register with the right backend + spawn metadata and
// returns the assigned peer_id in the SpawnPlan.
func TestSpawnTool_RegistersPeer(t *testing.T) {
	resetSpawnToolEnv(t)
	rl := &recSpawnLauncher{retTerm: "tmux", retPID: 4321}

	var seenMethod, seenPath string
	var seenBody []byte
	installSpawnToolSeams(t, rl, func(method, path string, body *bytes.Reader, out any) error {
		seenMethod = method
		seenPath = path
		if body != nil {
			seenBody = make([]byte, body.Len())
			_, _ = body.Read(seenBody)
		}
		peer := &a2a.Peer{
			PeerID:      "peer-mcp-spawn",
			DisplayName: "spawned-from-mcp",
			Backend:     "gemini",
		}
		bb, _ := json.Marshal(peer)
		return json.Unmarshal(bb, out)
	})

	res, err := runSpawnTool(context.Background(), mkSpawnReq(map[string]any{
		"backend":      "gemini",
		"display_name": "gemini-spawn",
		"cwd":          "/tmp",
		"terminal":     "tmux",
	}))
	if err != nil {
		t.Fatalf("runSpawnTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected non-error result; got %+v", res)
	}
	if rl.calls != 1 {
		t.Errorf("launcher calls=%d, want 1", rl.calls)
	}
	if rl.gotPlan.Bin != "gemini" {
		t.Errorf("plan.Bin=%q, want gemini", rl.gotPlan.Bin)
	}
	if seenMethod != http.MethodPost {
		t.Errorf("daemon method=%q, want POST", seenMethod)
	}
	if seenPath != "/v1/peers/register" {
		t.Errorf("daemon path=%q, want /v1/peers/register", seenPath)
	}
	var in a2a.RegisterInput
	if err := json.Unmarshal(seenBody, &in); err != nil {
		t.Fatalf("body not RegisterInput JSON: %v\n%s", err, seenBody)
	}
	if in.Backend != "gemini" {
		t.Errorf("RegisterInput.Backend=%q, want gemini", in.Backend)
	}
	if in.Role != a2a.RoleAgent {
		t.Errorf("RegisterInput.Role=%q, want %q", in.Role, a2a.RoleAgent)
	}
	if in.Metadata["spawned_by"] != "clawtool" {
		t.Errorf("metadata.spawned_by=%q, want clawtool", in.Metadata["spawned_by"])
	}
	if in.Metadata["spawn_terminal"] != "tmux" {
		t.Errorf("metadata.spawn_terminal=%q, want tmux", in.Metadata["spawn_terminal"])
	}
	plan, ok := res.StructuredContent.(SpawnPlan)
	if !ok {
		t.Fatalf("StructuredContent type = %T", res.StructuredContent)
	}
	if plan.PeerID != "peer-mcp-spawn" {
		t.Errorf("PeerID=%q, want peer-mcp-spawn", plan.PeerID)
	}
}

// TestSpawnTool_RejectsUnknownBackend: a backend not in
// spawnSupportedBackends returns IsError without touching seams.
func TestSpawnTool_RejectsUnknownBackend(t *testing.T) {
	resetSpawnToolEnv(t)
	rl := &recSpawnLauncher{}
	calls := 0
	installSpawnToolSeams(t, rl, func(string, string, *bytes.Reader, any) error {
		calls++
		return nil
	})
	res, err := runSpawnTool(context.Background(), mkSpawnReq(map[string]any{
		"backend": "hermes",
		"dry_run": true,
	}))
	if err != nil {
		t.Fatalf("runSpawnTool returned Go err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on unknown backend; got %+v", res)
	}
	if rl.calls != 0 || calls != 0 {
		t.Errorf("rejected backend reached seams: launcher=%d daemon=%d", rl.calls, calls)
	}
	if !strings.Contains(stringifyResult(res), "unknown backend") {
		t.Errorf("expected 'unknown backend' in error content; got %+v", res.Content)
	}
}

// TestSpawnTool_LauncherFailureSurfacesError: a launcher error
// surfaces as an IsError result; daemon round-trip is skipped.
func TestSpawnTool_LauncherFailureSurfacesError(t *testing.T) {
	resetSpawnToolEnv(t)
	rl := &recSpawnLauncher{retErr: errors.New("tmux: command not found")}
	calls := 0
	installSpawnToolSeams(t, rl, func(string, string, *bytes.Reader, any) error {
		calls++
		return nil
	})
	res, err := runSpawnTool(context.Background(), mkSpawnReq(map[string]any{
		"backend":  "codex",
		"terminal": "tmux",
		"cwd":      "/tmp",
	}))
	if err != nil {
		t.Fatalf("runSpawnTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError on launcher failure; got %+v", res)
	}
	if calls != 0 {
		t.Errorf("daemon round-trip happened despite launcher failure (%d calls)", calls)
	}
	if !strings.Contains(stringifyResult(res), "tmux") {
		t.Errorf("error should mention the failed launcher; got %+v", res.Content)
	}
}

// stringifyResult flattens an MCP tool result's content blocks
// into a single string for substring assertions.
func stringifyResult(res *mcp.CallToolResult) string {
	parts := []string{}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
