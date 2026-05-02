// Package core — Sandbox* MCP tools (ADR-020). v0.18 shipped the
// read-only surface (List / Show / Doctor); ADR-020 §"MCP-side
// SandboxRun" (resolved post-v0.22) reverses the original
// "no-MCP" stance and adds SandboxRun so chat-driven callers
// (Claude / Codex / Gemini) can run a one-shot command inside a
// named profile without dropping the operator to a shell.
//
// SandboxRun is the chat-side analogue of `clawtool sandbox run`
// — both delegate into sandbox.RunOneShot so the wire shape +
// engine wrap ordering stay identical across surfaces.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/sandbox"
)

type sandboxListResult struct {
	BaseResult
	Profiles []sandboxListEntry `json:"profiles"`
	Engine   string             `json:"engine"`
}

type sandboxListEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (r sandboxListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	if len(r.Profiles) == 0 {
		b.WriteString("(no sandbox profiles configured — see docs/sandbox.md)\n")
	} else {
		fmt.Fprintf(&b, "%d profile(s) (engine: %s)\n\n", len(r.Profiles), r.Engine)
		fmt.Fprintf(&b, "  %-28s %s\n", "PROFILE", "DESCRIPTION")
		for _, p := range r.Profiles {
			fmt.Fprintf(&b, "  %-28s %s\n", p.Name, p.Description)
		}
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type sandboxDoctorResult struct {
	BaseResult
	Engines  []sandbox.EngineStatus `json:"engines"`
	Selected string                 `json:"selected"`
}

func (r sandboxDoctorResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s %s\n", "ENGINE", "AVAILABLE")
	for _, st := range r.Engines {
		marker := "no"
		if st.Available {
			marker = "yes"
		}
		fmt.Fprintf(&b, "%-16s %s\n", st.Name, marker)
	}
	fmt.Fprintf(&b, "\nselected: %s\n", r.Selected)
	if r.Selected == "noop" {
		b.WriteString("  install bubblewrap (Linux) / sandbox-exec (macOS, built-in) / Docker for real enforcement\n")
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

type sandboxShowResult struct {
	BaseResult
	Profile *sandbox.Profile `json:"profile"`
	Engine  string           `json:"engine"`
}

func (r sandboxShowResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	if r.Profile == nil {
		return r.SuccessLine("(profile not found)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "name        %s\n", r.Profile.Name)
	if r.Profile.Description != "" {
		fmt.Fprintf(&b, "description %s\n", r.Profile.Description)
	}
	fmt.Fprintf(&b, "engine      %s\n", r.Engine)
	for _, p := range r.Profile.Paths {
		fmt.Fprintf(&b, "  %s   %s\n", p.Mode, p.Path)
	}
	fmt.Fprintf(&b, "network     %s\n", r.Profile.Network.Mode)
	for _, host := range r.Profile.Network.Allow {
		fmt.Fprintf(&b, "  allow %s\n", host)
	}
	if r.Profile.Limits.Timeout > 0 {
		fmt.Fprintf(&b, "timeout     %s\n", r.Profile.Limits.Timeout)
	}
	if r.Profile.Limits.MemoryBytes > 0 {
		fmt.Fprintf(&b, "memory      %d bytes\n", r.Profile.Limits.MemoryBytes)
	}
	b.WriteString(r.FooterLine())
	return b.String()
}

// sandboxRunResult is the wire envelope SandboxRun emits. The
// runner-side fields (stdout/stderr/exit_code/timed_out/profile)
// are flattened onto the result so MCP callers see them at the
// top level — embedding sandbox.RunResult would collide with
// BaseResult's `engine` json tag. The "engine" wire field comes
// from the embedded BaseResult; "profile" is named alongside.
type sandboxRunResult struct {
	BaseResult
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
	TimedOut bool     `json:"timed_out"`
	Profile  string   `json:"profile"`
}

// applyRun copies the runner's structured RunResult onto the MCP
// envelope. Centralised so the field-by-field flatten happens in
// one place — adding a field on sandbox.RunResult only requires
// one edit here.
func (r *sandboxRunResult) applyRun(rr sandbox.RunResult) {
	r.Stdout = rr.Stdout
	r.Stderr = rr.Stderr
	r.ExitCode = rr.ExitCode
	r.TimedOut = rr.TimedOut
	r.Profile = rr.Profile
	if rr.Engine != "" {
		r.Engine = rr.Engine
	}
}

func (r sandboxRunResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Command)
	}
	var b strings.Builder
	displayCmd := r.Command
	if len(r.Args) > 0 {
		displayCmd = r.Command + " " + strings.Join(r.Args, " ")
	}
	fmt.Fprintf(&b, "$ [%s/%s] %s\n", r.Profile, r.Engine, displayCmd)
	if r.Stdout != "" {
		b.WriteString(strings.TrimRight(r.Stdout, "\n"))
		b.WriteByte('\n')
	}
	if r.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(strings.TrimRight(r.Stderr, "\n"))
		b.WriteByte('\n')
	}
	if r.Stdout == "" && r.Stderr == "" {
		b.WriteString("(no output)\n")
	}
	extras := []string{
		fmt.Sprintf("exit %d", r.ExitCode),
		fmt.Sprintf("profile: %s", r.Profile),
		fmt.Sprintf("engine: %s", r.Engine),
	}
	if r.TimedOut {
		extras = append(extras, "TIMED OUT")
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// sandboxRunDefaultTimeoutMs / Max bound the timeout_ms knob.
// Default = 60_000 to match `clawtool sandbox run`'s default;
// max = 600_000 mirrors the Bash tool (10 minutes is plenty for a
// chat-driven one-off; longer runs belong in `Bash background=true`).
const (
	sandboxRunDefaultTimeoutMs = 60_000
	sandboxRunMaxTimeoutMs     = 600_000
)

func RegisterSandboxTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"SandboxRun",
			mcp.WithDescription(
				"Run a one-shot command inside a named sandbox profile "+
					"(bwrap / sandbox-exec / docker / custom). Use when "+
					"invoking untrusted code, running shell commands "+
					"with restricted FS / network access, or testing a "+
					"recipe in isolation. NOT for ambient long-running "+
					"processes — use Bash background=true for those. "+
					"Args: profile name, command, optional args + stdin "+
					"+ timeout. Returns: structured "+
					"stdout/stderr/exit_code/duration_ms. List profiles "+
					"via SandboxList; preview constraints via SandboxShow.",
			),
			mcp.WithString("profile", mcp.Required(),
				mcp.Description("Sandbox profile name from config.toml (`bwrap` / `sandbox-exec` / `docker` / a custom one). Run SandboxList to enumerate.")),
			mcp.WithString("command", mcp.Required(),
				mcp.Description("Command to run inside the sandbox. Resolved via $PATH inside the engine's view.")),
			mcp.WithArray("args",
				mcp.Description("Argv for the command. Empty = command runs with no extra arguments."),
				mcp.Items(map[string]any{"type": "string"}),
			),
			mcp.WithString("stdin",
				mcp.Description("Stdin payload piped to the child process. Empty = stdin closed.")),
			mcp.WithNumber("timeout_ms",
				mcp.Description("Hard timeout in milliseconds. Default 60000 (1m), max 600000 (10m).")),
		),
		runSandboxRun,
	)
	s.AddTool(
		mcp.NewTool(
			"SandboxList",
			mcp.WithDescription(
				"Enumerate configured sandbox profiles for `clawtool sandbox "+
					"run`. Use when the operator asks \"what sandboxes do I "+
					"have?\" or before recommending a profile for an untrusted "+
					"command. Returns each profile's name + description plus "+
					"the engine that would run it on this host (bwrap / "+
					"sandbox-exec / docker / noop). NOT for showing one "+
					"profile's full constraints — use SandboxShow; NOT for "+
					"checking engine availability — use SandboxDoctor. "+
					"Read-only.",
			),
		),
		runSandboxList,
	)
	s.AddTool(
		mcp.NewTool(
			"SandboxShow",
			mcp.WithDescription(
				"Render one parsed sandbox profile in full — paths (ro/rw), "+
					"network allow-list, limits (memory/timeout), env policy. "+
					"Use BEFORE recommending the profile to the operator so "+
					"the constraints are explicit, or when debugging why a "+
					"sandboxed command can't reach a path. NOT for browsing "+
					"available profiles — use SandboxList. Read-only.",
			),
			mcp.WithString("name", mcp.Required(),
				mcp.Description("Profile name from config.toml.")),
		),
		runSandboxShow,
	)
	s.AddTool(
		mcp.NewTool(
			"SandboxDoctor",
			mcp.WithDescription(
				"Diagnose which sandbox engines are installed on this host "+
					"(bwrap on Linux / sandbox-exec on macOS / docker / noop "+
					"fallback) and which one clawtool would select. Use when "+
					"the operator asks \"is sandboxing working?\" or after "+
					"SandboxList shows engine=noop to recommend the right "+
					"engine to install. NOT for inspecting profile contents — "+
					"use SandboxShow. Read-only.",
			),
		),
		runSandboxDoctor,
	)
}

func runSandboxList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := sandboxListResult{
		BaseResult: BaseResult{Operation: "SandboxList", Engine: "sandbox"},
	}
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	names := make([]string, 0, len(cfg.Sandboxes))
	for n := range cfg.Sandboxes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out.Profiles = append(out.Profiles, sandboxListEntry{
			Name:        n,
			Description: cfg.Sandboxes[n].Description,
		})
	}
	out.Engine = sandbox.SelectEngine().Name()
	return resultOf(out), nil
}

