// Package core — Bridge* MCP tools (ADR-014 Phase 1).
//
// Mirrors `clawtool bridge add/list/remove/upgrade` over MCP so a
// model can install / inspect / uninstall bridges mid-conversation
// ("kanka gemini bridge'i kur"). Same dispatch path as the CLI —
// both end up calling setup.Apply on the bridge's recipe.
package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/setup"
	"github.com/cogitave/clawtool/internal/setup/recipes/bridges"

	// Blank import: ensures bridges/init() registers with the recipe
	// registry before any tool handler runs (matches the pattern in
	// recipes_tool.go).
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ── shapes ─────────────────────────────────────────────────────────

type bridgeListResult struct {
	BaseResult
	Bridges []bridgeInfo `json:"bridges"`
}

type bridgeInfo struct {
	Family      string `json:"family"`
	Recipe      string `json:"recipe"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	Description string `json:"description"`
	Upstream    string `json:"upstream"`
}

func (r bridgeListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d bridge(s) registered\n\n", len(r.Bridges))
	fmt.Fprintf(&b, "  %-12s %-12s %s\n", "FAMILY", "STATUS", "DESCRIPTION")
	for _, br := range r.Bridges {
		fmt.Fprintf(&b, "  %-12s %-12s %s\n", br.Family, br.Status, br.Description)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type bridgeAddResult struct {
	BaseResult
	Family      string   `json:"family"`
	Recipe      string   `json:"recipe"`
	Skipped     bool     `json:"skipped,omitempty"`
	SkipReason  string   `json:"skip_reason,omitempty"`
	Installed   []string `json:"installed_prereqs,omitempty"`
	ManualHints []string `json:"manual_prereqs,omitempty"`
	VerifyOK    bool     `json:"verify_ok"`
	VerifyError string   `json:"verify_error,omitempty"`
}

func (r bridgeAddResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Family)
	}
	if r.Skipped {
		return fmt.Sprintf("↷ skipped %s — %s", r.Family, r.SkipReason)
	}
	verb := "installed"
	if !r.VerifyOK {
		verb = "installed (verify failed)"
	}
	extras := []string{r.Recipe}
	if !r.VerifyOK {
		extras = append(extras, "verify: "+r.VerifyError)
	}
	for _, h := range r.ManualHints {
		extras = append(extras, "manual prereq: "+h)
	}
	for _, i := range r.Installed {
		extras = append(extras, "installed: "+i)
	}
	return r.SuccessLine(verb+" "+r.Family+" bridge", extras...)
}

type bridgeRemoveResult struct {
	BaseResult
	Family string `json:"family"`
	Recipe string `json:"recipe"`
	Note   string `json:"note"`
}

func (r bridgeRemoveResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Family)
	}
	return r.SuccessLine(r.Note)
}

// ── registration ───────────────────────────────────────────────────

// RegisterBridgeTools adds BridgeList/Add/Remove/Upgrade to s.
func RegisterBridgeTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"BridgeList",
			mcp.WithDescription(
				"List the bridges clawtool can install (codex / opencode / gemini), "+
					"with current install state. Per ADR-014: a 'bridge' is the "+
					"connector clawtool installs to talk to another agent CLI; "+
					"distinct from 'agents' (instance management) and 'recipe' "+
					"(generic project-setup wizard).",
			),
		),
		runBridgeList,
	)
	s.AddTool(
		mcp.NewTool(
			"BridgeAdd",
			mcp.WithDescription(
				"Install the canonical bridge for the given family. Wraps the "+
					"upstream's published Claude Code plugin (codex-plugin-cc, "+
					"gemini-plugin-cc) or built-in subcommand (opencode acp). "+
					"Idempotent — re-running on an already-installed bridge "+
					"short-circuits to verify. Per ADR-014's curated-catalog "+
					"discipline there is no plugin-shopping parameter; power "+
					"users override via [bridge.<family>].plugin in config.toml.",
			),
			mcp.WithString("family", mcp.Required(),
				mcp.Description("Bridge family: codex | opencode | gemini.")),
		),
		runBridgeAdd,
	)
	s.AddTool(
		mcp.NewTool(
			"BridgeRemove",
			mcp.WithDescription(
				"Remove the bridge for the given family. v0.10 surfaces this as a "+
					"manual hint (claude plugin remove); fully automated uninstall "+
					"lands in v0.10.x.",
			),
			mcp.WithString("family", mcp.Required(),
				mcp.Description("Bridge family: codex | opencode | gemini.")),
		),
		runBridgeRemove,
	)
	s.AddTool(
		mcp.NewTool(
			"BridgeUpgrade",
			mcp.WithDescription(
				"Re-run the bridge install for the given family. Idempotent; "+
					"pulls the latest plugin version from the upstream marketplace.",
			),
			mcp.WithString("family", mcp.Required(),
				mcp.Description("Bridge family: codex | opencode | gemini.")),
		),
		runBridgeAdd, // upgrade == idempotent re-install in Phase 1
	)
}

// ── handlers ───────────────────────────────────────────────────────

func runBridgeList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := bridgeListResult{BaseResult: BaseResult{Operation: "BridgeList", Engine: "bridges"}}
	for _, fam := range bridges.Families() {
		r := bridges.LookupByFamily(fam)
		if r == nil {
			continue
		}
		status, detail, _ := r.Detect(ctx, "")
		m := r.Meta()
		out.Bridges = append(out.Bridges, bridgeInfo{
			Family:      fam,
			Recipe:      m.Name,
			Status:      string(status),
			Detail:      detail,
			Description: m.Description,
			Upstream:    m.Upstream,
		})
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

func runBridgeAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	family, err := req.RequireString("family")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: family"), nil
	}
	start := time.Now()
	out := bridgeAddResult{
		BaseResult: BaseResult{Operation: "BridgeAdd", Engine: "bridges"},
		Family:     family,
	}
	r := bridges.LookupByFamily(family)
	if r == nil {
		out.ErrorReason = fmt.Sprintf("unknown family %q", family)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Recipe = r.Meta().Name

	res, applyErr := setup.Apply(ctx, r, setup.ApplyOptions{
		Repo:     "",
		Prompter: setup.AlwaysSkip{},
	})
	out.Skipped = res.Skipped
	out.SkipReason = res.SkipReason
	out.Installed = res.Installed
	out.ManualHints = res.ManualHints
	if res.VerifyErr != nil {
		out.VerifyError = res.VerifyErr.Error()
	} else {
		out.VerifyOK = !res.Skipped
	}
	if applyErr != nil {
		out.ErrorReason = applyErr.Error()
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

func runBridgeRemove(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	family, err := req.RequireString("family")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: family"), nil
	}
	start := time.Now()
	out := bridgeRemoveResult{
		BaseResult: BaseResult{Operation: "BridgeRemove", Engine: "bridges"},
		Family:     family,
	}
	r := bridges.LookupByFamily(family)
	if r == nil {
		out.ErrorReason = fmt.Sprintf("unknown family %q", family)
		out.DurationMs = time.Since(start).Milliseconds()
		return resultOf(out), nil
	}
	out.Recipe = r.Meta().Name
	out.Note = fmt.Sprintf(
		"manual: run `claude plugin remove %s` (clawtool's automated remove ships in v0.10.x)",
		r.Meta().Name,
	)
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}
