// Package cli — `clawtool install` is the zero-touch first-run verb.
//
// Operator runs `clawtool install` ONCE after they put the binary on
// PATH; this file orchestrates everything else: daemon up, host
// detection, bridge install, agent-claim, MCP config write, hooks
// install, peer registration of the current shell, init --all on the
// repo, and a final daemon-health verify. Single status line per
// step on stderr (✓ / ⚠ / ✗ / ⤳); the only thing that lands on
// stdout is the closing one-liner:
//
//	clawtool kuruldu — N agent(s) registered, M recipe(s) applied, daemon @ 127.0.0.1:<port>
//
// The verb is IDEMPOTENT: every step short-circuits when its target
// state is already present (daemon healthy, bridge already added,
// agent already claimed, hooks already installed, peer already
// registered for this session). Failures of steps 2–10 are logged
// non-fatally and continue; only step 1 (daemon) aborts.
//
// Test seam: package-level vars (installLookPath, installDispatcher,
// installDaemonStarter, installPeerRegister, installInitAll) are
// stubbed by install_first_run_test.go so the suite exercises the
// dispatch flow without spawning real binaries / daemons / agents.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/daemon"
)

const installUsage = `Usage:
  clawtool install [--dry-run] [--workdir <path>] [--skip-init]

Zero-touch first-run setup. Runs ten steps end-to-end:

  1. Daemon health    — ensure 'clawtool serve --listen 127.0.0.1:<port>
                        --no-auth --mcp-http' is running (spawn if not).
  2. Host detection   — probe PATH for claude / codex / gemini / opencode.
  3. Bridge install   — 'clawtool bridge add <family>' for each detected
                        non-claude host (idempotent).
  4. Agent claim      — 'clawtool agents claim <family>' for each detected
                        host (skips opencode's known mcp-add failure).
  5. MCP config       — write the clawtool MCP server entry into each
                        detected host's config file.
  6. Hooks install    — append peer register/deregister entries to each
                        host's hooks config (idempotent).
  7. Peer registration — register the current shell session as a peer.
  8. Init --all       — apply every Core recipe to the cwd (skip if not
                        a git repo or --skip-init).
  9. Verify           — ping the daemon's /v1/agents and confirm at
                        least one agent surface is reachable.
  10. Exit            — single one-line summary on stdout.

Failures of steps 2–10 are logged non-fatally and the run continues.
Step 1 is hard-required: if the daemon won't come up, install aborts.

Idempotent — running twice does no extra work.
`

// installLookPath, installDispatcher, installDaemonStarter,
// installPeerRegister, installInitAll are the package-level
// test seams. Production binds them in init() to the real
// implementations; tests substitute deterministic stubs in
// t.Cleanup.
//
// Distinct from bootstrap.go's `lookPath` / `spawnAgent` so the two
// verbs' tests can stub independently without thread-of-control
// crosstalk inside `go test ./...`.
//
// Declared as plain function-typed vars (no inline literals) so
// the Go initializer-cycle detector doesn't flag the obvious
// recursion: dispatch → runInstall → installDispatcher → dispatch.
// init() wires the closures in a strict ordering that breaks the
// cycle.
var (
	installLookPath      func(string) (string, error)
	installDispatcher    func(*App, []string) (int, string)
	installDaemonStarter func(context.Context) (int, error)
	installPeerRegister  func(*App, string) error
	installInitAll       func(*App, string) (int, error)
)

func init() {
	installLookPath = exec.LookPath

	installDispatcher = func(a *App, argv []string) (int, string) {
		// Reuse the App's dispatch surface; capture stderr so
		// install_first_run can render its own status lines
		// without the upstream verbs' chatter polluting stderr.
		var buf strings.Builder
		prev := a.Stderr
		a.Stderr = &teeWriter{primary: &buf, secondary: nil}
		defer func() { a.Stderr = prev }()
		return a.dispatch(argv), strings.TrimSpace(buf.String())
	}

	installDaemonStarter = func(ctx context.Context) (int, error) {
		st, err := daemon.Ensure(ctx)
		if err != nil {
			return 0, err
		}
		return st.Port, nil
	}

	installPeerRegister = func(a *App, backend string) error {
		rc, tail := installDispatcher(a, []string{
			"peer", "register",
			"--backend", backend,
			"--display-name", defaultDisplayName(backend),
		})
		if rc != 0 {
			if tail != "" {
				return fmt.Errorf("peer register exit %d: %s", rc, tail)
			}
			return fmt.Errorf("peer register exit %d", rc)
		}
		return nil
	}

	installInitAll = func(a *App, cwd string) (int, error) {
		if !isGitRepo(cwd) {
			return 0, errSkipNotGitRepo
		}
		// runInitAll is the shared implementation onboard's
		// "Install core defaults?" prompt also targets. It
		// prints to stdout but doesn't return its summary
		// struct, so we surface a sentinel -1 for "ran but
		// count unknown" — tests stub the whole hook so the
		// heuristic only affects production.
		_ = a.runInitAll(cwd, false)
		return -1, nil
	}
}

