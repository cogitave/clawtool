// Package core — Sandbox* MCP tools (ADR-020). v0.18 ships the
// read-only surface (List / Show / Doctor) so models can discover
// the profile catalog and recommend the right one to operators.
// SandboxRun is intentionally CLI-only — letting a model spawn
// sandboxed commands has the wrong default.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
		b.WriteString("(no sandbox profiles configured — see ADR-020)\n")
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

func RegisterSandboxTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"SandboxList",
			mcp.WithDescription(
				"List configured sandbox profiles (ADR-020). Returns each "+
					"profile's name + description and the engine that would "+
					"run it on this host (bwrap / sandbox-exec / docker / noop).",
			),
		),
		runSandboxList,
	)
	s.AddTool(
		mcp.NewTool(
			"SandboxShow",
			mcp.WithDescription(
				"Render a parsed sandbox profile — paths, network policy, "+
					"limits, env policy. Use before recommending a profile to "+
					"the operator so the constraints are explicit.",
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
				"Report which sandbox engines are available on this host "+
					"(bwrap, sandbox-exec, docker). Use to recommend the right "+
					"engine to install when none is available.",
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
