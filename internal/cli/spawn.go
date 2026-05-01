// Package cli — `clawtool spawn` subcommand. Opens a NEW terminal
// window/pane running the requested agent CLI (claude-code / codex /
// gemini / opencode), then immediately registers the freshly-spawned
// agent as a peer in the daemon's BIAM registry so the caller can
// dispatch to it via SendMessage / PeerSend by peer_id.
//
// Why a verb (and not just `bootstrap`): bootstrap spawns the agent
// in the SAME terminal and pipes a single prompt; spawn opens a
// fresh terminal pane the operator (or another agent) keeps
// interacting with. Pairs with the orchestrator pattern where one
// running Claude Code session asks clawtool to fan out new sessions
// of codex / gemini etc, then talks to them via the peer surface.
//
// Terminal detection cascade is deliberately conservative — picks
// the FIRST viable launcher rather than asking. Operators with
// strong preferences pass --terminal explicitly. The cascade order
// reflects "what's most likely already in use":
//
//  1. tmux — if $TMUX is set we're in a tmux session; new-window
//     is the cheapest, most predictable option
//  2. screen — if $STY is set, same logic for GNU screen
//  3. wt.exe — Windows Terminal under WSL (very common dev surface)
//  4. gnome-terminal — Linux GNOME default
//  5. konsole — Linux KDE default
//  6. kitty — popular cross-platform GPU terminal
//  7. macOS Terminal.app via `open -a Terminal -n`
//  8. headless `nohup …&` fallback — last resort, no visible window
//
// Tests stub `defaultLauncher` and `registerPeerHTTP` at package
// level so the suite never spawns a real terminal or daemon.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/daemon"
)

const spawnUsage = `Usage:
  clawtool spawn <backend> [flags]

  Open a NEW terminal window/pane running the requested agent CLI,
  register it as a peer in the local clawtool daemon's BIAM registry,
  and print the assigned peer_id so callers can dispatch to it via
  SendMessage / PeerSend / clawtool peer send.

Backends:
  claude-code, codex, gemini, opencode

Flags:
  --display-name <text>  Human-friendly label for the peer registry
                         (defaults to "<user>@<host>/<backend>:spawn").
  --cwd <path>           Working directory the spawned agent runs in
                         (defaults to the current directory).
  --prompt "<text>"      First user message handed to the agent on
                         spawn. Empty = open the agent at its prompt.
  --terminal <name>      Force a launcher: tmux | screen | wt |
                         gnome-terminal | konsole | kitty | macos |
                         headless. Default: auto-detect.
  --dry-run              Print the spawn plan (terminal, command,
                         peer-registration intent) without executing.

Returns JSON-shaped lines on stdout: peer_id, terminal, pid (best-
effort), and the agent invocation command that ran. Exit code 0
when both spawn + peer-registration succeed; 1 if either fails.
`

// spawnArgs is the parsed flag bundle for runSpawn.
type spawnArgs struct {
	backend     string
	displayName string
	cwd         string
	prompt      string
	terminal    string
	dryRun      bool
}

// SpawnResult is the structured outcome of a spawn — also surfaced
// verbatim by the matching MCP tool so a calling agent gets one
// shape regardless of entry point.
type SpawnResult struct {
	PeerID   string   `json:"peer_id"`
	Backend  string   `json:"backend"`
	Family   string   `json:"family"`
	Terminal string   `json:"terminal"`
	PID      int      `json:"pid,omitempty"`
	Bin      string   `json:"bin"`
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	DryRun   bool     `json:"dry_run,omitempty"`
}

// supportedSpawnBackends is the canonical set of backend labels
// `clawtool spawn` accepts. Mapped to upstream agent families via
// backendFamily — codex/gemini/opencode are 1:1, claude-code → claude.
var supportedSpawnBackends = []string{"claude-code", "codex", "gemini", "opencode"}

