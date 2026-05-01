// Package core — Portal* MCP tools (ADR-018). Read-only surface in
// v0.16.1: PortalList, PortalUse, PortalWhich, PortalUnset,
// PortalRemove, plus a deferred-feature stub for PortalAsk so the
// shape is discoverable before the v0.16.2 CDP driver lands.
//
// PortalAdd is intentionally CLI-only — it spawns $EDITOR which
// has no meaning in an MCP context. Operators add portals from the
// terminal; agents discover and use them through MCP.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/portal"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// portalListResult lists configured portals + auth-cookie names.
type portalListResult struct {
	BaseResult
	Portals []portalRow `json:"portals"`
}

type portalRow struct {
	Name            string   `json:"name"`
	BaseURL         string   `json:"base_url"`
	StartURL        string   `json:"start_url,omitempty"`
	AuthCookieNames []string `json:"auth_cookie_names,omitempty"`
}

func (r portalListResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	if len(r.Portals) == 0 {
		return r.SuccessLine("(no portals configured — clawtool portal add <name>)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d portal(s)\n\n", len(r.Portals))
	fmt.Fprintf(&b, "  %-22s %-46s %s\n", "NAME", "BASE URL", "AUTH COOKIES")
	for _, p := range r.Portals {
		auth := strings.Join(p.AuthCookieNames, ",")
		if auth == "" {
			auth = "(none declared)"
		}
		fmt.Fprintf(&b, "  %-22s %-46s %s\n", p.Name, p.BaseURL, auth)
	}
	b.WriteString("\n")
	b.WriteString(r.FooterLine())
	return b.String()
}

type portalSimpleResult struct {
	BaseResult
	Detail string `json:"detail,omitempty"`
}

func (r portalSimpleResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	return r.SuccessLine(r.Detail)
}

// RegisterPortalTools wires the Portal* MCP surface. Always registered;
// missing config produces empty results, not boot failure.
func RegisterPortalTools(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"PortalList",
			mcp.WithDescription(
				"List configured web-UI portals. A portal is a named, "+
					"authenticated browser target with selectors and a "+
					"'response done' predicate — `clawtool portal ask "+
					"<name> \"prompt\"` drives it through Obscura. Returns "+
					"the registry; cookie material lives in secrets.toml "+
					"and never appears in this response.",
			),
		),
		runPortalList,
	)
	s.AddTool(
		mcp.NewTool(
			"PortalWhich",
			mcp.WithDescription(
				"Resolve the sticky-default portal — same precedence chain "+
					"as `clawtool portal which`: CLAWTOOL_PORTAL env > "+
					"sticky default > single-configured fallback.",
			),
		),
		runPortalWhich,
	)
	s.AddTool(
		mcp.NewTool(
			"PortalUse",
			mcp.WithDescription(
				"Pin a sticky-default web-UI portal so subsequent PortalAsk "+
					"calls without an explicit `portal` argument route here. "+
					"Use when the operator says \"use my deepseek portal\" or "+
					"after PortalList shows multiple portals and you need to "+
					"settle on one for the rest of the session. NOT for "+
					"clearing the default — use PortalUnset. Persists to "+
					"$XDG_CONFIG_HOME/clawtool/active_portal; same precedence "+
					"as the CLAWTOOL_PORTAL env var (env wins).",
			),
			mcp.WithString("name", mcp.Required(),
				mcp.Description("Configured portal name.")),
		),
		runPortalUse,
	)
	s.AddTool(
		mcp.NewTool(
			"PortalUnset",
			mcp.WithDescription(
				"Clear the sticky-default web-UI portal pin set by PortalUse. "+
					"Use when the operator wants future PortalAsk calls to "+
					"require an explicit `portal` argument again, or before "+
					"switching to a different default. NOT for removing a "+
					"portal stanza from config.toml — use PortalRemove for "+
					"that. No-op when no sticky default is set.",
			),
		),
		runPortalUnset,
	)
	s.AddTool(
		mcp.NewTool(
			"PortalRemove",
			mcp.WithDescription(
				"Remove a portal stanza from config.toml. Cookies under "+
					"[scopes.\"portal.<name>\"] in secrets.toml are left "+
					"in place — clean manually if no longer needed.",
			),
			mcp.WithString("name", mcp.Required(),
				mcp.Description("Configured portal name.")),
		),
		runPortalRemove,
	)
	s.AddTool(
		mcp.NewTool(
			"PortalAsk",
			mcp.WithDescription(
				"Drive a saved portal with the given prompt and stream "+
					"the response. NB: the CDP driver lands in v0.16.2; "+
					"v0.16.1 returns a deferred-feature error after "+
					"validating the resolved portal so the caller's "+
					"plumbing is testable today.",
			),
			mcp.WithString("portal",
				mcp.Description("Portal name. Empty = sticky default / single configured.")),
			mcp.WithString("prompt", mcp.Required(),
				mcp.Description("Prompt to send through the portal's input selector.")),
			mcp.WithNumber("timeout_ms",
				mcp.Description("Hard deadline for the whole flow. Default 180000.")),
		),
		runPortalAsk,
	)
}

