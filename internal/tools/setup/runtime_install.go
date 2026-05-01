// Package setuptools — `RuntimeInstall` MCP tool. Lets a calling
// agent (Claude) install one of clawtool's supported backend CLIs
// (codex | gemini | opencode | aider) AT RUNTIME from chat. The
// install.sh script lands tmux + node + claude-code so the user
// can open Claude on a fresh box; everything else is on-demand.
//
// Why an MCP tool and not just a CLI verb: the call path the
// operator wants is "open claude → ask for codex → claude installs
// it". Forcing the operator to drop to a shell to run
// `clawtool install codex` defeats the chat-first contract.
//
// Test seam: `runtimeInstallExec` is a package-level var the tests
// rebind to a deterministic stub so the suite never spawns a real
// `npm` / `pip` / `curl | sh`. Mirrors the spawnLauncher pattern.
package setuptools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// runtimeInstallSupported is the canonical set of runtime labels
// the tool accepts. Mirrors the agent_detect catalogue's keys for
// the four runtimes the operator can install from chat. claude is
// deliberately excluded — the install.sh script lands it before
// the operator ever opens a shell, so a chat-driven re-install
// would loop on the bootstrap.
var runtimeInstallSupported = []string{"codex", "gemini", "opencode", "aider"}

// runtimeInstallPlan is the per-runtime install dispatch. Held as
// a struct (not a map of fns) so tests can introspect what would
// have run without exec'ing anything.
type runtimeInstallPlan struct {
	// Bin is the binary `command -v` resolves to once the install
	// completes — used both for the post-install version probe
	// and for the result's binary_path.
	Bin string
	// Cmd is the install command ARGV. Element 0 is the launcher
	// (`npm`, `pip`, `sh`); rest are passed verbatim.
	Cmd []string
	// VersionArg is the flag we pass to Bin to extract a version
	// string. Usually `--version`; aider uses `--version` too.
	// Empty means "skip the version probe".
	VersionArg string
}

// runtimeInstallPlanFor returns the install plan for a runtime on
// the current platform. macOS / Linux share the npm + pip paths;
// opencode's curl-bash installer works the same on both.
func runtimeInstallPlanFor(rt string) (runtimeInstallPlan, error) {
	switch rt {
	case "codex":
		return runtimeInstallPlan{
			Bin:        "codex",
			Cmd:        []string{"npm", "install", "-g", "@openai/codex"},
			VersionArg: "--version",
		}, nil
	case "gemini":
		return runtimeInstallPlan{
			Bin:        "gemini",
			Cmd:        []string{"npm", "install", "-g", "@google/gemini-cli"},
			VersionArg: "--version",
		}, nil
	case "opencode":
		// opencode ships a curl-pipe-bash installer. We invoke
		// `sh -c` so the same plan works under either shell.
		return runtimeInstallPlan{
			Bin:        "opencode",
			Cmd:        []string{"sh", "-c", "curl -fsSL https://opencode.ai/install | bash"},
			VersionArg: "--version",
		}, nil
	case "aider":
		return runtimeInstallPlan{
			Bin:        "aider",
			Cmd:        []string{"pip", "install", "--user", "aider-chat"},
			VersionArg: "--version",
		}, nil
	}
	return runtimeInstallPlan{}, fmt.Errorf("unsupported runtime %q (one of: %s)", rt, strings.Join(runtimeInstallSupported, ", "))
}

