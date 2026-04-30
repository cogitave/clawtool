// Package cli — `clawtool bootstrap` subcommand. The zero-click
// onboarding verb. After a fresh install the operator should do
// nothing: clawtool spawns the chosen BIAM peer's CLI with the
// elevation flag, pipes the bootstrap prompt to it, and streams
// the agent's reply back to stdout. The agent is expected to run
// OnboardWizard + InitApply via MCP and print a 3-line summary.
//
// Test seam: production wires lookPath / spawnAgent to os/exec;
// tests stub them so the suite never spawns a real CLI.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/cogitave/clawtool/internal/agents"
)

const bootstrapUsage = `Usage:
  clawtool bootstrap [--agent <family>] [--workdir <path>] [--dry-run]

  Spawn the chosen BIAM peer's CLI with its elevation flag and pipe
  the bootstrap prompt to it as the FIRST user message. The agent
  runs OnboardWizard + InitApply autonomously, then prints a short
  summary and exits. Output streams to stdout verbatim.

Flags:
  --agent <family>   Peer to spawn. One of: claude (default), codex,
                     gemini, opencode, hermes, aider.
  --workdir <path>   cwd handed to the spawned agent. Default: current
                     directory.
  --dry-run          Print the planned spawn argv + bootstrap prompt
                     and exit without invoking the agent.

The bootstrap prompt asks the agent to run OnboardWizard and
InitApply via MCP, read AutonomousRun's UsageHint, and print a
3-line summary. Tail the agent's stdout for "DONE" — the verb
exits cleanly when the agent emits it or returns control.
`

// bootstrapPrompt is the verbatim markdown blob piped to the
// spawned agent as its first prompt. Kept package-level so tests
// can golden-compare it. The prompt deliberately leans on the
// MCP tool names the operator opted in to via `clawtool onboard`
// (clawtool's MCP server registers OnboardWizard / InitApply /
// AutonomousRun); listing them by name lets the agent skip the
// usual "let me poke around to see what's available" warmup.
const bootstrapPrompt = `# clawtool bootstrap

You're being onboarded by clawtool. Run the following steps autonomously,
without asking for confirmation:

1. Run the OnboardWizard MCP tool with agent_family=claude, telemetry_opt_in=true, non_interactive=true.
2. Run the InitApply MCP tool with core_only=true.
3. Read the AutonomousRun tool's UsageHint so you understand the loop contract.
4. Print a 3-line summary of what was set up.

Begin.
`

// bootstrapArgs is the parsed flag bundle.
type bootstrapArgs struct {
	agent   string
	workdir string
	dryRun  bool
}

// lookPath is the package-level test seam mirroring exec.LookPath.
// Tests rebind it via t.Cleanup; production stays on the stdlib path.
var lookPath = exec.LookPath

// spawnAgent is the package-level test seam for the actual subprocess
// spawn. Production wires it to a real exec.CommandContext + stdin
// pipe + stdout passthrough; tests rebind it to capture argv +
// prompt without ever forking a real binary.
//
// Returns the agent's exit code; non-zero is surfaced to the operator.
var spawnAgent = func(ctx context.Context, bin string, argv []string, prompt string, stdout, stderr io.Writer) int {
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "clawtool bootstrap: spawn %s: %v\n", bin, err)
		return 1
	}
	return 0
}

// installHints maps a family to the human-readable install pointer
// the verb prints when LookPath misses. Kept short — the operator
// just needs one canonical command to run.
var installHints = map[string]string{
	"claude":   `visit anthropic.com/claude-code or run "npm i -g @anthropic-ai/claude-code"`,
	"codex":    `run "npm i -g @openai/codex" (see github.com/openai/codex)`,
	"gemini":   `run "npm i -g @google/gemini-cli"`,
	"opencode": `run "curl -fsSL https://opencode.ai/install | bash"`,
	"hermes":   `see github.com/hermes-cli/hermes for install instructions`,
	"aider":    `run "pip install aider-install && aider-install"`,
}

