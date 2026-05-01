// Package setuptools — `Spawn` MCP tool. Mirror of the
// `clawtool spawn` CLI verb: opens a NEW terminal window/pane
// running the requested agent CLI, then registers the freshly-
// spawned agent as a peer in the daemon's BIAM registry so the
// caller can dispatch to it via SendMessage / PeerSend by
// peer_id within ~1s of the call returning.
//
// Why mirror (and not call into internal/cli): the cli package
// already imports tools/core (for ToolsList), so a reverse
// dependency from setuptools → cli would close the cycle. The
// launch + register surface is small (~100 LOC) so a lightweight
// re-implementation here keeps the dependency graph clean while
// the CLI verb keeps owning the user-visible help text + flag
// parsing.
//
// Test seams: `spawnLauncher` and `spawnRegisterHTTP` are
// package-level vars tests rebind to deterministic stubs so the
// suite never spawns a real terminal or hits the daemon.
package setuptools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// spawnSupportedBackends is the canonical set of backend labels
// the Spawn tool accepts. Mirrors supportedSpawnBackends in
// internal/cli/spawn.go — kept duplicated rather than shared
// because cli ↔ tools imports would cycle.
var spawnSupportedBackends = []string{"claude-code", "codex", "gemini", "opencode"}

// spawnElevationFlags mirrors agents.elevationFlags for the
// subset of families this tool spawns. Inlined to avoid pulling
// the agents package + its transports into the MCP tool's import
// graph (the agents package transitively imports the BIAM runner
// glue, which is heavier than the tool needs).
var spawnElevationFlags = map[string]string{
	"claude":   "--dangerously-skip-permissions",
	"codex":    "--dangerously-bypass-approvals-and-sandbox",
	"gemini":   "--yolo",
	"opencode": "--yolo",
}

// spawnArgvForFamily composes the binary + argv for a backend
// family, matching the cli.buildBootstrapArgv shape.
func spawnArgvForFamily(family string) (string, []string) {
	flag := spawnElevationFlags[family]
	switch family {
	case "claude":
		return "claude", []string{flag, "--print", "--output-format", "text"}
	case "codex":
		return "codex", []string{"exec", "--skip-git-repo-check", "--json", flag}
	case "gemini":
		return "gemini", []string{"-p", "-", "--skip-trust", "--output-format", "text", flag}
	case "opencode":
		return "opencode", []string{"run", flag}
	}
	return family, []string{flag}
}

// spawnBackendFamily resolves a peer-registry backend label to
// the upstream agent family name.
func spawnBackendFamily(backend string) string {
	if backend == "claude-code" {
		return "claude"
	}
	return backend
}

// SpawnPlan captures the deterministic shape of a planned spawn —
// returned verbatim by the tool's StructuredContent so a calling
// agent can introspect what was just launched.
type SpawnPlan struct {
	PeerID   string   `json:"peer_id,omitempty"`
	Backend  string   `json:"backend"`
	Family   string   `json:"family"`
	Terminal string   `json:"terminal"`
	PID      int      `json:"pid,omitempty"`
	Bin      string   `json:"bin"`
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	DryRun   bool     `json:"dry_run,omitempty"`
}

// spawnLauncher is the indirection seam tests rebind. Production
// shells out to the chosen terminal multiplexer / window manager;
// tests record the request and return a deterministic terminal +
// PID without forking.
type spawnLauncher interface {
	Launch(ctx context.Context, plan SpawnLaunchPlan) (terminal string, pid int, err error)
}

// SpawnLaunchPlan is the payload spawnLauncher.Launch consumes.
type SpawnLaunchPlan struct {
	Terminal string
	Bin      string
	Argv     []string
	Cwd      string
	Prompt   string
}

// defaultSpawnLauncher is the package-level seam.
var defaultSpawnLauncher spawnLauncher = realSpawnLauncher{}

// spawnRegisterHTTP is the daemon round-trip seam.
var spawnRegisterHTTP = func(method, path string, body *bytes.Reader, out any) error {
	return daemon.HTTPRequest(method, path, body, out)
}

// RegisterSpawn wires the Spawn MCP tool to s.
func RegisterSpawn(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"Spawn",
			mcp.WithDescription(
				"Open a NEW terminal window/pane running the requested agent backend (claude-code|codex|gemini|opencode), auto-register it in the BIAM peer registry, and return the assigned peer_id. Pair with SendMessage / PeerSend to talk to the spawned agent. The spawned agent's hooks fire as if the operator opened it manually.",
			),
			mcp.WithString("backend",
				mcp.Required(),
				mcp.Description("One of: claude-code, codex, gemini, opencode.")),
			mcp.WithString("display_name",
				mcp.Description("Human-friendly label for the peer registry. Defaults to '<user>@<host>/<backend>:spawn'.")),
			mcp.WithString("cwd",
				mcp.Description("Working directory the spawned agent runs in. Defaults to the daemon's cwd.")),
			mcp.WithString("prompt",
				mcp.Description("Optional first user message handed to the agent on spawn.")),
			mcp.WithString("terminal",
				mcp.Description("Force a launcher: tmux | screen | wt | gnome-terminal | konsole | kitty | macos | headless. Default: auto-detect.")),
			mcp.WithBoolean("dry_run",
				mcp.Description("Print the spawn plan without executing — no terminal opens, no peer registers.")),
		),
		runSpawnTool,
	)
}

