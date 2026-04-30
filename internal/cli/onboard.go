package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/cogitave/clawtool/internal/telemetry"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/cogitave/clawtool/internal/xdg"
)

// versionShortForOnboard returns version.Resolved() trimmed of the
// `+dirty` / `-gXXXX` suffix that pollutes a dev-build header.
// Tagged releases pass through unchanged.
func versionShortForOnboard() string {
	v := version.Resolved()
	for _, sep := range []string{"+", "-"} {
		if i := indexOfRune(v, sep); i > 0 {
			v = v[:i]
		}
	}
	return v
}

func indexOfRune(s, sep string) int {
	for i := 0; i < len(s); i++ {
		if string(s[i]) == sep {
			return i
		}
	}
	return -1
}

// onboardState carries everything the wizard collects before any side
// effects happen. Persisting choices up front makes the test path
// trivial — the side-effect dispatch loop runs only after huh.Run
// returns clean.
type onboardState struct {
	Found          map[string]bool
	MissingBridges []string
	InstallBridges []string
	// PrimaryCLI is the operator's main interface — answers
	// "which CLI will you mostly drive clawtool through?". Drives
	// smart defaults: that CLI's bridge gets pre-selected for
	// install (if missing), its MCP-claim entry gets pre-checked
	// (if claimable). Empty when the operator skips the question.
	// Allowed values: "claude-code" | "codex" | "gemini" |
	// "opencode" | "hermes".
	PrimaryCLI string
	// MCPClaimable is the set of detected hosts whose `mcp add`
	// surface accepts clawtool registration today (codex, gemini,
	// opencode). The wizard defaults this to selected so the
	// operator's "every host sees clawtool" expectation holds.
	MCPClaimable []string
	ClaimMCP     []string // selected from MCPClaimable
	// StartDaemon controls the explicit daemon-up step. Defaults
	// to true so the operator gets a healthy persistent daemon
	// out of the box. The MCP-claim step calls daemon.Ensure
	// implicitly, but a dedicated yes/no question makes the
	// daemon visible in the wizard flow + lets the operator skip
	// it on hosts where a long-running listener is unwanted.
	StartDaemon    bool
	CreateIdentity bool
	// InitSecrets drops a 0600 secrets.toml stub if absent, so
	// `clawtool source set-secret <inst> <KEY>` later writes
	// without surprising the operator with a new file appearing.
	// Default true.
	InitSecrets bool
	Telemetry   bool
	RunInit     bool
}

// onboardDeps lets tests substitute the side-effecting calls
// (PATH lookup, form runner, bridge installer, identity bootstrap,
// daemon ensure, host claim). In production they hit the real CLI /
// huh / daemon / agents packages.
type onboardDeps struct {
	lookPath       func(string) error
	runForm        func(*huh.Form) error
	bridgeAdd      func(string) error
	createIdentity func() error
	identityExists func() bool
	stdoutLn       func(string)
	// claimMCPHost wraps daemon.Ensure + agents.Find(name).Claim()
	// so the wizard can register clawtool as an MCP server in each
	// selected host without leaking those details into the wizard
	// flow itself. Returns the host's URL on success.
	claimMCPHost func(string) (string, error)
	// ensureDaemon explicitly brings up the persistent daemon (or
	// returns its existing state). Returns the dialable URL.
	ensureDaemon func() (string, error)
	// initSecrets drops an empty 0600 secrets.toml if absent.
	// Idempotent; succeeds silently when the file is already
	// present (mode-0600 audit lives in `clawtool doctor`).
	initSecrets func() error
	// verifySummary runs the end-of-onboard sanity panel:
	// daemon health, agent list, doctor's [config] + [sandbox-
	// worker] sections (no full doctor — too noisy for the wizard
	// tail). Output goes to stdoutLn; never errors.
	verifySummary func()
	// track emits a telemetry event for one wizard step. Defaults
	// to telemetry.Get().Track in production (no-op when telemetry
	// is disabled) and a recording stub in tests. Per-step events
	// share `command="onboard"` and discriminate via `event_kind`
	// + the relevant taxonomy keys (agent / bridge / outcome).
	// Pre-1.0 the operator has already opted in by default, so the
	// stream of step events is what tells us where the funnel
	// drops people — fan-in for the install→onboard problem the
	// nudges target.
	track func(event string, props map[string]any)
	// forceDefaults is the --yes / unattended mode escape hatch.
	// When true, the wizard skips huh.Run and applies "what every
	// form-default would have produced": install every missing
	// bridge, claim every claimable host, start daemon, create
	// identity, init secrets, telemetry on (pre-1.0 default), no
	// project init. Drives the e2e harness + lets operators bake
	// `clawtool onboard --yes` into Dockerfiles / CI scripts.
	forceDefaults bool
}

