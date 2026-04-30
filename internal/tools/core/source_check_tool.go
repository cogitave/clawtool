// Package core — SourceCheck MCP tool.
//
// Mirrors the `clawtool source check [<instance>] [--json]` CLI
// verb (commit ddabc05) on the MCP transport. Returns the same
// {name, ready, missing[]} array shape so MCP-only callers can
// probe whether a source's required env vars resolve via the
// secrets store — without shelling out to the CLI.
//
// Use case: an installer / bootstrap agent (typically running
// as a clawtool MCP client) wants to verify GitHub / Slack /
// Postgres credentials are configured BEFORE issuing dispatch
// commands that depend on the source. The CLI verb requires
// `Bash clawtool source check …` round-trips; the MCP tool
// short-circuits.
//
// Read-only; no side-effects, no auth-sensitive data emitted
// (only env-var NAMES, never values). Operators can call it
// freely.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// sourceCheckMCPEntry is the structured per-source payload.
// Field shape matches the CLI's sourceCheckEntry verbatim
// (commit ddabc05) — that's the documented wire contract.
// Defined here rather than imported because internal/cli is
// not allowed in the layered import graph.
type sourceCheckMCPEntry struct {
	Name    string   `json:"name"`
	Ready   bool     `json:"ready"`
	Missing []string `json:"missing,omitempty"`
}

// sourceCheckResult is the tool's full response: BaseResult
// metadata + the array of per-source entries + a `ready` summary
// flag (true iff every reported source is ready). Operators
// gating on the aggregate state can read `ready` without
// iterating the array; per-source detail lives in entries.
type sourceCheckResult struct {
	BaseResult
	Ready   bool                  `json:"ready"`
	Entries []sourceCheckMCPEntry `json:"entries"`
}

// Render is the human-readable banner. Mirrors the CLI's table
// shape for consistency.
func (r sourceCheckResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	if len(r.Entries) == 0 {
		b.WriteString(r.HeaderLine("(no sources configured)"))
		b.WriteByte('\n')
		b.WriteString(r.FooterLine())
		return b.String()
	}
	b.WriteString(r.HeaderLine(fmt.Sprintf("%d source(s) probed", len(r.Entries))))
	b.WriteByte('\n')
	for _, e := range r.Entries {
		if e.Ready {
			fmt.Fprintf(&b, "  %-30s ✓ ready\n", e.Name)
		} else {
			fmt.Fprintf(&b, "  %-30s ✗ missing: %s\n", e.Name, strings.Join(e.Missing, ", "))
		}
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine())
	return b.String()
}

// RegisterSourceCheckTool adds the `SourceCheck` MCP tool to s.
// Wired from manifest.go via registry.ToolSpec.Register so the
// surface-drift test catches missing routing-rows / allowed-tools
// entries automatically.
func RegisterSourceCheckTool(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"SourceCheck",
			mcp.WithDescription(
				"Probe configured MCP source instances to see whether each "+
					"one's required env vars resolve via the secrets store "+
					"(or process env). Returns {entries: [{name, ready, "+
					"missing[]}], ready: bool}. Pass `instance` to filter "+
					"to one source; omit to probe every configured source. "+
					"Same shape as `clawtool source check [<instance>] "+
					"--json`. Read-only; never emits secret values, only "+
					"the names of env vars that didn't resolve.",
			),
			mcp.WithString("instance",
				mcp.Description("Optional source instance name (e.g. 'github'). When empty or missing, probes every configured source.")),
		),
		runSourceCheck,
	)
}

// runSourceCheck is the MCP handler. Mirrors runSourceCheck in
// internal/cli/source.go — same loading + Resolve logic, same
// classification, just packaged as a structured tool result
// instead of a process exit code.
func runSourceCheck(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := sourceCheckResult{
		BaseResult: BaseResult{Operation: "SourceCheck", Engine: "sources"},
	}
	defer func() { out.DurationMs = time.Since(start).Milliseconds() }()

	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = fmt.Sprintf("load config: %s", err.Error())
		return resultOf(out), nil
	}

	want := strings.TrimSpace(req.GetString("instance", ""))
	var names []string
	if want != "" {
		if _, ok := cfg.Sources[want]; !ok {
			out.ErrorReason = fmt.Sprintf("instance %q not configured", want)
			return resultOf(out), nil
		}
		names = []string{want}
	} else {
		names = make([]string, 0, len(cfg.Sources))
		for n := range cfg.Sources {
			names = append(names, n)
		}
		sort.Strings(names)
	}

	store, _ := secrets.LoadOrEmpty(secrets.DefaultPath())

	allReady := true
	out.Entries = make([]sourceCheckMCPEntry, 0, len(names))
	for _, name := range names {
		src := cfg.Sources[name]
		_, missing := store.Resolve(name, src.Env)
		ready := len(missing) == 0
		if !ready {
			allReady = false
		}
		out.Entries = append(out.Entries, sourceCheckMCPEntry{
			Name:    name,
			Ready:   ready,
			Missing: missing,
		})
	}
	out.Ready = allReady && len(out.Entries) > 0
	return resultOf(out), nil
}