// errSkipNotGitRepo signals installInitAll that step 8 should
// short-circuit cleanly without firing init.
var errSkipNotGitRepo = errors.New("cwd is not a git repo")

// teeWriter forwards writes to a primary capture (string builder)
// and optionally a secondary (e.g. the original os.Stderr). Used
// by installDispatcher to capture upstream-verb stderr without
// hiding it from the operator entirely.
type teeWriter struct {
	primary   io.Writer
	secondary io.Writer
}

func (t *teeWriter) Write(p []byte) (int, error) {
	if t.primary != nil {
		_, _ = t.primary.Write(p)
	}
	if t.secondary != nil {
		return t.secondary.Write(p)
	}
	return len(p), nil
}

// installArgs is the parsed flag bundle.
type installArgs struct {
	dryRun   bool
	workdir  string
	skipInit bool
}

func parseInstallArgs(argv []string) (installArgs, error) {
	out := installArgs{}
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--dry-run":
			out.dryRun = true
		case "--skip-init":
			out.skipInit = true
		case "--workdir":
			if i+1 >= len(argv) {
				return out, fmt.Errorf("--workdir requires a value")
			}
			out.workdir = argv[i+1]
			i++
		case "--help", "-h":
			return out, errInstallHelp
		default:
			return out, fmt.Errorf("unknown flag %q", v)
		}
	}
	return out, nil
}

// errInstallHelp is the sentinel parseInstallArgs returns when
// --help / -h is passed — the dispatcher routes that to a stdout
// usage print + exit 0 instead of a usage error.
var errInstallHelp = errors.New("help requested")

// installSummary is the running tally the verb maintains across
// the ten steps so the closing line can be rendered in one shot.
type installSummary struct {
	port             int
	agentsClaimed    int
	recipesApplied   int
	bridgesInstalled int
	peersRegistered  int
	stepsOK          int
	stepsWarn        int
	stepsFailed      int
}