// runOnboard is the dispatcher hooked into Run().
func (a *App) runOnboard(argv []string) int {
	yes := false
	force := false
	noDefaults := false
	for _, arg := range argv {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(a.Stdout, onboardUsage)
			return 0
		case "--yes", "-y":
			yes = true
		case "--force", "-f":
			force = true
		case "--no-defaults":
			noDefaults = true
		}
	}
	// Env-var equivalent of --no-defaults so containers / CI can
	// suppress the Core-defaults nudge without editing argv.
	if os.Getenv("CLAWTOOL_ONBOARD_NO_DEFAULTS") == "1" {
		noDefaults = true
	}
	// --force wipes the resume state + onboarded marker so the
	// wizard starts from scratch without any prompt.
	if force {
		_ = clearOnboardProgress()
		_ = os.Remove(onboardedMarkerPath())
	}
	d := onboardDeps{
		lookPath: func(bin string) error { _, err := exec.LookPath(bin); return err },
		runForm: func(f *huh.Form) error {
			f.WithAccessible(false)
			return f.Run()
		},
		bridgeAdd:      a.BridgeAdd,
		createIdentity: func() error { _, err := biam.LoadOrCreateIdentity(""); return err },
		identityExists: func() bool {
			_, err := exec.LookPath("clawtool") // placeholder; real check below
			return err == nil
		},
		stdoutLn: func(s string) { fmt.Fprintln(a.Stdout, s) },
		claimMCPHost: func(name string) (string, error) {
			st, err := daemon.Ensure(context.Background())
			if err != nil {
				return "", fmt.Errorf("ensure daemon: %w", err)
			}
			ad, err := agents.Find(name)
			if err != nil {
				return "", err
			}
			if _, err := ad.Claim(agents.Options{}); err != nil {
				return "", err
			}
			return st.URL(), nil
		},
		ensureDaemon: func() (string, error) {
			st, err := daemon.Ensure(context.Background())
			if err != nil {
				return "", err
			}
			return st.URL(), nil
		},
		initSecrets: func() error {
			path := a.SecretsPath()
			if _, err := os.Stat(path); err == nil {
				return nil // already present; respect operator
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			return os.WriteFile(path,
				[]byte("# clawtool secrets store — mode 0600 by convention.\n# Add per-instance API keys via:\n#   clawtool source set-secret <instance> <KEY> --value <v>\n"),
				0o600)
		},
		verifySummary: func() {
			fmt.Fprintln(a.Stdout, "")
			fmt.Fprintln(a.Stdout, "── verify ───────────────────────────────────")
			a.runOverview(nil)
		},
		track: func(event string, props map[string]any) {
			if tc := telemetry.Get(); tc != nil && tc.Enabled() {
				tc.Track(event, props)
			}
		},
		forceDefaults: yes,
	}
	// Interactive TTY path → Bubble Tea wizard with alt-screen
	// buffer + stepwise progression. --yes / non-TTY (CI, pipes,
	// docker exec without -t, the test harness) falls through to
	// the linear onboard() implementation so its plain-text
	// contract stays stable.
	//
	// Resolve stdout / stdin to *os.File. App.Stdin is unset by
	// default (cli.New() only wires Stdout + Stderr), so when the
	// embedded reader isn't an *os.File we fall back to the real
	// os.Stdin / os.Stdout — that's what production invocations
	// actually use, and it's the right TTY to probe.
	stdout, _ := a.Stdout.(*os.File)
	if stdout == nil {
		stdout = os.Stdout
	}
	stdin, _ := a.Stdin.(*os.File)
	if stdin == nil {
		stdin = os.Stdin
	}
	useTUI := !yes && isTTY(stdout) && isTTY(stdin)
	if useTUI {
		if err := a.onboardTUI(context.Background(), d); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Fprintln(a.Stdout, "clawtool onboard: aborted; nothing changed.")
				return 0
			}
			fmt.Fprintf(a.Stderr, "clawtool onboard: %v\n", err)
			return 1
		}
		a.maybeNudgeCoreDefaults(yes, noDefaults)
		return 0
	}
	if err := a.onboard(context.Background(), d); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool onboard: %v\n", err)
		return 1
	}
	a.maybeNudgeCoreDefaults(yes, noDefaults)
	return 0
}