// backendFamily resolves a peer-registry backend label (the value
// stored in the a2a.Peer registry) to the upstream agent family
// name (the key into agents.ElevationFlag + buildBootstrapArgv).
func backendFamily(backend string) string {
	if backend == "claude-code" {
		return "claude"
	}
	return backend
}

// launcher is the indirection seam tests substitute. Production
// wires it to realLauncher{} which shells out to the chosen
// terminal multiplexer / window manager. Tests rebind it to record
// what WOULD have launched.
type launcher interface {
	// Launch runs the prepared command and returns the
	// terminal name actually used (e.g. "tmux", "wt") plus a
	// best-effort PID. terminal=="dry-run" is reserved for
	// the --dry-run path which never reaches the launcher.
	Launch(ctx context.Context, plan launchPlan) (terminal string, pid int, err error)
}

type launchPlan struct {
	Terminal string   // requested launcher (auto-detected when empty)
	Bin      string   // agent binary, e.g. "claude"
	Argv     []string // agent argv (binary excluded)
	Cwd      string   // working directory the agent runs in
	Prompt   string   // optional first message; producers handle stdin/argv themselves
}

// defaultLauncher is the package-level seam. Production binds it
// to realLauncher{}; tests swap in a recorder.
var defaultLauncher launcher = realLauncher{}

// registerPeerHTTP is the seam tests substitute for the
// daemon round-trip. Production dials the local daemon via
// daemon.HTTPRequest; tests stub it to return a fixture peer.
var registerPeerHTTP = func(method, path string, body *bytes.Reader, out any) error {
	return daemon.HTTPRequest(method, path, body, out)
}

// runSpawn dispatches `clawtool spawn …`.
func (a *App) runSpawn(argv []string) int {
	args, err := parseSpawnArgs(argv)
	if err != nil {
		if err.Error() == "help requested" {
			fmt.Fprint(a.Stdout, spawnUsage)
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool spawn: %v\n\n%s", err, spawnUsage)
		return 2
	}
	if !isSupportedSpawnBackend(args.backend) {
		fmt.Fprintf(a.Stderr, "clawtool spawn: unknown backend %q (one of: %s)\n",
			args.backend, strings.Join(supportedSpawnBackends, ", "))
		return 2
	}
	family := backendFamily(args.backend)
	if args.cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			args.cwd = wd
		}
	}
	if args.displayName == "" {
		args.displayName = defaultDisplayName(args.backend) + ":spawn"
	}

	bin, agentArgv := buildBootstrapArgv(family)
	plan := launchPlan{
		Terminal: args.terminal,
		Bin:      bin,
		Argv:     agentArgv,
		Cwd:      args.cwd,
		Prompt:   args.prompt,
	}

	// Dry-run: print the plan + the peer-registration we WOULD
	// send. No launcher call, no daemon round-trip. Operators use
	// this to verify cascade pick-up without forking a window.
	if args.dryRun {
		chosen := plan.Terminal
		if chosen == "" {
			chosen = autodetectTerminal()
		}
		res := SpawnResult{
			Backend:  args.backend,
			Family:   family,
			Terminal: chosen,
			Bin:      bin,
			Argv:     agentArgv,
			Cwd:      args.cwd,
			DryRun:   true,
		}
		fmt.Fprintln(a.Stdout, "clawtool spawn — dry-run plan")
		fmt.Fprintf(a.Stdout, "  backend:      %s (family=%s)\n", args.backend, family)
		fmt.Fprintf(a.Stdout, "  terminal:     %s\n", chosen)
		fmt.Fprintf(a.Stdout, "  display-name: %s\n", args.displayName)
		fmt.Fprintf(a.Stdout, "  cwd:          %s\n", args.cwd)
		fmt.Fprintf(a.Stdout, "  spawn:        %s %s\n", bin, strings.Join(agentArgv, " "))
		if args.prompt != "" {
			fmt.Fprintf(a.Stdout, "  first-prompt: %s\n", oneLine(args.prompt))
		}
		fmt.Fprintln(a.Stdout, "  peer-register: POST /v1/peers/register {backend="+args.backend+", role=agent, metadata.spawned_by=clawtool}")
		body, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}

	// Real spawn. Launcher returns the terminal name actually
	// used (auto-detect collapses inside realLauncher) plus a
	// best-effort PID. Some launchers (wt.exe, open -a) detach
	// fast enough that we can't observe a stable child PID; in
	// that case PID is 0 and the operator sees the registry's
	// row instead.
	chosen, pid, lerr := defaultLauncher.Launch(context.Background(), plan)
	if lerr != nil {
		fmt.Fprintf(a.Stderr, "clawtool spawn: launch %s: %v\n", plan.Bin, lerr)
		return 1
	}

	// Register the peer immediately. Per the operator's
	// constraint the peer MUST land in the registry within 2s
	// of spawn — we don't wait for the agent to finish booting,
	// we just hand the daemon the metadata so the next
	// SendMessage call can resolve it. The agent's own startup
	// hook may also re-register; the registry's identity tuple
	// (backend + path + session + tmux_pane) collapses dupes.
	in := a2a.RegisterInput{
		DisplayName: args.displayName,
		Path:        args.cwd,
		Backend:     args.backend,
		Role:        a2a.RoleAgent,
		PID:         pid,
		Metadata: map[string]string{
			"spawned_by":     "clawtool",
			"spawn_terminal": chosen,
			"spawn_bin":      plan.Bin,
		},
	}
	body, _ := json.Marshal(in)
	var peer a2a.Peer
	if err := registerPeerHTTP(http.MethodPost, "/v1/peers/register", bytes.NewReader(body), &peer); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool spawn: register peer: %v\n", err)
		// Best-effort: surface the launch result anyway so the
		// operator can register manually if the daemon was down.
		fmt.Fprintf(a.Stdout, "warning: peer registration failed; agent is running in %s without a peer_id\n", chosen)
		return 1
	}

	res := SpawnResult{
		PeerID:   peer.PeerID,
		Backend:  args.backend,
		Family:   family,
		Terminal: chosen,
		PID:      pid,
		Bin:      plan.Bin,
		Argv:     plan.Argv,
		Cwd:      plan.Cwd,
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(a.Stdout, string(out))
	return 0
}

