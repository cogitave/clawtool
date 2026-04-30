package setuptools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// onboardWizardResult is what the chat path returns after running
// the non-interactive subset of `clawtool onboard`.
type onboardWizardResult struct {
	AgentFamily   string `json:"agent_family"`
	Telemetry     bool   `json:"telemetry"`
	MarkerWritten bool   `json:"marker_written"`
	NextAction    string `json:"next_action"`
	ErrorReason   string `json:"error_reason,omitempty"`
}

// validAgentFamilies enumerates the strings the wizard's
// PrimaryCLI select widget accepts, plus "none" / empty for the
// "decide later" case. Mirrors internal/cli/onboard.go's
// primaryCLIOptions list — kept literal here so a future addition
// in onboard.go forces an explicit update on the chat path too.
var validAgentFamilies = map[string]bool{
	"":         true, // "decide later" — explicit empty
	"none":     true, // explicit "decide later" alias
	"claude":   true,
	"codex":    true,
	"gemini":   true,
	"opencode": true,
	"hermes":   true,
}

// RegisterOnboardWizard wires the OnboardWizard MCP tool to s.
func RegisterOnboardWizard(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"OnboardWizard",
			mcp.WithDescription(
				"Run the non-interactive subset of `clawtool onboard` from chat: persist the agent-family default, record the telemetry preference, and write the onboarded marker. The interactive TUI parts (bridge installs, daemon ensure, MCP host registration) stay CLI-only — operators run `clawtool onboard` for those.",
			),
			mcp.WithString("agent_family",
				mcp.Description("Operator's primary CLI family. One of: claude / codex / gemini / opencode / hermes / none. Empty or 'none' = decide later.")),
			mcp.WithBoolean("telemetry_opt_in",
				mcp.Description("Anonymous-telemetry preference. Default true (pre-1.0 default = on; matches the wizard form).")),
			mcp.WithBoolean("non_interactive",
				mcp.Description("Must be true. The chat invocation can't drive a TUI — pass true to confirm. Absent / false returns an error pointing the operator to `clawtool onboard`.")),
		),
		runOnboardWizard,
	)
}

// runOnboardWizard is the handler. Three side effects, in order
// (each guarded; partial state is recoverable):
//  1. Update config.toml's [telemetry] section with the operator's
//     preference (atomic write via Config.Save).
//  2. Persist the agent-family default. There's no first-class
//     home for it on disk yet — we use config.toml's
//     [profile.active] field as a carrier so the value round-trips
//     and `clawtool onboard` can pick it up later. Empty value
//     means "leave as-is" so we don't clobber an existing profile.
//  3. Write ~/.config/clawtool/.onboarded so IsOnboarded() flips
//     to true and the SessionStart nudge stops firing.
func runOnboardWizard(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	// Required gate — the entry-point precondition. The point of
	// the flag is to make the caller aware that the chat path is
	// a SUBSET of the wizard, not the same thing.
	nonInteractive := false
	if v, ok := args["non_interactive"]; ok {
		if b, ok := v.(bool); ok {
			nonInteractive = b
		}
	}
	if !nonInteractive {
		return mcp.NewToolResultError(
			"OnboardWizard: pass non_interactive=true to confirm. The interactive wizard (bridge installs, MCP host registration, daemon ensure) can't run from chat — run `clawtool onboard` from a terminal for that flow.",
		), nil
	}

	family := strings.TrimSpace(req.GetString("agent_family", ""))
	if !validAgentFamilies[family] {
		return mcp.NewToolResultError(fmt.Sprintf(
			"OnboardWizard: unknown agent_family %q — pick one of claude / codex / gemini / opencode / hermes / none.", family,
		)), nil
	}
	if family == "none" {
		// Normalise to empty so the persisted shape stays clean.
		family = ""
	}

	// Telemetry default = true (pre-1.0 default = on).
	telemetry := true
	if v, ok := args["telemetry_opt_in"]; ok {
		if b, ok := v.(bool); ok {
			telemetry = b
		}
	}

	out := onboardWizardResult{
		AgentFamily: family,
		Telemetry:   telemetry,
		NextAction:  "Call InitApply to install core defaults into the repo.",
	}

	// 1 + 2: persist preferences via config.toml.
	cfgPath := config.DefaultPath()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		out.ErrorReason = fmt.Sprintf("load config: %v", err)
		return resultOfJSON("OnboardWizard", out)
	}
	cfg.Telemetry.Enabled = telemetry
	if family != "" {
		// Profile.Active is the only existing carrier for an
		// operator-level "primary" preference. Mark it with the
		// `family:<name>` prefix so we don't collide with a
		// real profile name an operator might have set; the
		// next `clawtool onboard` run (or the parallel
		// init-context-summary branch) can promote this to a
		// dedicated field without an on-disk migration.
		cfg.Profile.Active = "family:" + family
	}
	if err := cfg.Save(cfgPath); err != nil {
		out.ErrorReason = fmt.Sprintf("save config: %v", err)
		return resultOfJSON("OnboardWizard", out)
	}

	// 3: marker write. Same path internal/cli/onboard.go's
	// writeOnboardedMarker uses (~/.config/clawtool/.onboarded).
	// We re-implement it here rather than importing internal/cli
	// because that package imports internal/tools indirectly via
	// App's tool wiring; an internal/cli import here would form
	// a cycle. The path is stable and one place — drift would
	// surface as IsOnboarded() returning false in spite of a
	// successful chat onboard, which the test pins.
	markerPath := filepath.Join(xdg.ConfigDir(), ".onboarded")
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
		out.ErrorReason = fmt.Sprintf("mkdir config dir: %v", err)
		return resultOfJSON("OnboardWizard", out)
	}
	stamp := time.Now().UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(markerPath, []byte(stamp), 0o644); err != nil {
		out.ErrorReason = fmt.Sprintf("write marker: %v", err)
		return resultOfJSON("OnboardWizard", out)
	}
	out.MarkerWritten = true

	return resultOfJSON("OnboardWizard", out)
}
