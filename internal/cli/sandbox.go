// Package cli — `clawtool sandbox` subcommand surface (ADR-020).
//
// v0.18 ships read-only verbs (list / show / doctor) plus the
// surface stub for `run`. The dispatch-time integration
// (`clawtool send --sandbox <profile>`) lands v0.18.1+ alongside
// the per-OS engine implementations.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/cli/listfmt"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/sandbox"
)

// sandboxDoctorJSON is the wire shape produced by `sandbox doctor
// --json`. Mirrors the rest of the project's snake_case wire
// convention (matches agentListEntry, agents.Status, BuildInfo).
// Defined locally instead of tagging sandbox.EngineStatus
// directly so the MCP tool path's pre-existing PascalCase wire
// shape isn't perturbed — that's a separate audit/migration.
type sandboxDoctorJSON struct {
	Engines  []sandboxEngineEntry `json:"engines"`
	Selected string               `json:"selected"`
}

type sandboxEngineEntry struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

const sandboxUsage = `Usage:
  clawtool sandbox list             List configured profiles.
  clawtool sandbox show <name>      Render a parsed profile + resolved engine.
  clawtool sandbox doctor           Check which sandbox engines are available.
  clawtool sandbox run <name> -- <cmd ...>
                                    One-off sandboxed command (escape hatch).

Profiles live under [sandboxes.<name>] in ~/.config/clawtool/config.toml.
Per-agent default lands in [agents.X].sandbox = "<profile>".

Engines:
  Linux    — bubblewrap (bwrap)
  macOS    — sandbox-exec (Seatbelt)
  Anywhere — docker (fallback)
  noop     — when nothing is available; surface works, enforcement absent

See docs/sandbox.md for the full design.
`

func (a *App) runSandbox(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, sandboxUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		format, _, err := listfmt.ExtractFlag(argv[1:])
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool sandbox list: %v\n", err)
			return 2
		}
		return dispatchPlainErr(a.Stderr, "sandbox list", a.SandboxList(format))
	case "show":
		if len(argv) != 2 {
			fmt.Fprintln(a.Stderr, "usage: clawtool sandbox show <name>")
			return 2
		}
		return dispatchPlainErr(a.Stderr, "sandbox show", a.SandboxShow(argv[1]))
	case "doctor":
		asJSON := false
		for _, x := range argv[1:] {
			if x == "--json" || x == "--format=json" {
				asJSON = true
			}
		}
		return dispatchPlainErr(a.Stderr, "sandbox doctor", a.SandboxDoctor(asJSON))
	case "run":
		fmt.Fprintln(a.Stderr, "clawtool sandbox run: surface only — engine enforcement is wired through `clawtool send --sandbox <profile>`.")
		fmt.Fprintln(a.Stderr, "  This verb validates the profile but doesn't run the command.")
		return 1
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, sandboxUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool sandbox: unknown subcommand %q\n\n%s", argv[0], sandboxUsage)
		return 2
	}
}

// SandboxList prints every configured profile + the engine that
// would run it on this host.
func (a *App) SandboxList(format listfmt.Format) error {
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return err
	}
	names := make([]string, 0, len(cfg.Sandboxes))
	for n := range cfg.Sandboxes {
		names = append(names, n)
	}
	sort.Strings(names)

	engine := sandbox.SelectEngine()
	cols := listfmt.Cols{Header: []string{"PROFILE", "ENGINE", "DESCRIPTION"}}
	for _, n := range names {
		p := cfg.Sandboxes[n]
		cols.Rows = append(cols.Rows, []string{n, engine.Name(), strings.TrimSpace(p.Description)})
	}
	// Empty-state contract codified in listfmt.RenderOrHint
	// (sister of skill / source / portal / hooks list): table
	// mode emits the human hint, JSON + TSV route through Render
	// so pipelines see `[]\n` / a header line.
	return listfmt.RenderOrHint(a.Stdout, format, cols, "(no sandbox profiles configured — see docs/sandbox.md)")
}

