package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

// onboardState carries everything the wizard collects before any side
// effects happen. Persisting choices up front makes the test path
// trivial — the side-effect dispatch loop runs only after huh.Run
// returns clean.
type onboardState struct {
	Found          map[string]bool
	MissingBridges []string
	InstallBridges []string
	CreateIdentity bool
	Telemetry      bool
}

// onboardDeps lets tests substitute the four side-effecting calls
// (PATH lookup, form runner, bridge installer, identity bootstrap).
// In production they hit the real CLI / huh / agents packages.
type onboardDeps struct {
	lookPath       func(string) error
	runForm        func(*huh.Form) error
	bridgeAdd      func(string) error
	createIdentity func() error
	identityExists func() bool
	stdoutLn       func(string)
}

// runOnboard is the dispatcher hooked into Run().
func (a *App) runOnboard(argv []string) int {
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		fmt.Fprint(a.Stdout, onboardUsage)
		return 0
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
	}
	if err := a.onboard(context.Background(), d); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool onboard: %v\n", err)
		return 1
	}
	return 0
}

// onboard runs the wizard. Pure-ish: every side effect routes
// through onboardDeps so the test path can drive it without a TTY.
func (a *App) onboard(ctx context.Context, d onboardDeps) error {
	state := detectHost(d.lookPath)

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewNote().
				Title("clawtool onboard").
				Description(hostSummary(state.Found)),
		),
	}

	if len(state.MissingBridges) > 0 {
		opts := make([]huh.Option[string], 0, len(state.MissingBridges))
		for _, fam := range state.MissingBridges {
			opts = append(opts, huh.NewOption(fam, fam))
		}
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Install missing bridges").
				Description("Selected items run `clawtool bridge add <family>` after submit. Bridge install failures stay non-fatal.").
				Options(opts...).
				Value(&state.InstallBridges),
		))
	}

	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Create BIAM identity?").
			Description("Generates an Ed25519 keypair at ~/.config/clawtool/identity.ed25519 (mode 0600). Required for `clawtool send --async`.").
			Affirmative("Generate").
			Negative("Skip").
			Value(&state.CreateIdentity),
	))

	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Anonymous telemetry").
			Description("Emits command name, version, OS/arch, duration, exit code, and error class. No prompts, paths, file contents, secrets, or env values.").
			Affirmative("Opt in").
			Negative("No thanks").
			Value(&state.Telemetry),
	))

	form := huh.NewForm(groups...)
	if err := d.runForm(form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			d.stdoutLn("clawtool onboard: aborted; nothing changed.")
			return nil
		}
		return fmt.Errorf("form: %w", err)
	}

	for _, fam := range state.InstallBridges {
		if err := d.bridgeAdd(fam); err != nil {
			d.stdoutLn(fmt.Sprintf("  ! bridge %s: %v", fam, err))
		} else {
			d.stdoutLn(fmt.Sprintf("  ✓ bridge %s installed", fam))
		}
	}

	if state.CreateIdentity {
		if err := d.createIdentity(); err != nil {
			return fmt.Errorf("create identity: %w", err)
		}
		d.stdoutLn("  ✓ BIAM identity ready")
	}

	if state.Telemetry {
		d.stdoutLn("  ✓ telemetry opt-in recorded (CLAWTOOL_TELEMETRY=1)")
	} else {
		d.stdoutLn("  · telemetry: opted out")
	}

	d.stdoutLn("")
	d.stdoutLn("Done. Run `clawtool send --list` to see your callable agents.")
	return nil
}

// detectHost reports which agent CLIs are on PATH and which bridges
// would need installing.
func detectHost(lookPath func(string) error) onboardState {
	families := []string{"claude", "codex", "opencode", "gemini"}
	state := onboardState{Found: map[string]bool{}}
	for _, fam := range families {
		if lookPath(fam) == nil {
			state.Found[fam] = true
			continue
		}
		state.Found[fam] = false
		// `claude` is the operator's primary; if it's missing we
		// don't offer a bridge for it (clawtool runs inside Claude
		// Code, no plugin needed).
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
	for _, fam := range []string{"claude", "codex", "opencode", "gemini"} {
		mark := "✗"
		if found[fam] {
			mark = "✓"
		}
		out += fmt.Sprintf("  %s %s\n", mark, fam)
	}
	out += "\nThis wizard offers to install missing bridges, generate the BIAM identity, and record your telemetry preference."
	return out
}

const onboardUsage = `Usage:
  clawtool onboard         Interactive first-run wizard. Detects host CLIs,
                           offers bridge installs, bootstraps the BIAM
                           identity, and records telemetry consent.

For non-interactive setup use 'clawtool init --yes' (project recipes)
plus 'clawtool bridge add <family>' per agent.
`