// RuntimeInstallResult is the deterministic shape returned to the
// MCP caller. Field names match the operator-facing contract spelled
// out in the tool's UsageHint so a caller agent can predict them
// without reading source.
type RuntimeInstallResult struct {
	Runtime    string `json:"runtime"`
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	BinaryPath string `json:"binary_path,omitempty"`
	Platform   string `json:"platform"`
	Skipped    bool   `json:"skipped,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// runtimeInstallExec is the package-level seam. Production runs
// the install command via os/exec; tests stub it so the suite
// never npm-installs a real package. The `probe` callback resolves
// the post-install binary path + version; tests stub that too so
// the assertion can stay deterministic across CI hosts.
var runtimeInstallExec = func(ctx context.Context, cmd []string) (stdout string, err error) {
	if len(cmd) == 0 {
		return "", errors.New("empty command")
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	out, e := c.CombinedOutput()
	return string(out), e
}

// runtimeInstallProbe resolves the absolute path + --version output
// for a binary post-install. Pulled into its own seam so a test
// can fake "installed" without the binary actually existing on the
// CI host.
var runtimeInstallProbe = func(bin, versionArg string) (path, version string, err error) {
	p, lerr := exec.LookPath(bin)
	if lerr != nil {
		return "", "", lerr
	}
	if versionArg == "" {
		return p, "", nil
	}
	out, verr := exec.Command(p, versionArg).CombinedOutput()
	if verr != nil {
		// A non-zero exit on --version is non-fatal; report the
		// path + an empty version rather than failing the install.
		return p, "", nil
	}
	return p, strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), nil
}

// RegisterRuntimeInstall wires the RuntimeInstall MCP tool to s.
func RegisterRuntimeInstall(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"RuntimeInstall",
			mcp.WithDescription(
				"Install one of clawtool's supported backend CLIs (codex|gemini|opencode|aider) at runtime from chat. Detects platform, runs the right install command (npm / pip / curl-pipe-sh), waits for completion, and returns the resolved binary path + version. Idempotent: an already-installed runtime returns its existing version and skips the install. Pair with Spawn to immediately open the freshly-installed runtime in a tmux pane.",
			),
			mcp.WithString("runtime",
				mcp.Required(),
				mcp.Description("One of: codex, gemini, opencode, aider.")),
			mcp.WithBoolean("force",
				mcp.Description("Re-run the install even if the binary is already on PATH. Default false (idempotent).")),
		),
		runRuntimeInstall,
	)
}

// runRuntimeInstall is the MCP handler for RuntimeInstall.
func runRuntimeInstall(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rt := strings.TrimSpace(req.GetString("runtime", ""))
	if rt == "" {
		return mcp.NewToolResultError("RuntimeInstall: runtime is required"), nil
	}
	if !containsString(runtimeInstallSupported, rt) {
		return mcp.NewToolResultError(fmt.Sprintf(
			"RuntimeInstall: unknown runtime %q (one of: %s)",
			rt, strings.Join(runtimeInstallSupported, ", "))), nil
	}
	force := req.GetBool("force", false)
	plan, err := runtimeInstallPlanFor(rt)
	if err != nil {
		return mcp.NewToolResultError("RuntimeInstall: " + err.Error()), nil
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH

	// Idempotency probe: if the binary is already on PATH and the
	// caller didn't force, return the existing path + version
	// without spawning a single child process.
	if !force {
		if p, ver, perr := runtimeInstallProbe(plan.Bin, plan.VersionArg); perr == nil {
			return resultOfJSON("RuntimeInstall", RuntimeInstallResult{
				Runtime:    rt,
				Installed:  true,
				Version:    ver,
				BinaryPath: p,
				Platform:   platform,
				Skipped:    true,
				Reason:     "already installed",
			})
		}
	}

	if _, e := runtimeInstallExec(ctx, plan.Cmd); e != nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"RuntimeInstall: %s install failed: %v (cmd: %s)",
			rt, e, strings.Join(plan.Cmd, " "))), nil
	}

	// Re-probe; an install that exits 0 but doesn't land the
	// binary on PATH is still a failure from the operator's POV
	// (usually means the package manager's bin dir isn't on PATH).
	p, ver, perr := runtimeInstallProbe(plan.Bin, plan.VersionArg)
	if perr != nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"RuntimeInstall: %s install completed but %q is not on PATH (%v) — check that the package manager's global bin dir is on $PATH",
			rt, plan.Bin, perr)), nil
	}
	return resultOfJSON("RuntimeInstall", RuntimeInstallResult{
		Runtime:    rt,
		Installed:  true,
		Version:    ver,
		BinaryPath: p,
		Platform:   platform,
	})
}