// maybeNudgeCoreDefaults runs the post-onboard "Install core
// defaults? [Y/n]" prompt and, on Y / non-TTY / --yes, applies every
// Core recipe via runInitAll. Skipped when --no-defaults /
// CLAWTOOL_ONBOARD_NO_DEFAULTS=1.
//
// Lives in runOnboard (post-wizard) so the existing onboard / TUI
// test suites stay untouched: they exercise app.onboard()/onboardTUI
// directly, not runOnboard, so this nudge never fires from those
// tests.
func (a *App) maybeNudgeCoreDefaults(yes, noDefaults bool) {
	if noDefaults {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	// --yes / non-TTY: apply unconditionally (operator-side
	// "everything we ship" intent).
	stdout, _ := a.Stdout.(*os.File)
	if stdout == nil {
		stdout = os.Stdout
	}
	stdin, _ := a.Stdin.(*os.File)
	if stdin == nil {
		stdin = os.Stdin
	}
	tty := isTTY(stdout) && isTTY(stdin)
	if yes || !tty {
		fmt.Fprintln(a.Stdout, "")
		fmt.Fprintln(a.Stdout, "── core defaults ────────────────────────────")
		_ = a.runInitAll(cwd)
		return
	}
	// Interactive: ask once with Y default.
	apply := true
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Install core defaults?").
			Description("Apply every recipe clawtool considers part of the curated default install (CLAUDE.md, agent-claim, conventional-commits-ci, promptfoo-redteam, etc.). Skip with --no-defaults or CLAWTOOL_ONBOARD_NO_DEFAULTS=1.").
			Affirmative("Yes, install").
			Negative("Skip").
			Value(&apply),
	))
	if err := form.Run(); err != nil {
		// Aborted prompts are silent — operator can still run
		// `clawtool init --all` later.
		return
	}
	if !apply {
		return
	}
	fmt.Fprintln(a.Stdout, "")
	fmt.Fprintln(a.Stdout, "── core defaults ────────────────────────────")
	_ = a.runInitAll(cwd)
}

// onboardTUI wraps the Bubble Tea wizard. The model owns the entire
// flow (steps + run-phase progress + summary); we just hand it the
// detected host state and the dep callbacks. Side-effect callbacks
// (bridgeAdd, claimMCPHost, ...) are the same ones the linear path
// uses, so both implementations execute identical real work.
func (a *App) onboardTUI(ctx context.Context, d onboardDeps) error {
	state := detectHost(d.lookPath)
	track := d.track
	if track == nil {
		track = func(string, map[string]any) {}
	}
	track("clawtool.onboard", map[string]any{"event_kind": "start", "command": "onboard"})

	// Re-entry / resume gate. Three cases:
	//   1. Progress file present → operator interrupted a previous
	//      session. Ask whether to resume from where they left off
	//      or start over.
	//   2. .onboarded marker present, no progress file → wizard
	//      previously ran to completion. Ask whether to re-run.
	//   3. Neither → fresh wizard (no extra prompt).
	startStep := 0
	progress, perr := loadOnboardProgress()
	if perr != nil {
		// Couldn't parse the file — surface and start fresh.
		// The wizard remains usable; we just lost the resume
		// shortcut for this run.
		fmt.Fprintf(a.Stderr, "clawtool onboard: ignoring corrupt progress file: %v\n", perr)
		_ = clearOnboardProgress()
	}
	if progress != nil {
		choice, err := promptResume(progress, a.Stdout, a.Stderr)
		if err != nil {
			return err
		}
		switch choice {
		case "resume":
			state = progress.State
			startStep = progress.StepIdx
			track("clawtool.onboard", map[string]any{"event_kind": "resume", "step_idx": startStep})
		case "restart":
			_ = clearOnboardProgress()
			track("clawtool.onboard", map[string]any{"event_kind": "restart_from_progress"})
		case "cancel":
			d.stdoutLn("clawtool onboard: cancelled; previous progress kept.")
			return nil
		}
	} else if IsOnboarded() {
		choice, err := promptAlreadyOnboarded(a.Stdout, a.Stderr)
		if err != nil {
			return err
		}
		switch choice {
		case "redo":
			track("clawtool.onboard", map[string]any{"event_kind": "redo"})
		case "cancel":
			d.stdoutLn("clawtool onboard: already configured; nothing to do.")
			d.stdoutLn("(re-run with `clawtool onboard --force` to wipe and start fresh.)")
			return nil
		}
	}

	if err := runOnboardTUI(ctx, &state, d, track, startStep); err != nil {
		return err
	}

	// Post-program output (telemetry thank-you, star CTA, verify
	// summary) lands AFTER the alt-screen exits so the operator's
	// regular terminal scrollback gets these lines.
	d.stdoutLn("Done. Run `clawtool send --list` to see your callable agents.")
	if d.verifySummary != nil {
		d.verifySummary()
	}
	if state.Telemetry {
		d.stdoutLn("")
		d.stdoutLn("───────────────────────────────────────────────────")
		d.stdoutLn("Telemetry stays on through v1.0.0 while clawtool is")
		d.stdoutLn("in active development — anonymous usage data tells")
		d.stdoutLn("us which paths actually get used so we can sharpen")
		d.stdoutLn("them. Thank you for contributing to the build by")
		d.stdoutLn("leaving it on; if it ever feels invasive, flip it")
		d.stdoutLn("off any time with: clawtool telemetry off")
		d.stdoutLn("───────────────────────────────────────────────────")
	}
	d.stdoutLn("")
	d.stdoutLn("⭐ Enjoying clawtool? A GitHub star helps a lot:")
	d.stdoutLn("   https://github.com/cogitave/clawtool")
	return nil
}