func runSandboxShow(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: name"), nil
	}
	out := sandboxShowResult{
		BaseResult: BaseResult{Operation: "SandboxShow", Engine: "sandbox"},
	}
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	raw, ok := cfg.Sandboxes[name]
	if !ok {
		out.ErrorReason = fmt.Sprintf("profile %q not found", name)
		return resultOf(out), nil
	}
	prof, err := sandbox.ParseProfile(name, raw)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Profile = prof
	out.Engine = sandbox.SelectEngine().Name()
	return resultOf(out), nil
}

func runSandboxDoctor(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := sandboxDoctorResult{
		BaseResult: BaseResult{Operation: "SandboxDoctor", Engine: "sandbox"},
		Engines:    sandbox.AvailableEngines(),
		Selected:   sandbox.SelectEngine().Name(),
	}
	return resultOf(out), nil
}

// sandboxRunner is the function pointer the SandboxRun handler
// dispatches into. Tests swap this with a stub so the assertions
// don't depend on a real bwrap / docker on the runner — the
// handler logic (arg validation, profile lookup, response shape)
// is what we're guarding here.
var sandboxRunner = sandbox.RunOneShot

func runSandboxRun(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	profileName, err := req.RequireString("profile")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: profile"), nil
	}
	command, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: command"), nil
	}

	out := sandboxRunResult{
		BaseResult: BaseResult{Operation: "SandboxRun", Engine: "sandbox"},
		Command:    command,
	}

	// Decode the optional argv slice (mcp-go emits []any).
	if raw, ok := req.GetArguments()["args"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out.Args = append(out.Args, s)
			}
		}
	}
	stdin := req.GetString("stdin", "")

	timeoutMs := int(req.GetFloat("timeout_ms", float64(sandboxRunDefaultTimeoutMs)))
	if timeoutMs <= 0 {
		timeoutMs = sandboxRunDefaultTimeoutMs
	}
	if timeoutMs > sandboxRunMaxTimeoutMs {
		timeoutMs = sandboxRunMaxTimeoutMs
	}

	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	raw, ok := cfg.Sandboxes[profileName]
	if !ok {
		out.ErrorReason = fmt.Sprintf("profile %q not found", profileName)
		return resultOf(out), nil
	}
	prof, err := sandbox.ParseProfile(profileName, raw)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}

	start := time.Now()
	res, err := sandboxRunner(ctx, sandbox.RunRequest{
		Profile: prof,
		Command: command,
		Args:    out.Args,
		Stdin:   stdin,
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
	})
	out.DurationMs = time.Since(start).Milliseconds()
	out.applyRun(res)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	return resultOf(out), nil
}
