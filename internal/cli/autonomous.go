// Package cli — `clawtool autonomous` subcommand. A single-message-driven
// self-paced dev loop: the operator types ONE prompt and the binary keeps
// dispatching it back to the chosen BIAM peer until the agent emits
// `DONE: <summary>` (or writes a tick.json with `done: true`), the
// max-iterations cap is hit, or the operator hits Ctrl-C.
//
// Tests stub the dispatcher via the AutonomousDispatcher interface
// (set --cooldown=0s to skip the 5-min sleep).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
)

const autonomousUsage = `Usage:
  clawtool autonomous "<goal>" [--agent <instance>] [--max-iterations <N>]
                               [--cooldown <duration>] [--workdir <path>]
                               [--dry-run]

  Run a self-paced single-message dev loop. The CLI builds a session
  prompt from <goal> + iteration metadata and dispatches it to the
  chosen BIAM peer (default: claude). After each iteration the agent
  is expected to write <workdir>/.clawtool/autonomous/tick-N.json with
  {summary, files_changed, next_steps, done}; the loop ends when
  done == true, --max-iterations is hit, or the operator sends SIGINT.

Flags:
  --agent <instance>       Peer instance to dispatch to. Default: claude.
  --max-iterations <N>     Hard cap on iterations. Default: 10.
  --cooldown <duration>    Sleep between iterations (e.g. 5m, 30s, 0s).
                           Default: 5m. Tests pass 0s.
  --workdir <path>         Working directory; tick-N.json + final.json
                           land under <workdir>/.clawtool/autonomous/.
                           Default: cwd.
  --dry-run                Print the planned prompt + flags and exit
                           without dispatching.

Hint: pair with OnboardStatus + InitApply for "one message, full
pipeline" — onboard the repo, install defaults, then hand the
autonomous loop a goal and walk away.
`

// Tick is the structured return from each iteration; the peer is
// contracted to write it to <workdir>/.clawtool/autonomous/tick-<N>.json.
type Tick struct {
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed,omitempty"`
	NextSteps    string   `json:"next_steps,omitempty"`
	Done         bool     `json:"done"`
}

// AutonomousDispatcher is the test seam for autonomous mode.
type AutonomousDispatcher interface {
	Dispatch(ctx context.Context, agent, prompt, workdir string, iter int) (Tick, error)
}

// defaultDispatcher is the package-level seam; tests swap it via
// SetAutonomousDispatcher.
var defaultDispatcher AutonomousDispatcher = realDispatcher{}

// SetAutonomousDispatcher installs a stub and returns the prior one.
func SetAutonomousDispatcher(d AutonomousDispatcher) AutonomousDispatcher {
	prev := defaultDispatcher
	defaultDispatcher = d
	return prev
}

// realDispatcher routes through agents.Supervisor.Send + reads back tick-N.json.
type realDispatcher struct{}

func (realDispatcher) Dispatch(ctx context.Context, agent, prompt, workdir string, iter int) (Tick, error) {
	sup := agents.NewSupervisor()
	rc, err := sup.Send(ctx, agent, prompt, map[string]any{"cwd": workdir})
	if err != nil {
		return Tick{}, err
	}
	// Drain stdout so the upstream process is reaped before we read
	// its tick file. The agent communicates via tick.json, not stdout.
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	return readTick(workdir, iter)
}

type autonomousArgs struct {
	goal          string
	agent         string
	maxIterations int
	cooldown      time.Duration
	workdir       string
	dryRun        bool
}