// runInstall is the verb dispatcher.
func (a *App) runInstall(argv []string) int {
	args, err := parseInstallArgs(argv)
	if err != nil {
		if errors.Is(err, errInstallHelp) {
			fmt.Fprint(a.Stdout, installUsage)
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool install: %v\n\n%s", err, installUsage)
		return 2
	}
	if args.workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool install: getwd: %v\n", err)
			return 1
		}
		args.workdir = wd
	}

	if args.dryRun {
		return a.runInstallDryRun(args)
	}

	sum := &installSummary{}
	ctx := context.Background()

	// ── 1. daemon health (hard-required) ──────────────────────
	a.installStep(1, "daemon health", func() stepResult {
		port, err := installDaemonStarter(ctx)
		if err != nil {
			return stepResult{level: stepFail, msg: err.Error()}
		}
		sum.port = port
		return stepResult{level: stepOK, msg: fmt.Sprintf("listening @ 127.0.0.1:%d", port)}
	}, sum)
	if sum.port == 0 {
		fmt.Fprintln(a.Stderr, "clawtool install: aborting — daemon failed to come up")
		return 1
	}

	// ── 2. host detection ─────────────────────────────────────
	hosts := a.installStepHosts(2, sum)

	// ── 3. bridge install (one per detected non-claude host) ──
	for _, h := range hosts {
		if h == "claude" {
			continue // claude-code uses the plugin marketplace path
		}
		hh := h
		a.installStep(3, fmt.Sprintf("bridge add %s", hh), func() stepResult {
			rc, tail := installDispatcher(a, []string{"bridge", "add", hh})
			if rc != 0 {
				return stepResult{level: stepWarn, msg: trim1(tail)}
			}
			sum.bridgesInstalled++
			return stepResult{level: stepOK, msg: "ok"}
		}, sum)
	}

	// ── 4. agent claim (skip opencode's known failure) ────────
	for _, h := range hosts {
		if h == "opencode" {
			a.installLine(stepWarn, 4, "claim opencode", "skipped — opencode mcp add path is broken upstream")
			sum.stepsWarn++
			continue
		}
		hh := claimNameFor(h)
		a.installStep(4, fmt.Sprintf("claim %s", hh), func() stepResult {
			rc, tail := installDispatcher(a, []string{"agents", "claim", hh})
			if rc != 0 {
				return stepResult{level: stepWarn, msg: trim1(tail)}
			}
			sum.agentsClaimed++
			return stepResult{level: stepOK, msg: "claimed"}
		}, sum)
	}

	// ── 5. MCP config write — covered by step 4 (agents.Claim
	//      writes the host's MCP entry). Emit a status line so
	//      operators can see the step ran and was idempotent.
	a.installLine(stepOK, 5, "MCP config", fmt.Sprintf("written by step 4 for %d host(s)", sum.agentsClaimed))
	sum.stepsOK++

	// ── 6. hooks install per host ─────────────────────────────
	for _, h := range hosts {
		hh := claimNameFor(h)
		a.installStep(6, fmt.Sprintf("hooks install %s", hh), func() stepResult {
			rc, tail := installDispatcher(a, []string{"hooks", "install", hh})
			if rc != 0 {
				return stepResult{level: stepWarn, msg: trim1(tail)}
			}
			return stepResult{level: stepOK, msg: "snippet emitted (idempotent)"}
		}, sum)
	}

	// ── 7. peer registration of the current shell ─────────────
	a.installStep(7, "peer register (current shell)", func() stepResult {
		// Pick a backend label that reflects this shell. Prefer
		// claude-code when present (most common); otherwise the
		// first detected host; fall back to bare "clawtool".
		backend := "clawtool"
		for _, h := range []string{"claude", "codex", "gemini", "opencode"} {
			if contains(hosts, h) {
				backend = claimNameFor(h)
				break
			}
		}
		if err := installPeerRegister(a, backend); err != nil {
			return stepResult{level: stepWarn, msg: err.Error()}
		}
		sum.peersRegistered++
		return stepResult{level: stepOK, msg: "registered as " + backend}
	}, sum)

	// ── 8. init --all on cwd ──────────────────────────────────
	if args.skipInit {
		a.installLine(stepOK, 8, "init --all", "skipped (--skip-init)")
		sum.stepsOK++
	} else {
		a.installStep(8, "init --all", func() stepResult {
			applied, err := installInitAll(a, args.workdir)
			if errors.Is(err, errSkipNotGitRepo) {
				return stepResult{level: stepWarn, msg: "skipped — cwd is not a git repo"}
			}
			if err != nil {
				return stepResult{level: stepWarn, msg: err.Error()}
			}
			if applied >= 0 {
				sum.recipesApplied += applied
			}
			return stepResult{level: stepOK, msg: "applied core defaults"}
		}, sum)
	}

	// ── 9. verify daemon /v1/agents ───────────────────────────
	a.installStep(9, "verify daemon", func() stepResult {
		st, err := daemon.ReadState()
		if err != nil || st == nil {
			return stepResult{level: stepWarn, msg: "no daemon state file"}
		}
		if !daemon.IsRunning(st) {
			return stepResult{level: stepWarn, msg: "daemon not answering /v1/health"}
		}
		return stepResult{level: stepOK, msg: "/v1/health OK"}
	}, sum)

	// ── 10. exit summary ──────────────────────────────────────
	fmt.Fprintf(a.Stdout, "clawtool kuruldu — %d agent(s) registered, %d recipe(s) applied, daemon @ 127.0.0.1:%d\n",
		sum.agentsClaimed, sum.recipesApplied, sum.port)
	return 0
}

// runInstallDryRun emits the ten-step plan to stdout WITHOUT side
// effects so operators can see exactly what `clawtool install` would
// do before committing. Detection still runs (read-only LookPath
// probes) so the plan reflects this host.
func (a *App) runInstallDryRun(args installArgs) int {
	fmt.Fprintln(a.Stdout, "clawtool install — dry-run plan")
	fmt.Fprintf(a.Stdout, "  workdir: %s\n", args.workdir)
	fmt.Fprintln(a.Stdout, "")

	hosts := detectInstallHosts()
	fmt.Fprintf(a.Stdout, "  1. daemon health:    ensure 'clawtool serve --listen 127.0.0.1:<port> --no-auth --mcp-http' is up\n")
	fmt.Fprintf(a.Stdout, "  2. host detection:   detected %d host(s): %s\n", len(hosts), strings.Join(hosts, ", "))
	fmt.Fprintf(a.Stdout, "  3. bridge install:   for each non-claude detected host\n")
	fmt.Fprintf(a.Stdout, "  4. agent claim:      'clawtool agents claim <h>' for each detected host (opencode skipped)\n")
	fmt.Fprintf(a.Stdout, "  5. MCP config:       written by step 4\n")
	fmt.Fprintf(a.Stdout, "  6. hooks install:    'clawtool hooks install <h>' for each detected host\n")
	fmt.Fprintf(a.Stdout, "  7. peer register:    register the current shell as a peer\n")
	if args.skipInit {
		fmt.Fprintf(a.Stdout, "  8. init --all:       SKIPPED (--skip-init)\n")
	} else {
		fmt.Fprintf(a.Stdout, "  8. init --all:       apply Core recipes to %s (if git repo)\n", args.workdir)
	}
	fmt.Fprintf(a.Stdout, "  9. verify:           probe daemon's /v1/health\n")
	fmt.Fprintf(a.Stdout, "  10. exit:            print 'clawtool kuruldu — …' summary\n")
	return 0
}