// onboard runs the wizard. Pure-ish: every side effect routes
// through onboardDeps so the test path can drive it without a TTY.
func (a *App) onboard(ctx context.Context, d onboardDeps) error {
	state := detectHost(d.lookPath)

	// Visual canvas: clear the operator's terminal so the wizard
	// lands on a clean slate (escape sequence is a no-op when
	// stdout isn't a tty, so piped invocations stay log-greppable),
	// then render a tight rounded-box header with the host
	// detection summary as a single pill row. Replaces the prior
	// multi-line `huh.NewNote` welcome group that overflowed on
	// small terminals.
	ux := newOnboardUX(a.Stdout)
	ux.ClearScreen()
	ux.Header(versionShortForOnboard(), state.Found)

	groups := []*huh.Group{}

	// Primary CLI — the operator's main interface. Drives smart
	// defaults for every following question. Pre-selected to the
	// detected host that's most likely the primary (claude-code
	// when it's on PATH, since clawtool itself is most often
	// running inside Claude Code; falls back to the first detected
	// CLI otherwise). Operator can override.
	primaryOpts := primaryCLIOptions(state.Found)
	state.PrimaryCLI = primaryDefault(state.Found)
	groups = append(groups, huh.NewGroup(
		huh.NewSelect[string]().
			Title("Which CLI will you primarily use?").
			Description("Pick the agent you'll spend most of your time in. clawtool routes through that one as the primary; the others connect via MCP / bridge so you can dispatch across them. Choosing claude-code means clawtool is registered as a Claude Code plugin (already done if you installed via the marketplace); choosing codex / gemini / opencode auto-selects that family's bridge for install + MCP claim. Pick \"none / decide later\" to skip the smart defaults.").
			Options(primaryOpts...).
			Value(&state.PrimaryCLI),
	))

	if len(state.MissingBridges) > 0 {
		opts := make([]huh.Option[string], 0, len(state.MissingBridges))
		for _, fam := range state.MissingBridges {
			opts = append(opts, huh.NewOption(fam, fam))
		}
		// Smart default: when the operator's primary CLI is one
		// of the missing-bridge families (and isn't claude-code,
		// which uses the plugin install path), pre-check it so
		// they don't have to hunt for the right entry.
		if state.PrimaryCLI != "" && state.PrimaryCLI != "claude-code" {
			for _, fam := range state.MissingBridges {
				if fam == state.PrimaryCLI {
					state.InstallBridges = []string{fam}
					break
				}
			}
		}
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Install missing bridges").
				Description("Selected items run `clawtool bridge add <family>` after submit. Bridge install failures stay non-fatal. Your primary CLI's bridge is pre-checked.").
				Options(opts...).
				Value(&state.InstallBridges),
		))
	}

	if len(state.MCPClaimable) > 0 {
		opts := make([]huh.Option[string], 0, len(state.MCPClaimable))
		for _, h := range state.MCPClaimable {
			opts = append(opts, huh.NewOption(h, h))
		}
		// Default to selecting all so the operator's "every host
		// sees clawtool" intent is the path of least resistance.
		// When PrimaryCLI is set and it's claimable, that entry
		// is guaranteed in the default selection (idempotent
		// since it's already in the all-claimable set).
		state.ClaimMCP = append([]string{}, state.MCPClaimable...)
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Register clawtool as an MCP server in these hosts").
				Description("Starts a single persistent local daemon (loopback HTTP + bearer auth) and points each selected host at it. Without this, hosts can't see clawtool tools or send cross-host messages. Your primary CLI is included by default.").
				Options(opts...).
				Value(&state.ClaimMCP),
		))
	}

	state.StartDaemon = true
	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Start the persistent daemon now?").
			Description("`clawtool serve --listen --mcp-http` is the single backend every host (codex / gemini / claude / opencode) fans into. Default = on. Skip only if you'll start it later via `clawtool daemon start`.").
			Affirmative("Start daemon").
			Negative("Skip").
			Value(&state.StartDaemon),
	))

	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Create BIAM identity?").
			Description("Generates an Ed25519 keypair at ~/.config/clawtool/identity.ed25519 (mode 0600). Required for `clawtool send --async` and cross-host BIAM messaging.").
			Affirmative("Generate").
			Negative("Skip").
			Value(&state.CreateIdentity),
	))

	state.InitSecrets = true
	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Initialise the secrets store?").
			Description("Drops an empty 0600 secrets.toml at ~/.config/clawtool/secrets.toml so `clawtool source set-secret <inst> <KEY> --value <v>` writes without surprising you with a new file. Idempotent — skips when already present. Default = on.").
			Affirmative("Initialise").
			Negative("Skip").
			Value(&state.InitSecrets),
	))

	groups = append(groups, huh.NewGroup(
		huh.NewNote().
			Title("Sandbox worker (optional, advanced)").
			Description("Routes Bash/Read/Edit/Write tool calls through an isolated container instead of the daemon's host process. Default = off (host execution). To enable later: build the worker image and flip [sandbox_worker] mode to \"container\" in ~/.config/clawtool/config.toml. Run `clawtool sandbox-worker --help` for the full surface."),
	))

	// Pre-1.0 development phase: default to opt-in. The wizard
	// description explains exactly what flows; the operator can
	// still flip Negative if they want full silence. We collapse
	// back to opt-out default at v1.0 (tracked in the roadmap).
	state.Telemetry = true
	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Anonymous telemetry (pre-1.0 default = on)").
			Description("Until v1.0.0 ships, telemetry is on by default — clawtool is in active development and the dashboard is what tells us which paths actually get used. Emits ONLY: command name + subcommand, version, OS/arch, duration, exit code, error class, agent FAMILY (claude/codex/gemini/opencode/hermes — never the instance label), recipe / engine / bridge names from the public catalog. NEVER: prompts, paths, file contents, secrets, env values, instance IDs, hostnames. Anonymous distinct ID at ~/.local/share/clawtool/telemetry-id. Flip to 'No thanks' for total silence.").
			Affirmative("Opt in").
			Negative("No thanks").
			Value(&state.Telemetry),
	))

	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Run `clawtool init` after onboard?").
			Description("Onboard set up your host. `clawtool init` is the project-level wizard that injects release-please / dependabot / commitlint / brain / etc. into the repo you're sitting in. Skip if you'd rather run it later in a different repo.").
			Affirmative("Yes, set this repo up too").
			Negative("Skip").
			Value(&state.RunInit),
	))

	track := d.track
	if track == nil {
		track = func(string, map[string]any) {}
	}
	track("clawtool.onboard", map[string]any{"event_kind": "start", "command": "onboard"})

	if d.forceDefaults {
		// Yes-mode: install EVERY missing bridge (the form's
		// pre-set is conditional on PrimaryCLI matching one
		// missing-bridge entry, which leaves multi-missing
		// scenarios un-checked otherwise). Identity gets
		// generated by default. The other state fields already
		// carry their pre-form defaults (StartDaemon, ClaimMCP,
		// InitSecrets, Telemetry) so they need no override.
		// Skip huh.Run entirely — the smart-default state IS
		// the answer.
		state.InstallBridges = append([]string{}, state.MissingBridges...)
		state.CreateIdentity = true
	} else {
		form := huh.NewForm(groups...)
		if err := d.runForm(form); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				d.stdoutLn("clawtool onboard: aborted; nothing changed.")
				track("clawtool.onboard", map[string]any{"event_kind": "finish", "outcome": "cancelled"})
				return nil
			}
			track("clawtool.onboard", map[string]any{"event_kind": "finish", "outcome": "error"})
			return fmt.Errorf("form: %w", err)
		}
	}

	track("clawtool.onboard", map[string]any{
		"event_kind": "host_detect",
		"agent":      state.PrimaryCLI,
	})

	// Side-effect dispatch — every step renders through the
	// onboardUX as a phase line so the operator sees structured
	// progress (Section + → label + ✓ result + dim duration)
	// instead of the prior raw stdoutLn block of mixed glyphs.
	// Each phase outcome also feeds the closing Summary so the
	// operator gets a tight checklist of what was wired.
	var summary []SummaryRow

	if len(state.InstallBridges) > 0 {
		ux.Section("Bridges")
		for _, fam := range state.InstallBridges {
			ux.PhaseStart(fmt.Sprintf("install bridge %s", fam))
			outcome := "success"
			if err := d.bridgeAdd(fam); err != nil {
				outcome = "error"
				ux.PhaseFail(err.Error())
				summary = append(summary, SummaryRow{Label: "bridge " + fam, Outcome: "fail", Detail: err.Error()})
			} else {
				ux.PhaseDone("")
				summary = append(summary, SummaryRow{Label: "bridge " + fam, Outcome: "ok"})
			}
			track("clawtool.onboard", map[string]any{
				"event_kind": "bridge_install",
				"bridge":     fam,
				"outcome":    outcome,
			})
		}
	}

	if len(state.ClaimMCP) > 0 {
		ux.Section("MCP host registration")
		for _, h := range state.ClaimMCP {
			ux.PhaseStart(fmt.Sprintf("register %s", h))
			if d.claimMCPHost == nil {
				ux.PhaseSkip("not wired (test build?)")
				summary = append(summary, SummaryRow{Label: "MCP " + h, Outcome: "skip"})
				track("clawtool.onboard", map[string]any{
					"event_kind": "mcp_claim",
					"agent":      h,
					"outcome":    "skipped",
				})
				continue
			}
			outcome := "success"
			url, err := d.claimMCPHost(h)
			if err != nil {
				outcome = "error"
				ux.PhaseFail(err.Error())
				summary = append(summary, SummaryRow{Label: "MCP " + h, Outcome: "fail", Detail: err.Error()})
			} else {
				ux.PhaseDone(url)
				summary = append(summary, SummaryRow{Label: "MCP " + h, Outcome: "ok", Detail: url})
			}
			track("clawtool.onboard", map[string]any{
				"event_kind": "mcp_claim",
				"agent":      h,
				"outcome":    outcome,
			})
		}
	}

	if state.StartDaemon && d.ensureDaemon != nil {
		ux.Section("Daemon")
		ux.PhaseStart("start persistent daemon")
		outcome := "success"
		if url, err := d.ensureDaemon(); err != nil {
			outcome = "error"
			ux.PhaseFail(err.Error())
			summary = append(summary, SummaryRow{Label: "daemon", Outcome: "fail", Detail: err.Error()})
		} else {
			ux.PhaseDone(url)
			summary = append(summary, SummaryRow{Label: "daemon", Outcome: "ok", Detail: url})
		}
		track("clawtool.onboard", map[string]any{
			"event_kind": "daemon_start",
			"outcome":    outcome,
		})
	}

	if state.CreateIdentity {
		ux.Section("Identity")
		ux.PhaseStart("generate BIAM Ed25519 keypair")
		if err := d.createIdentity(); err != nil {
			ux.PhaseFail(err.Error())
			track("clawtool.onboard", map[string]any{
				"event_kind": "identity_create",
				"outcome":    "error",
			})
			return fmt.Errorf("create identity: %w", err)
		}
		ux.PhaseDone("~/.config/clawtool/identity.ed25519, mode 0600")
		summary = append(summary, SummaryRow{Label: "BIAM identity", Outcome: "ok"})
		track("clawtool.onboard", map[string]any{
			"event_kind": "identity_create",
			"outcome":    "success",
		})
	}

	if state.InitSecrets && d.initSecrets != nil {
		ux.Section("Secrets store")
		ux.PhaseStart("initialise empty secrets.toml")
		outcome := "success"
		if err := d.initSecrets(); err != nil {
			outcome = "error"
			ux.PhaseFail(err.Error())
			summary = append(summary, SummaryRow{Label: "secrets store", Outcome: "fail", Detail: err.Error()})
		} else {
			ux.PhaseDone("~/.config/clawtool/secrets.toml, mode 0600")
			summary = append(summary, SummaryRow{Label: "secrets store", Outcome: "ok"})
		}
		track("clawtool.onboard", map[string]any{
			"event_kind": "secrets_init",
			"outcome":    outcome,
		})
	}

	// Telemetry preference goes into the summary as a status row
	// rather than its own phase — it's a recorded preference, not
	// a side-effect that "ran."
	if state.Telemetry {
		summary = append(summary, SummaryRow{Label: "telemetry", Outcome: "ok", Detail: "opted in"})
		track("clawtool.onboard", map[string]any{
			"event_kind": "telemetry_optin",
			"outcome":    "success",
		})
	} else {
		summary = append(summary, SummaryRow{Label: "telemetry", Outcome: "skip", Detail: "opted out"})
		track("clawtool.onboard", map[string]any{
			"event_kind": "telemetry_optout",
			"outcome":    "success",
		})
	}

	// Mark the host as onboarded so the install.sh / SessionStart
	// / first-run nudges stop firing. Best-effort — a write failure
	// is logged but doesn't fail onboarding (operator can still
	// dispatch + the next nudge harmlessly re-suggests the wizard).
	if err := writeOnboardedMarker(); err != nil {
		d.stdoutLn(fmt.Sprintf("  ! could not write onboarded marker: %v", err))
	}

	// Closing checklist + next-steps panel. Replaces the prior
	// stream of stdoutLn paragraphs with one tight scan-friendly
	// block: every wired component on one screen.
	ux.Summary(summary)

	var nextSteps []string
	if state.PrimaryCLI != "" {
		nextSteps = append(nextSteps, fmt.Sprintf("Primary interface: %s", state.PrimaryCLI))
	}
	if state.RunInit {
		nextSteps = append(nextSteps, "clawtool init     drop project recipes (release-please / dependabot / brain) into this repo")
	}
	nextSteps = append(nextSteps, "clawtool send --list     see your callable agents")
	nextSteps = append(nextSteps, "clawtool overview        live state of daemon + active dispatches")
	ux.NextSteps(nextSteps)

	// Existing test contract: the post-onboard hint must mention
	// `clawtool send --list` so operators know where to discover
	// callable agents. Keep emitted via stdoutLn so the test
	// harness's recorded buffer still sees it.
	d.stdoutLn("Done. Run `clawtool send --list` to see your callable agents.")

	if d.verifySummary != nil {
		d.verifySummary()
	}

	// Pre-1.0 telemetry thank-you. Lands at the very end so it's
	// the last thing the operator reads before the prompt comes
	// back. Only when they actually opted in.
	if state.Telemetry {
		d.stdoutLn("")
		d.stdoutLn("───────────────────────────────────────────────────")
		d.stdoutLn("Telemetry stays on through v1.0.0 while clawtool is")
		d.stdoutLn("in active development — anonymous usage data tells")
		d.stdoutLn("us which paths actually get used so we can sharpen")
		d.stdoutLn("them. Thank you for contributing to the build by")
		d.stdoutLn("leaving it on; if it ever feels invasive, flip it")
		d.stdoutLn("off any time with: clawtool telemetry off")
		d.stdoutLn("───────────────────────────────────────────────────")
	}

	// Star CTA. The closing nudge — operators who got this far
	// almost-certainly have something working, and a star is the
	// cheapest signal we can ask for. Plain text, single line,
	// shown after the telemetry block so the wizard finishes on
	// "here's where to give back" rather than "here's a privacy
	// disclosure". No prompt — just an URL the operator can click
	// (most modern terminals OSC-8 underline-link plain URLs).
	d.stdoutLn("")
	d.stdoutLn("⭐ Enjoying clawtool? A GitHub star helps a lot:")
	d.stdoutLn("   https://github.com/cogitave/clawtool")

	track("clawtool.onboard", map[string]any{
		"event_kind": "finish",
		"outcome":    "success",
	})
	return nil
}