// ── handlers ───────────────────────────────────────────────────────

func runPortalList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := portalListResult{BaseResult: BaseResult{Operation: "PortalList", Engine: "config"}}
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	names := portal.Names(cfg)
	sort.Strings(names)
	for _, n := range names {
		p := cfg.Portals[n]
		out.Portals = append(out.Portals, portalRow{
			Name:            n,
			BaseURL:         p.BaseURL,
			StartURL:        p.StartURL,
			AuthCookieNames: p.AuthCookieNames,
		})
	}
	return resultOf(out), nil
}

func runPortalWhich(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := portalSimpleResult{BaseResult: BaseResult{Operation: "PortalWhich", Engine: "config"}}
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	if len(cfg.Portals) == 0 {
		out.ErrorReason = "no portals configured"
		return resultOf(out), nil
	}
	if env := strings.TrimSpace(os.Getenv("CLAWTOOL_PORTAL")); env != "" {
		if _, ok := cfg.Portals[env]; !ok {
			out.ErrorReason = fmt.Sprintf("CLAWTOOL_PORTAL=%q not in registry", env)
			return resultOf(out), nil
		}
		out.Detail = env + " (env)"
		return resultOf(out), nil
	}
	if name := readPortalStickyShared(); name != "" {
		if _, ok := cfg.Portals[name]; !ok {
			out.ErrorReason = fmt.Sprintf("sticky portal %q is not in registry", name)
			return resultOf(out), nil
		}
		out.Detail = name + " (sticky)"
		return resultOf(out), nil
	}
	if len(cfg.Portals) == 1 {
		for n := range cfg.Portals {
			out.Detail = n + " (single configured)"
			return resultOf(out), nil
		}
	}
	out.ErrorReason = "portal ambiguous — set CLAWTOOL_PORTAL or run `clawtool portal use <name>`"
	return resultOf(out), nil
}

func runPortalUse(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := portalSimpleResult{BaseResult: BaseResult{Operation: "PortalUse", Engine: "config"}}
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: name"), nil
	}
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	if _, ok := cfg.Portals[name]; !ok {
		out.ErrorReason = fmt.Sprintf("portal %q not in registry", name)
		return resultOf(out), nil
	}
	if err := writePortalStickyShared(name); err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Detail = "active portal → " + name
	return resultOf(out), nil
}

func runPortalUnset(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := portalSimpleResult{BaseResult: BaseResult{Operation: "PortalUnset", Engine: "config"}}
	if err := clearPortalStickyShared(); err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Detail = "sticky portal cleared"
	return resultOf(out), nil
}

func runPortalRemove(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := portalSimpleResult{BaseResult: BaseResult{Operation: "PortalRemove", Engine: "config"}}
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: name"), nil
	}
	cfgPath := config.DefaultPath()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	if _, ok := cfg.Portals[name]; !ok {
		out.ErrorReason = fmt.Sprintf("portal %q not found", name)
		return resultOf(out), nil
	}
	if err := config.RemovePortalBlock(cfgPath, name); err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Detail = fmt.Sprintf("removed %s (cookies under [scopes.%q] left in secrets.toml)", name, portal.SecretsScopePrefix+name)
	return resultOf(out), nil
}