// parseSpawnArgs decodes the verb's argv (already shifted past
// "spawn"). The first non-flag positional is the backend; flags
// may appear before or after it because we lift the leading
// non-flag token out of argv before handing the rest to flag.Parse
// (stdlib flag stops at the first positional, so a bare
// `spawn codex --dry-run` would otherwise misparse the flag).
func parseSpawnArgs(argv []string) (spawnArgs, error) {
	out := spawnArgs{}
	// Lift the FIRST non-flag positional as the backend; pass
	// the rest to flag.Parse. Allows both
	//   spawn codex --dry-run
	//   spawn --dry-run codex
	// without users having to remember the order.
	var backend string
	rest := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		if backend == "" && v != "" && !strings.HasPrefix(v, "-") {
			backend = v
			continue
		}
		rest = append(rest, v)
	}

	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{}) // swallow flag-package usage; we own --help
	displayName := fs.String("display-name", "", "")
	cwd := fs.String("cwd", "", "")
	prompt := fs.String("prompt", "", "")
	terminal := fs.String("terminal", "", "")
	dryRun := fs.Bool("dry-run", false, "")
	help := fs.Bool("help", false, "")
	helpShort := fs.Bool("h", false, "")
	if err := fs.Parse(rest); err != nil {
		return out, err
	}
	if *help || *helpShort {
		return out, fmt.Errorf("help requested")
	}
	if extra := fs.Args(); len(extra) > 0 {
		return out, fmt.Errorf("unexpected extra positional args: %v", extra)
	}
	if backend == "" {
		return out, fmt.Errorf("backend is required")
	}
	out.backend = backend
	out.displayName = *displayName
	out.cwd = *cwd
	out.prompt = *prompt
	out.terminal = *terminal
	out.dryRun = *dryRun
	return out, nil
}