// detectHost reports which agent CLIs are on PATH, which bridges
// would need installing, and which detected hosts can be claimed as
// shared-MCP fan-in targets.
//
// `hermes` was added per Codex 491d1332 audit (was previously omitted
// from family detection — operator could detect-Hermes-as-bridge but
// not surface it in the wizard).
func detectHost(lookPath func(string) error) onboardState {
	families := []string{"claude", "codex", "opencode", "gemini", "hermes"}
	// Hosts whose `mcp add` we know how to drive (matches the
	// internal/agents/mcp_host.go registrations). claude is its own
	// path — clawtool runs INSIDE Claude Code so MCP registration
	// happens via the marketplace plugin, not via this wizard.
	mcpClaimable := map[string]bool{"codex": true, "gemini": true, "opencode": true}

	state := onboardState{Found: map[string]bool{}}
	for _, fam := range families {
		if lookPath(fam) == nil {
			state.Found[fam] = true
			if mcpClaimable[fam] {
				state.MCPClaimable = append(state.MCPClaimable, fam)
			}
			continue
		}
		state.Found[fam] = false
		if fam != "claude" {
			state.MissingBridges = append(state.MissingBridges, fam)
		}
	}
	return state
}

// hostSummary renders the host-detection result as the welcome
// page's body. Stable formatting → easy snapshot in tests.
func hostSummary(found map[string]bool) string {
	out := "Detected host CLIs:\n"
	for _, fam := range []string{"claude", "codex", "opencode", "gemini", "hermes"} {
		mark := "✗"
		if found[fam] {
			mark = "✓"
		}
		out += fmt.Sprintf("  %s %s\n", mark, fam)
	}
	out += "\nThis wizard offers to install missing bridges, register clawtool as an MCP server in detected hosts, generate the BIAM identity, and record your telemetry preference."
	return out
}

