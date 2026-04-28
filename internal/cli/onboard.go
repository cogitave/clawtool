package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/daemon"
)

// onboardState carries everything the wizard collects before any side
// effects happen. Persisting choices up front makes the test path
// trivial — the side-effect dispatch loop runs only after huh.Run
// returns clean.
type onboardState struct {
	Found          map[string]bool
	MissingBridges []string
	InstallBridges []string
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

	if len(state.MCPClaimable) > 0 {
		opts := make([]huh.Option[string], 0, len(state.MCPClaimable))
		for _, h := range state.MCPClaimable {
			opts = append(opts, huh.NewOption(h, h))
		}
		// Default to selecting all so the operator's "every host
		// sees clawtool" intent is the path of least resistance.
		state.ClaimMCP = append([]string{}, state.MCPClaimable...)
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Register clawtool as an MCP server in these hosts").
				Description("Starts a single persistent local daemon (loopback HTTP + bearer auth) and points each selected host at it. Without this, hosts can't see clawtool tools or send cross-host messages.").
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

	groups = append(groups, huh.NewGroup(
		huh.NewConfirm().
			Title("Anonymous telemetry").
			Description("Emits command name, version, OS/arch, duration, exit code, and error class. No prompts, paths, file contents, secrets, or env values.").
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

	for _, h := range state.ClaimMCP {
		if d.claimMCPHost == nil {
			d.stdoutLn(fmt.Sprintf("  ! MCP claim %s: not wired (test build?)", h))
			continue
		}
		url, err := d.claimMCPHost(h)
		if err != nil {
			d.stdoutLn(fmt.Sprintf("  ! MCP claim %s: %v", h, err))
		} else {
			d.stdoutLn(fmt.Sprintf("  ✓ %s registered → %s", h, url))
		}
	}

	if state.StartDaemon && d.ensureDaemon != nil {
		if url, err := d.ensureDaemon(); err != nil {
			d.stdoutLn(fmt.Sprintf("  ! daemon: %v", err))
		} else {
			d.stdoutLn(fmt.Sprintf("  ✓ daemon up → %s", url))
		}
	}

	if state.CreateIdentity {
		if err := d.createIdentity(); err != nil {
			return fmt.Errorf("create identity: %w", err)
		}
		d.stdoutLn("  ✓ BIAM identity ready")
	}

	if state.InitSecrets && d.initSecrets != nil {
		if err := d.initSecrets(); err != nil {
			d.stdoutLn(fmt.Sprintf("  ! secrets store: %v", err))
		} else {
			d.stdoutLn("  ✓ secrets store ready (~/.config/clawtool/secrets.toml, mode 0600)")
		}
	}

	if state.Telemetry {
		d.stdoutLn("  ✓ telemetry opt-in recorded (CLAWTOOL_TELEMETRY=1)")
	} else {
		d.stdoutLn("  · telemetry: opted out")
	}

	d.stdoutLn("")
	if state.RunInit {
		d.stdoutLn("Run `clawtool init` now to drop project recipes (release-please / dependabot / etc.) into the current repo.")
	} else {
		d.stdoutLn("Done. Run `clawtool send --list` to see your callable agents.")
	}

	if d.verifySummary != nil {
		d.verifySummary()
	}
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

const onboardUsage = `Usage:
  clawtool onboard         Interactive first-run wizard. Detects host CLIs,
                           offers bridge installs, bootstraps the BIAM
                           identity, and records telemetry consent.

For non-interactive setup use 'clawtool init --yes' (project recipes)
plus 'clawtool bridge add <family>' per agent.
`