// SandboxShow parses one profile + prints the resolved view.
func (a *App) SandboxShow(name string) error {
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return err
	}
	raw, ok := cfg.Sandboxes[name]
	if !ok {
		return fmt.Errorf("profile %q not found in config.toml", name)
	}
	profile, err := sandbox.ParseProfile(name, raw)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "name        %s\n", profile.Name)
	if profile.Description != "" {
		fmt.Fprintf(a.Stdout, "description %s\n", profile.Description)
	}
	fmt.Fprintln(a.Stdout, "paths:")
	for _, r := range profile.Paths {
		fmt.Fprintf(a.Stdout, "  %s   %s\n", r.Mode, r.Path)
	}
	fmt.Fprintf(a.Stdout, "network     %s\n", profile.Network.Mode)
	if profile.Network.Mode == "allowlist" {
		for _, host := range profile.Network.Allow {
			fmt.Fprintf(a.Stdout, "  allow %s\n", host)
		}
	}
	if profile.Limits.Timeout > 0 {
		fmt.Fprintf(a.Stdout, "timeout     %s\n", profile.Limits.Timeout)
	}
	if profile.Limits.MemoryBytes > 0 {
		fmt.Fprintf(a.Stdout, "memory      %d bytes\n", profile.Limits.MemoryBytes)
	}
	if profile.Limits.CPUShares > 0 {
		fmt.Fprintf(a.Stdout, "cpu_shares  %d\n", profile.Limits.CPUShares)
	}
	if profile.Limits.ProcessCount > 0 {
		fmt.Fprintf(a.Stdout, "max_procs   %d\n", profile.Limits.ProcessCount)
	}
	if len(profile.Env.Allow) > 0 {
		fmt.Fprintf(a.Stdout, "env.allow   %s\n", strings.Join(profile.Env.Allow, ", "))
	}
	if len(profile.Env.Deny) > 0 {
		fmt.Fprintf(a.Stdout, "env.deny    %s\n", strings.Join(profile.Env.Deny, ", "))
	}
	engine := sandbox.SelectEngine()
	fmt.Fprintf(a.Stdout, "engine      %s\n", engine.Name())
	return nil
}

// SandboxDoctor reports every registered engine's availability.
// JSON path emits a stable {engines, selected} shape — automation
// pipelines that gate on `selected == "noop"` to flag missing
// enforcement can branch on it without parsing the human table.
func (a *App) SandboxDoctor(asJSON bool) error {
	statuses := sandbox.AvailableEngines()
	chosen := sandbox.SelectEngine().Name()

	if asJSON {
		entries := make([]sandboxEngineEntry, 0, len(statuses))
		for _, st := range statuses {
			entries = append(entries, sandboxEngineEntry{
				Name:      st.Name,
				Available: st.Available,
			})
		}
		body, err := json.MarshalIndent(sandboxDoctorJSON{
			Engines:  entries,
			Selected: chosen,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, string(body))
		return nil
	}

	fmt.Fprintf(a.Stdout, "%-16s %s\n", "ENGINE", "AVAILABLE")
	for _, st := range statuses {
		marker := "no"
		if st.Available {
			marker = "yes"
		}
		fmt.Fprintf(a.Stdout, "%-16s %s\n", st.Name, marker)
	}
	fmt.Fprintf(a.Stdout, "\nselected: %s\n", chosen)
	if chosen == "noop" {
		fmt.Fprintln(a.Stdout, "  install bubblewrap (Linux) / sandbox-exec (macOS, built-in) / Docker for real enforcement")
	}
	// Per ADR-020 §Resolved (2026-05-02): for each MISSING engine,
	// surface a multi-line install hint instead of a bare "no".
	// Auto-install would require sudo + silently widen trust —
	// the operator runs the command themselves. sandbox.InstallHint
	// returns "" for engines that have no install path on this OS
	// (noop, irrelevant cross-OS combos), so the loop is a no-op
	// for those. NO sudo driving — instructions only.
	for _, st := range statuses {
		if st.Available {
			continue
		}
		hint := sandbox.InstallHint(runtime.GOOS, st.Name)
		if hint == "" {
			continue
		}
		fmt.Fprintf(a.Stdout, "\n%s missing — install hint:\n", st.Name)
		for _, line := range strings.Split(hint, "\n") {
			fmt.Fprintf(a.Stdout, "  %s\n", line)
		}
	}
	return nil
}

var _ = errors.New // reserved for future verb additions