// primaryCLIOptions builds the Primary CLI select-list. Detected
// hosts are listed first (with a "✓" prefix in the label so the
// operator's eye lands on what's already installed); undetected
// follow with their family name unannotated. A trailing "none /
// decide later" sentinel lets the operator skip smart defaults.
//
// Order matters for the wizard's "first option = default cursor"
// behavior — claude-code goes first when present because clawtool
// runs inside Claude Code most of the time.
func primaryCLIOptions(found map[string]bool) []huh.Option[string] {
	families := []string{"claude-code", "codex", "gemini", "opencode", "hermes"}
	opts := make([]huh.Option[string], 0, len(families)+1)
	// Detected first.
	for _, fam := range families {
		key := fam
		if fam == "claude-code" {
			// claude-code uses the plugin path; PATH check
			// looks for "claude" binary.
			key = "claude"
		}
		if found[key] {
			opts = append(opts, huh.NewOption(fam+" (✓ detected)", fam))
		}
	}
	// Undetected after.
	for _, fam := range families {
		key := fam
		if fam == "claude-code" {
			key = "claude"
		}
		if !found[key] {
			opts = append(opts, huh.NewOption(fam, fam))
		}
	}
	opts = append(opts, huh.NewOption("none / decide later", ""))
	return opts
}