func parseAutonomousArgs(argv []string) (autonomousArgs, error) {
	out := autonomousArgs{agent: "claude", maxIterations: 10, cooldown: 5 * time.Minute}
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
		case "--max-iterations":
			if i+1 >= len(argv) {
				return out, errors.New("--max-iterations requires a value")
			}
			n, err := strconv.Atoi(argv[i+1])
			if err != nil || n <= 0 {
				return out, fmt.Errorf("--max-iterations: %q is not a positive int", argv[i+1])
			}
			out.maxIterations = n
			i++
		case "--cooldown":
			if i+1 >= len(argv) {
				return out, errors.New("--cooldown requires a value")
			}
			d, err := time.ParseDuration(argv[i+1])
			if err != nil {
				return out, fmt.Errorf("--cooldown: %w", err)
			}
			out.cooldown = d
			i++
		case "--dry-run":
			out.dryRun = true
		case "--help", "-h":
			return out, errors.New("help requested")
		default:
			if strings.HasPrefix(v, "-") {
				return out, fmt.Errorf("unknown flag %q", v)
			}
			if out.goal == "" {
				out.goal = v
			} else {
				out.goal += " " + v
			}
		}
	}
	return out, nil
}

func (a *App) runAutonomous(argv []string) int {
	args, err := parseAutonomousArgs(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autonomous: %v\n\n%s", err, autonomousUsage)
		return 2
	}
	if args.goal == "" {
		fmt.Fprint(a.Stderr, "clawtool autonomous: missing <goal>\n\n"+autonomousUsage)
		return 2
	}
	if args.workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool autonomous: getwd: %v\n", err)
			return 1
		}
		args.workdir = wd
	}
	// Onboard guardrail: the loop assumes .clawtool/ exists (rules
	// gate, tick directory). A fresh repo would silently no-op every
	// iteration — refuse cleanly instead.
	if _, err := os.Stat(filepath.Join(args.workdir, ".clawtool")); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(a.Stderr, "clawtool autonomous: %q is not onboarded (no .clawtool/ directory)\n", args.workdir)
		fmt.Fprintln(a.Stderr, "  run `clawtool onboard` (or call OnboardStatus + InitApply via MCP) first.")
		return 1
	}
	if args.dryRun {
		return a.printAutonomousPlan(args)
	}
	return a.runAutonomousLoop(args)
}

func (a *App) printAutonomousPlan(args autonomousArgs) int {
	fmt.Fprintln(a.Stdout, "clawtool autonomous — dry-run plan")
	fmt.Fprintf(a.Stdout, "  goal:           %s\n", args.goal)
	fmt.Fprintf(a.Stdout, "  agent:          %s\n", args.agent)
	fmt.Fprintf(a.Stdout, "  max-iterations: %d\n", args.maxIterations)
	fmt.Fprintf(a.Stdout, "  cooldown:       %s\n", args.cooldown)
	fmt.Fprintf(a.Stdout, "  workdir:        %s\n", args.workdir)
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "session-prompt template (iteration 1 of N):")
	fmt.Fprintln(a.Stdout, buildSessionPrompt(args.goal, 1, args.maxIterations, args.workdir))
	return 0
}

// buildSessionPrompt is the verbatim template handed to the peer per
// iteration. Kept package-level so tests can golden-compare it.
func buildSessionPrompt(goal string, iter, maxIter int, workdir string) string {
	tickPath := filepath.Join(workdir, ".clawtool", "autonomous", fmt.Sprintf("tick-%d.json", iter))
	return fmt.Sprintf(`You are operating in clawtool autonomous mode.

Goal: %s

This is iteration %d of %d. Make incremental progress toward the
goal. When you have finished EVERYTHING, emit a single line of the
form "DONE: <one-line summary>" as your final message AND write
%s with {"summary": "...", "files_changed": [...], "next_steps": "", "done": true}.

If you are NOT finished, write %s with {"summary": "what you did this turn", "files_changed": [...], "next_steps": "what to tackle next", "done": false}.

Do not block on operator input. Do not ask clarifying questions —
make the most reasonable interpretation of the goal and proceed.
The loop will dispatch you again with the same goal + a fresh
iteration counter.`, goal, iter, maxIter, tickPath, tickPath)
}

