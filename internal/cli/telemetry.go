package cli

import (
	"fmt"

	"github.com/cogitave/clawtool/internal/config"
)

// runTelemetry exposes the telemetry opt-in flag as a CLI verb so
// operators can flip it without hand-editing config.toml. The
// onboard wizard's closing line literally tells people "flip it off
// any time with: clawtool telemetry off" — without this dispatcher
// that hint dead-ends in "unknown command".
//
// Verbs:
//
//	clawtool telemetry status   Print current state + the resolved config path.
//	clawtool telemetry on       Set telemetry.enabled = true.
//	clawtool telemetry off      Set telemetry.enabled = false.
//
// The state lives in [telemetry].enabled in the user's config.toml.
// The change takes effect on the next CLI / daemon start (the
// process-local telemetry.Get() client is initialised once at
// startup; we don't re-read mid-flight).
func (a *App) runTelemetry(argv []string) int {
	if len(argv) == 0 || argv[0] == "--help" || argv[0] == "-h" {
		fmt.Fprint(a.Stdout, telemetryUsage)
		if len(argv) == 0 {
			return 2
		}
		return 0
	}
	switch argv[0] {
	case "status":
		return a.telemetryStatus()
	case "on", "enable":
		return a.telemetrySet(true)
	case "off", "disable":
		return a.telemetrySet(false)
	default:
		fmt.Fprintf(a.Stderr, "clawtool telemetry: unknown subcommand %q\n\n%s", argv[0], telemetryUsage)
		return 2
	}
}

func (a *App) telemetryStatus() int {
	path := a.Path()
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool telemetry: %v\n", err)
		return 1
	}
	state := "off"
	if cfg.Telemetry.Enabled {
		state = "on"
	}
	fmt.Fprintf(a.Stdout, "telemetry: %s\nconfig:    %s\n", state, path)
	if cfg.Telemetry.Host != "" {
		fmt.Fprintf(a.Stdout, "host:      %s\n", cfg.Telemetry.Host)
	}
	return 0
}

func (a *App) telemetrySet(enabled bool) int {
	path := a.Path()
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool telemetry: %v\n", err)
		return 1
	}
	if cfg.Telemetry.Enabled == enabled {
		state := "off"
		if enabled {
			state = "on"
		}
		fmt.Fprintf(a.Stdout, "telemetry already %s (no change)\n", state)
		return 0
	}
	cfg.Telemetry.Enabled = enabled
	if err := cfg.Save(path); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool telemetry: %v\n", err)
		return 1
	}
	state := "off"
	if enabled {
		state = "on"
	}
	fmt.Fprintf(a.Stdout, "✓ telemetry %s (takes effect on next CLI / daemon start)\n", state)
	return 0
}

const telemetryUsage = `Usage:
  clawtool telemetry status   Show whether anonymous telemetry is enabled.
  clawtool telemetry on       Enable telemetry. (Allow-list event payload —
                              command + version + duration + exit code +
                              agent family + recipe / engine / bridge name.
                              Never prompts, paths, secrets, env values.)
  clawtool telemetry off      Disable telemetry. Process-local clients keep
                              their initial state until restart.
`
