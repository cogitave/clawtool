package cli

import (
	"fmt"
	"strings"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/version"
)

// preV1Locked reports whether telemetry opt-out is blocked at this
// version. ADR-030 + operator policy (2026-04-29): pre-v1.0.0,
// telemetry stays on — the data we need to diagnose install /
// onboard / dispatch funnels is exactly what gets hidden the
// moment the first user opts out, and we have no real signal yet
// that the project is finished enough to reduce data collection.
// The lock disappears the moment we tag v1.0.0, at which point
// `clawtool telemetry off` resumes working as a normal opt-out.
//
// Detection: version.Resolved() returns "vX.Y.Z" or "X.Y.Z-…" or
// "(devel)" / "(unknown)" for hand-built binaries. We only lock
// when we can prove the major version is 0; everything else
// (dev builds, unparseable strings) falls through to the legacy
// behaviour so a developer testing changes locally can still
// toggle the flag.
func preV1Locked() bool {
	v := strings.TrimPrefix(version.Resolved(), "v")
	if v == "" || strings.HasPrefix(v, "(") {
		return false // dev build — let the developer flip the flag
	}
	// Parse the major version: "0.22.35-15-g..." → "0".
	dot := strings.IndexByte(v, '.')
	if dot < 1 {
		return false
	}
	major := v[:dot]
	return major == "0"
}

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
	if preV1Locked() {
		fmt.Fprintln(a.Stdout, "policy:    opt-out locked until v1.0.0 (pre-1.0 development cycle)")
	}
	return 0
}

func (a *App) telemetrySet(enabled bool) int {
	// Pre-v1.0.0: opt-out is locked. The data hidden by the first
	// opt-out is exactly what we need to validate the install /
	// onboard / dispatch funnels are working — until v1.0.0, the
	// project is too early to reduce data collection.
	// Concretely: telemetry stays on, no override. The lock
	// disappears the moment we tag v1.0.0 and the major version
	// flips to 1+; this branch is then skipped and `telemetry
	// off` resumes working as a normal opt-out.
	if !enabled && preV1Locked() {
		fmt.Fprintf(a.Stderr,
			"clawtool telemetry: opt-out is locked until v1.0.0.\n"+
				"  Anonymous telemetry stays on through the pre-1.0 cycle so we can\n"+
				"  diagnose install / onboard / dispatch funnel breaks. The payload is\n"+
				"  strictly allow-listed — command + version + duration + exit code +\n"+
				"  agent family + recipe / engine / bridge name. Never prompts, paths,\n"+
				"  secrets, env values. Source: internal/telemetry/telemetry.go\n"+
				"\n"+
				"  When we ship v1.0.0, `clawtool telemetry off` resumes working as a\n"+
				"  normal opt-out. Until then, this verb is a no-op refusal.\n")
		return 1
	}
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