// runSpawnTool is the MCP handler for Spawn.
func runSpawnTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	backend := strings.TrimSpace(req.GetString("backend", ""))
	if backend == "" {
		return mcp.NewToolResultError("Spawn: backend is required"), nil
	}
	if !containsString(spawnSupportedBackends, backend) {
		return mcp.NewToolResultError(fmt.Sprintf(
			"Spawn: unknown backend %q (one of: %s)",
			backend, strings.Join(spawnSupportedBackends, ", "))), nil
	}

	displayName := strings.TrimSpace(req.GetString("display_name", ""))
	cwd := strings.TrimSpace(req.GetString("cwd", ""))
	prompt := req.GetString("prompt", "")
	terminal := strings.TrimSpace(req.GetString("terminal", ""))
	dryRun := req.GetBool("dry_run", false)

	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if displayName == "" {
		host, _ := os.Hostname()
		user := firstNonEmptySpawn(os.Getenv("USER"), os.Getenv("USERNAME"), "user")
		displayName = fmt.Sprintf("%s@%s/%s:spawn", user, host, backend)
	}

	family := spawnBackendFamily(backend)
	bin, argv := spawnArgvForFamily(family)
	plan := SpawnLaunchPlan{
		Terminal: terminal,
		Bin:      bin,
		Argv:     argv,
		Cwd:      cwd,
		Prompt:   prompt,
	}

	if dryRun {
		chosen := plan.Terminal
		if chosen == "" {
			chosen = autodetectSpawnTerminal()
		}
		out := SpawnPlan{
			Backend:  backend,
			Family:   family,
			Terminal: chosen,
			Bin:      bin,
			Argv:     argv,
			Cwd:      cwd,
			DryRun:   true,
		}
		return resultOfJSON("Spawn", out)
	}

	chosen, pid, lerr := defaultSpawnLauncher.Launch(ctx, plan)
	if lerr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Spawn: launch %s: %v", bin, lerr)), nil
	}

	in := a2a.RegisterInput{
		DisplayName: displayName,
		Path:        cwd,
		Backend:     backend,
		Role:        a2a.RoleAgent,
		PID:         pid,
		Metadata: map[string]string{
			"spawned_by":     "clawtool",
			"spawn_terminal": chosen,
			"spawn_bin":      bin,
		},
	}
	body, _ := json.Marshal(in)
	var peer a2a.Peer
	if err := spawnRegisterHTTP(http.MethodPost, "/v1/peers/register", bytes.NewReader(body), &peer); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"Spawn: launched %s in %s but peer registration failed: %v",
			bin, chosen, err)), nil
	}

	return resultOfJSON("Spawn", SpawnPlan{
		PeerID:   peer.PeerID,
		Backend:  backend,
		Family:   family,
		Terminal: chosen,
		PID:      pid,
		Bin:      bin,
		Argv:     argv,
		Cwd:      cwd,
	})
}

// autodetectSpawnTerminal mirrors the cli verb's cascade.
func autodetectSpawnTerminal() string {
	if os.Getenv("TMUX") != "" {
		return "tmux"
	}
	if os.Getenv("STY") != "" {
		return "screen"
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		if _, err := exec.LookPath("wt.exe"); err == nil {
			return "wt"
		}
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("gnome-terminal"); err == nil {
			return "gnome-terminal"
		}
		if _, err := exec.LookPath("konsole"); err == nil {
			return "konsole"
		}
		if _, err := exec.LookPath("kitty"); err == nil {
			return "kitty"
		}
	}
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return "headless"
}

// realSpawnLauncher is the production launcher. Mirrors the
// internal/cli realLauncher's cascade — same argv shapes, same
// fallback to nohup. Kept near the production handler so any
// platform fix lands in both surfaces together.
type realSpawnLauncher struct{}

func (realSpawnLauncher) Launch(ctx context.Context, p SpawnLaunchPlan) (string, int, error) {
	chosen := p.Terminal
	if chosen == "" {
		chosen = autodetectSpawnTerminal()
	}
	full := append([]string{p.Bin}, p.Argv...)
	cmdline := strings.Join(full, " ")
	switch chosen {
	case "tmux":
		args := []string{"new-window"}
		if p.Cwd != "" {
			args = append(args, "-c", p.Cwd)
		}
		args = append(args, cmdline)
		c := exec.CommandContext(ctx, "tmux", args...)
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		pid := 0
		if c.Process != nil {
			pid = c.Process.Pid
		}
		return chosen, pid, nil
	case "screen":
		c := exec.CommandContext(ctx, "screen", "-X", "screen", p.Bin)
		c.Args = append(c.Args, p.Argv...)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "wt":
		args := []string{"new-tab"}
		if p.Cwd != "" {
			args = append(args, "-d", p.Cwd)
		}
		args = append(args, p.Bin)
		args = append(args, p.Argv...)
		c := exec.CommandContext(ctx, "wt.exe", args...)
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "gnome-terminal":
		c := exec.CommandContext(ctx, "gnome-terminal", "--tab", "--", "bash", "-lc", cmdline)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "konsole":
		c := exec.CommandContext(ctx, "konsole", "--new-tab", "-e", "bash", "-lc", cmdline)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "kitty":
		c := exec.CommandContext(ctx, "kitty", "--single-instance", "--", "bash", "-lc", cmdline)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "macos":
		script := fmt.Sprintf("cd %q && %s", p.Cwd, cmdline)
		c := exec.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`tell application "Terminal" to do script %q`, script))
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "headless":
		c := exec.CommandContext(ctx, "nohup", append([]string{p.Bin}, p.Argv...)...)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		pid := 0
		if c.Process != nil {
			pid = c.Process.Pid
		}
		return chosen, pid, nil
	}
	return chosen, 0, fmt.Errorf("unsupported terminal %q", chosen)
}

// containsString is a tiny helper kept local so this file doesn't
// need to import a slices helper just for one membership test.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// firstNonEmptySpawn picks the first non-empty string. Mirrors
// firstNonEmpty in internal/cli/peer.go without sharing — we
// can't import cli (would cycle).
func firstNonEmptySpawn(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