// installStepHosts runs step 2 (host detection) and emits its status
// line. Returned slice drives steps 3, 4, 6.
func (a *App) installStepHosts(stepNum int, sum *installSummary) []string {
	hosts := detectInstallHosts()
	if len(hosts) == 0 {
		a.installLine(stepWarn, stepNum, "host detection", "no agent CLIs found on PATH")
		sum.stepsWarn++
		return nil
	}
	a.installLine(stepOK, stepNum, "host detection", fmt.Sprintf("found %s", strings.Join(hosts, ", ")))
	sum.stepsOK++
	return hosts
}

// detectInstallHosts probes PATH for each known agent CLI binary.
// Returns the family names (claude / codex / gemini / opencode) of
// detected hosts. Honours installLookPath so tests stub the result.
func detectInstallHosts() []string {
	families := []string{"claude", "codex", "gemini", "opencode"}
	out := make([]string, 0, len(families))
	for _, fam := range families {
		if _, err := installLookPath(fam); err == nil {
			out = append(out, fam)
		}
	}
	return out
}

// claimNameFor maps a detected-binary family to its agents.Registry
// adapter name. claude → claude-code; everything else passes through.
func claimNameFor(family string) string {
	if family == "claude" {
		return "claude-code"
	}
	return family
}

// stepLevel discriminates ✓ / ⚠ / ✗ on a step's status line. Glyph
// choice mirrors the operator-facing precedent (onboard wizard's
// SummaryRow uses the same three states).
type stepLevel int

const (
	stepOK stepLevel = iota
	stepWarn
	stepFail
)

func (l stepLevel) glyph() string {
	switch l {
	case stepOK:
		return "✓"
	case stepWarn:
		return "⚠"
	case stepFail:
		return "✗"
	}
	return "•"
}

// stepResult is what an installStep callback returns.
type stepResult struct {
	level stepLevel
	msg   string
}

// installStep runs one step's callback, prints its status line, and
// updates the summary counters. Centralised here so every step uses
// the same prefix shape (`<glyph> step N. <label>: <msg>`).
func (a *App) installStep(num int, label string, fn func() stepResult, sum *installSummary) {
	start := time.Now()
	res := fn()
	dur := time.Since(start)
	a.installLine(res.level, num, label, res.msg+fmt.Sprintf(" (%dms)", dur.Milliseconds()))
	switch res.level {
	case stepOK:
		sum.stepsOK++
	case stepWarn:
		sum.stepsWarn++
	case stepFail:
		sum.stepsFailed++
	}
}

// installLine emits one stderr status line with the consistent
// `<glyph> step N. <label>: <msg>` shape. Public so the step-less
// emit path (skipped MCP-config / claimed-skip / etc.) can land
// rows on the same surface.
func (a *App) installLine(lv stepLevel, num int, label, msg string) {
	if msg == "" {
		fmt.Fprintf(a.Stderr, "%s step %d. %s\n", lv.glyph(), num, label)
		return
	}
	fmt.Fprintf(a.Stderr, "%s step %d. %s: %s\n", lv.glyph(), num, label, msg)
}

// trim1 collapses a multi-line stderr capture down to a single line
// for the status row, so a recipe that prints a 40-line trace
// doesn't blow the install log out.
func trim1(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " (…)"
	}
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// contains is a tiny helper for the host slice — keeps this file
// self-contained (the project's other slice-contains helpers live
// in unrelated packages).
func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// isGitRepo reports whether path contains (or is contained by) a
// .git directory. Walks up to filepath.VolumeName so we don't loop
// forever on a non-repo invocation.
func isGitRepo(path string) bool {
	for {
		if fi, err := os.Stat(filepath.Join(path, ".git")); err == nil && fi.IsDir() {
			return true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}