// isSupportedSpawnBackend reports whether `b` is in
// supportedSpawnBackends.
func isSupportedSpawnBackend(b string) bool {
	for _, s := range supportedSpawnBackends {
		if s == b {
			return true
		}
	}
	return false
}

// autodetectTerminal returns the launcher name we'd pick for the
// current host. Pure function (reads env + runtime.GOOS only) so
// tests can rebind os.Getenv via t.Setenv and exercise every
// branch deterministically.
//
// The cascade prefers in-session multiplexers (tmux / screen)
// because they're cheapest + don't open a new window — the
// operator stays in the same view and gets a new pane. Falls
// back to GUI terminals only when no multiplexer is active.
func autodetectTerminal() string {
	// 1. tmux — `$TMUX` is set inside any tmux session.
	if os.Getenv("TMUX") != "" {
		return "tmux"
	}
	// 2. screen — `$STY` is set inside any GNU screen session.
	if os.Getenv("STY") != "" {
		return "screen"
	}
	// 3. WSL → Windows Terminal. WSL_DISTRO_NAME is set by
	// every WSL2 shell; wt.exe is the recommended terminal.
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		if _, err := exec.LookPath("wt.exe"); err == nil {
			return "wt"
		}
	}
	// 4-6. Linux GUI terminals.
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
	// 7. macOS Terminal.app.
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	// 8. Last resort: nohup the agent headlessly. No visible
	// window, but the peer still registers and the operator
	// can attach via the daemon's peer surface.
	return "headless"
}

// realLauncher is the production launcher. Each branch composes
// the platform-specific argv that ends up running `bin agentArgv…`
// inside the chosen terminal.
type realLauncher struct{}

// Launch picks (or honors) a terminal launcher and runs the
// command. Returns the terminal name + best-effort PID.
func (realLauncher) Launch(ctx context.Context, p launchPlan) (string, int, error) {
	chosen := p.Terminal
	if chosen == "" {
		chosen = autodetectTerminal()
	}
	full := append([]string{p.Bin}, p.Argv...)
	cmdline := strings.Join(full, " ")
	switch chosen {
	case "tmux":
		// `new-window -P -F '#{pane_id}'` prints the new pane
		// id on stdout — useful for downstream peer metadata
		// but not required for spawn correctness.
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
		// `screen -X screen <cmd>` adds a new window inside
		// the running session.
		c := exec.CommandContext(ctx, "screen", "-X", "screen", p.Bin)
		c.Args = append(c.Args, p.Argv...)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "wt":
		// Windows Terminal: `wt.exe new-tab -d <cwd> <cmd>`.
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
		// `kitty @ launch --type=tab` requires kitty's remote
		// control to be on; fall back to a fresh window when
		// the IPC isn't available.
		c := exec.CommandContext(ctx, "kitty", "--single-instance", "--", "bash", "-lc", cmdline)
		c.Dir = p.Cwd
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "macos":
		// `open -a Terminal -n` opens a fresh Terminal.app
		// window; the working directory + command are passed
		// via a generated shell wrapper since `open` doesn't
		// take argv directly.
		script := fmt.Sprintf("cd %q && %s", p.Cwd, cmdline)
		c := exec.CommandContext(ctx, "osascript", "-e",
			fmt.Sprintf(`tell application "Terminal" to do script %q`, script))
		if err := c.Start(); err != nil {
			return chosen, 0, err
		}
		return chosen, 0, nil
	case "headless":
		// nohup … & — no terminal, no controlling tty, output
		// piped to /dev/null. The peer still registers so the
		// operator / orchestrator can talk to it via the daemon.
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

// oneLine collapses a multi-line prompt into a single-line preview
// for the dry-run plan. Keeps the operator's terminal readable
// even when --prompt was a 200-line block.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " / ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