func parseBootstrapArgs(argv []string) (bootstrapArgs, error) {
	out := bootstrapArgs{agent: "claude"}
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--agent", "--workdir":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("%s requires a value", v)
			}
			if v == "--agent" {
				out.agent = argv[i+1]
			} else {
				out.workdir = argv[i+1]
			}
			i++
		case "--dry-run":
			out.dryRun = true
		case "--help", "-h":
			return out, fmt.Errorf("help requested")
		default:
			return out, fmt.Errorf("unknown flag %q", v)
		}
	}
	return out, nil
}

// runBootstrap is the verb dispatcher.
func (a *App) runBootstrap(argv []string) int {
	args, err := parseBootstrapArgs(argv)
	if err != nil {
		// Help request prints usage on stdout (operator-friendly);
		// any other parse error is a usage error on stderr.
		if err.Error() == "help requested" {
			fmt.Fprint(a.Stdout, bootstrapUsage)
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool bootstrap: %v\n\n%s", err, bootstrapUsage)
		return 2
	}
	if _, ok := installHints[args.agent]; !ok {
		fmt.Fprintf(a.Stderr, "clawtool bootstrap: unknown agent %q (one of: claude, codex, gemini, opencode, hermes, aider)\n", args.agent)
		return 2
	}
	if args.workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool bootstrap: getwd: %v\n", err)
			return 1
		}
		args.workdir = wd
	}
	bin, argvOut := buildBootstrapArgv(args.agent)
	if args.dryRun {
		fmt.Fprintln(a.Stdout, "clawtool bootstrap — dry-run plan")
		fmt.Fprintf(a.Stdout, "  agent:   %s\n", args.agent)
		fmt.Fprintf(a.Stdout, "  workdir: %s\n", args.workdir)
		fmt.Fprintf(a.Stdout, "  spawn:   %s %s\n", bin, strings.Join(argvOut, " "))
		fmt.Fprintln(a.Stdout)
		fmt.Fprintln(a.Stdout, "bootstrap prompt:")
		fmt.Fprint(a.Stdout, bootstrapPrompt)
		return 0
	}
	if _, err := lookPath(bin); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool bootstrap: %s not found on PATH. Install: %s.\n",
			bin, installHints[args.agent])
		return 2
	}
	fmt.Fprintf(a.Stdout, "clawtool bootstrap: spawning %s with elevation, piping init prompt…\n", bin)
	return spawnAgent(context.Background(), bin, argvOut, bootstrapPrompt, a.Stdout, a.Stderr)
}

// buildBootstrapArgv composes the binary name + argv slice for the
// chosen agent. Argv shape mirrors each CLI's published headless
// mode plus the elevation flag from agents.ElevationFlag — the
// single canonical map shared with the transport layer. Stdin is
// the prompt channel for every supported family.
func buildBootstrapArgv(family string) (string, []string) {
	flag := agents.ElevationFlag(family)
	switch family {
	case "claude":
		// `--print` (alias `-p`) reads the prompt from stdin when
		// no positional prompt is given. `--output-format text`
		// keeps stdout human-readable so the operator's terminal
		// shows progress without needing a stream-json parser.
		return "claude", []string{flag, "--print", "--output-format", "text"}
	case "codex":
		// `codex exec` is the headless print mode; reads stdin
		// when no positional prompt. --json keeps stdout machine-
		// parseable for downstream piping but a TTY operator will
		// still see line-delimited progress.
		return "codex", []string{"exec", "--skip-git-repo-check", "--json", flag}
	case "gemini":
		// `-p -` reads the prompt from stdin (Gemini convention).
		// --skip-trust mirrors the relay path so the CLI doesn't
		// refuse to run in an untrusted folder.
		return "gemini", []string{"-p", "-", "--skip-trust", "--output-format", "text", flag}
	case "opencode":
		// `run` reads stdin when no positional argument.
		return "opencode", []string{"run", flag}
	case "hermes":
		return "hermes", []string{"chat", flag}
	case "aider":
		// Aider reads --message from argv, not stdin — but the
		// bootstrap path pipes via stdin uniformly, so we use
		// `--message-file -` which Aider treats as stdin.
		return "aider", []string{"--message-file", "-", "--no-stream", "--no-pretty", flag}
	}
	return family, []string{flag}
}