func (a *App) runAutonomousLoop(args autonomousArgs) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Fprintln(a.Stderr, "clawtool autonomous: interrupt received, stopping after current iteration")
		cancel()
	}()

	tickDir := filepath.Join(args.workdir, ".clawtool", "autonomous")
	if err := os.MkdirAll(tickDir, 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autonomous: mkdir tick-dir: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.Stdout, "clawtool autonomous: dispatching to %q for up to %d iterations (cooldown %s)\n",
		args.agent, args.maxIterations, args.cooldown)

	var (
		ticks    []Tick
		stopped  string
		finished bool
	)
	for i := 1; i <= args.maxIterations; i++ {
		if ctx.Err() != nil {
			stopped = "interrupted"
			break
		}
		prompt := buildSessionPrompt(args.goal, i, args.maxIterations, args.workdir)
		fmt.Fprintf(a.Stdout, "  iteration %d/%d…\n", i, args.maxIterations)
		tick, err := defaultDispatcher.Dispatch(ctx, args.agent, prompt, args.workdir, i)
		if err != nil {
			fmt.Fprintf(a.Stderr, "  iteration %d errored: %v\n", i, err)
			stopped = "error: " + err.Error()
			break
		}
		ticks = append(ticks, tick)
		fmt.Fprintf(a.Stdout, "    summary: %s\n", tick.Summary)
		if tick.Done {
			finished = true
			stopped = "done"
			break
		}
		if i < args.maxIterations && args.cooldown > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(args.cooldown):
			}
			if ctx.Err() != nil {
				stopped = "interrupted"
				break
			}
		}
	}
	if stopped == "" {
		stopped = "max-iterations"
	}
	if err := writeFinal(tickDir, args, ticks, stopped, finished); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autonomous: write final.json: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "clawtool autonomous: stopped (%s) after %d iteration(s); final.json at %s\n",
		stopped, len(ticks), filepath.Join(tickDir, "final.json"))
	if !finished && stopped != "done" {
		// Non-zero only when the loop did NOT reach DONE. Useful
		// for CI scripts wrapping autonomous mode.
		return 1
	}
	return 0
}

// readTick loads tick-<N>.json. Missing file is treated as an
// in-progress iteration with no signal — we synthesise a {done: false}
// tick rather than erroring, so a peer that legitimately returns
// mid-task without a tick still keeps the loop running until max.
func readTick(workdir string, iter int) (Tick, error) {
	path := filepath.Join(workdir, ".clawtool", "autonomous", fmt.Sprintf("tick-%d.json", iter))
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Tick{Summary: "(no tick file written)", Done: false}, nil
	}
	if err != nil {
		return Tick{}, fmt.Errorf("read tick: %w", err)
	}
	var t Tick
	if err := json.Unmarshal(b, &t); err != nil {
		return Tick{}, fmt.Errorf("parse tick %s: %w", path, err)
	}
	return t, nil
}

// writeFinal records the loop's terminal state. Partial-friendly:
// SIGINT lands here with whatever ticks were collected.
func writeFinal(tickDir string, args autonomousArgs, ticks []Tick, stoppedReason string, finished bool) error {
	final := struct {
		Goal          string    `json:"goal"`
		Agent         string    `json:"agent"`
		MaxIterations int       `json:"max_iterations"`
		Iterations    int       `json:"iterations"`
		StoppedReason string    `json:"stopped_reason"`
		Finished      bool      `json:"finished"`
		Ticks         []Tick    `json:"ticks"`
		FinishedAt    time.Time `json:"finished_at"`
	}{
		Goal: args.goal, Agent: args.agent, MaxIterations: args.maxIterations,
		Iterations: len(ticks), StoppedReason: stoppedReason,
		Finished: finished, Ticks: ticks, FinishedAt: time.Now().UTC(),
	}
	b, err := json.MarshalIndent(final, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tickDir, "final.json"), b, 0o644)
}