// primaryDefault picks the most-likely primary CLI to seed the
// select widget. Order: claude-code (detected) → first detected
// family → empty (operator picks).
func primaryDefault(found map[string]bool) string {
	if found["claude"] {
		return "claude-code"
	}
	for _, fam := range []string{"codex", "gemini", "opencode", "hermes"} {
		if found[fam] {
			return fam
		}
	}
	return ""
}

// onboardedMarkerPath returns the file `clawtool onboard` writes
// when the wizard completes successfully. SessionStart hook + the
// CLI's no-args first-run check both consume this signal to decide
// whether to nudge the operator.
//
// Lives in $XDG_CONFIG_HOME/clawtool/.onboarded (fallback
// ~/.config/clawtool/.onboarded), zero-byte timestamped via mtime.
// Single source of truth — never branch on "config.toml exists" or
// "daemon is up", those are partial signals.
func onboardedMarkerPath() string {
	return filepath.Join(xdg.ConfigDir(), ".onboarded")
}

// writeOnboardedMarker creates the marker file. Idempotent. mode
// 0644 since the contents are non-secret (timestamp only) and a
// missing parent dir is created at 0700 to match the rest of the
// config tree.
func writeOnboardedMarker() error {
	path := onboardedMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// IsOnboarded reports whether the operator has completed the
// onboard wizard at least once. Exported so the SessionStart hook
// (claude_bootstrap.go) and the no-args first-run check can both
// consume the same signal.
func IsOnboarded() bool {
	_, err := os.Stat(onboardedMarkerPath())
	return err == nil
}

const onboardUsage = `Usage:
  clawtool onboard           Interactive first-run wizard. Detects host CLIs,
                             offers bridge installs, bootstraps the BIAM
                             identity, and records telemetry consent. If you
                             interrupt the wizard mid-flow (Ctrl-C, terminal
                             close), the next invocation prompts to resume
                             from the step you left off. If the wizard has
                             already completed once, the next invocation
                             prompts before re-running.
  clawtool onboard --yes     Non-interactive: skip every prompt and apply the
                             wizard's smart defaults (install every missing
                             bridge, claim every claimable host, start daemon,
                             generate identity, init secrets stub). Drives
                             Dockerfiles / CI scripts / the e2e harness. Alias: -y.
  clawtool onboard --force   Wipe the saved progress + the onboarded marker
                             before launching, so the wizard starts from
                             scratch with no resume / re-entry prompt. Alias: -f.

For project-level recipes (release-please / dependabot / brain / etc.)
use 'clawtool init --yes' separately.
`