func runPortalAsk(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out := portalSimpleResult{BaseResult: BaseResult{Operation: "PortalAsk", Engine: "portal"}}
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: prompt"), nil
	}
	name := strings.TrimSpace(req.GetString("portal", ""))
	timeoutMs := int(req.GetFloat("timeout_ms", 0))

	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	if name == "" {
		if env := strings.TrimSpace(os.Getenv("CLAWTOOL_PORTAL")); env != "" {
			name = env
		} else if s := readPortalStickyShared(); s != "" {
			name = s
		} else if len(cfg.Portals) == 1 {
			for n := range cfg.Portals {
				name = n
				break
			}
		} else {
			out.ErrorReason = "portal ambiguous — pass `portal` or run `clawtool portal use <name>`"
			return resultOf(out), nil
		}
	}
	p, ok := cfg.Portals[name]
	if !ok {
		out.ErrorReason = fmt.Sprintf("portal %q not in registry", name)
		return resultOf(out), nil
	}
	if err := portal.Validate(name, p); err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	if timeoutMs > 0 {
		p.TimeoutMs = timeoutMs
	}
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		out.ErrorReason = fmt.Sprintf("load secrets: %v", err)
		return resultOf(out), nil
	}
	rawCookies, _ := store.Get(p.SecretsScope, "cookies_json")
	cookies, err := portal.ParseCookies(rawCookies)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	// Caller's ctx may be short-lived (MCP request); enforce the
	// portal's own timeout while still honouring upstream cancel.
	askCtx := ctx
	if p.TimeoutMs > 0 {
		var cancel context.CancelFunc
		askCtx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	text, err := portal.Ask(askCtx, p, prompt, portal.AskOptions{Cookies: cookies})
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Detail = text
	return resultOf(out), nil
}

// RegisterPortalAliases scans cfg.Portals and binds a thin wrapper
// `<name>__ask` for each one. Same wire-naming convention as
// internal/sources/manager.go aggregation. Each alias forwards to
// PortalAsk with the portal name pre-bound, so the calling model
// can do `my_deepseek__ask({"prompt":"..."})` without remembering
// the generic shape.
func RegisterPortalAliases(s *server.MCPServer, cfg config.Config) {
	for name, p := range cfg.Portals {
		if err := portal.Validate(name, p); err != nil {
			// Skip invalid entries — surface the diagnostic via
			// PortalList (which doesn't filter), keep boot quiet.
			continue
		}
		aliasName := name + "__ask"
		boundName := name
		s.AddTool(
			mcp.NewTool(
				aliasName,
				mcp.WithDescription(fmt.Sprintf(
					"Ask the %q portal (%s). Thin wrapper over PortalAsk; "+
						"selectors / cookies / predicates resolved from "+
						"saved config.",
					name, p.BaseURL)),
				mcp.WithString("prompt", mcp.Required(),
					mcp.Description("Prompt to send through the portal's input selector.")),
				mcp.WithNumber("timeout_ms",
					mcp.Description("Override the portal's configured timeout for this call.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				prompt, err := req.RequireString("prompt")
				if err != nil {
					return mcp.NewToolResultError("missing required argument: prompt"), nil
				}
				return runPortalAskBound(ctx, boundName, prompt, int(req.GetFloat("timeout_ms", 0)))
			},
		)
	}
}

// runPortalAskBound is the shared core both PortalAsk and per-portal
// aliases route through. Pulled out so a typo doesn't cause the two
// code paths to drift.
func runPortalAskBound(ctx context.Context, name, prompt string, timeoutMs int) (*mcp.CallToolResult, error) {
	out := portalSimpleResult{BaseResult: BaseResult{Operation: "PortalAsk", Engine: "portal"}}
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	p, ok := cfg.Portals[name]
	if !ok {
		out.ErrorReason = fmt.Sprintf("portal %q no longer in registry — restart serve to refresh aliases", name)
		return resultOf(out), nil
	}
	if err := portal.Validate(name, p); err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	if timeoutMs > 0 {
		p.TimeoutMs = timeoutMs
	}
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		out.ErrorReason = fmt.Sprintf("load secrets: %v", err)
		return resultOf(out), nil
	}
	rawCookies, _ := store.Get(p.SecretsScope, "cookies_json")
	cookies, err := portal.ParseCookies(rawCookies)
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	askCtx := ctx
	if p.TimeoutMs > 0 {
		var cancel context.CancelFunc
		askCtx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	text, err := portal.Ask(askCtx, p, prompt, portal.AskOptions{Cookies: cookies})
	if err != nil {
		out.ErrorReason = err.Error()
		return resultOf(out), nil
	}
	out.Detail = text
	return resultOf(out), nil
}

// ── sticky helpers (shared with internal/cli/portal.go) ───────────

func portalStickyFileShared() string {
	return filepath.Join(xdg.ConfigDir(), "active_portal")
}

func readPortalStickyShared() string {
	b, err := os.ReadFile(portalStickyFileShared())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writePortalStickyShared(name string) error {
	return atomicfile.WriteFileMkdir(portalStickyFileShared(), []byte(strings.TrimSpace(name)+"\n"), 0o644, 0o755)
}

func clearPortalStickyShared() error {
	err := os.Remove(portalStickyFileShared())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
