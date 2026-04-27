// Package server — surface drift detection.
//
// The clawtool plugin lives across four planes (per
// docs/feature-shipping-contract.md): MCP tool registration,
// marketplace surface (commands/ + plugin.json), agent routing
// bias (skills/clawtool/SKILL.md), and product docs (README).
// A new feature ships when ALL four planes update; absence on
// any plane is a regression.
//
// This test is the foundation of Codex's "Tool Manifest Registry"
// recommendation (BIAM task a3ef5af9 — top-1 ROI refactor). The
// full registry refactor is deferred — this drift detector is the
// minimum viable check-surface invariant: every slash command
// referenced from commands/ must correspond to a real MCP tool,
// and every shipped tool must have a SKILL.md routing-map row.
//
// When this test fails, the fix is mechanical: add the missing
// row OR explicitly allow-list the gap with a justification in
// the surfaceAllowlist below.

package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/tools/core"
)

// surfaceAllowlist holds tool names that are intentionally
// surface-incomplete. Each entry must include a one-line reason
// so the next reviewer understands why the gap is acceptable
// rather than a bug.
var surfaceAllowlist = map[string]string{
	// Multi-agent dispatch surface — these don't get slash
	// commands because they're agent-facing primitives, not user
	// verbs. SendMessage gets one via /clawtool-send (future).
	"AgentList":  "agent-facing primitive; no user verb",
	"TaskGet":    "agent-facing primitive",
	"TaskWait":   "agent-facing primitive",
	"TaskList":   "agent-facing primitive",
	"TaskNotify": "agent-facing primitive (fan-in completion push)",
	"BashOutput": "companion to Bash background mode; agent-facing",
	"BashKill":   "companion to Bash background mode; agent-facing",
	"RulesCheck": "agent-facing primitive; rules.toml is the user surface",

	// Sourced/aggregated tools land per-source under wire names
	// like `<instance>__<tool>` — they don't have plugin slash
	// commands by design.

	// Browser/Portal tools have no slash commands today; future
	// /clawtool-portal-add lives in cli, not commands/. Track:
	"BrowserFetch":  "no /clawtool-browser-fetch; reach via Agent skill",
	"BrowserScrape": "no /clawtool-browser-scrape; reach via Agent skill",
	"PortalAsk":     "addressable via per-portal `<name>__ask` aliases",
	"PortalUse":     "CLI-only verb (clawtool portal use)",
	"PortalUnset":   "CLI-only verb",
	"PortalList":    "CLI-only verb (clawtool portal list)",
	"PortalWhich":   "CLI-only verb",
	"PortalRemove":  "CLI-only verb",

	// Recipe / Bridge / Verify / Mcp* / Sandbox* / SemanticSearch
	// have CLI verbs (`clawtool recipe`, `clawtool bridge`, etc.)
	// not slash commands.
	"RecipeList":     "CLI-only verb (clawtool recipe list)",
	"RecipeStatus":   "CLI-only verb",
	"RecipeApply":    "CLI-only verb (clawtool recipe apply)",
	"BridgeList":     "CLI-only verb (clawtool bridge list)",
	"BridgeAdd":      "CLI-only verb (clawtool bridge add)",
	"BridgeRemove":   "CLI-only verb",
	"BridgeUpgrade":  "CLI-only verb",
	"Verify":         "CLI-only verb (clawtool verify)",
	"SemanticSearch": "agent-facing primitive",
	"McpList":        "CLI-only verb (clawtool mcp list)",
	"McpNew":         "CLI-only verb (clawtool mcp new)",
	"McpRun":         "CLI-only verb",
	"McpBuild":       "CLI-only verb",
	"McpInstall":     "CLI-only verb",
	"SandboxList":    "CLI-only verb (clawtool sandbox list)",
	"SandboxShow":    "CLI-only verb",
	"SandboxDoctor":  "CLI-only verb (clawtool sandbox doctor)",
	"SkillNew":       "addressed via the four-plane scaffolder slash command (future)",
	"WebFetch":       "no slash command — reach via Agent skill",
	"WebSearch":      "no slash command — reach via Agent skill",
	"ToolSearch":     "no slash command — reach via Agent skill",
	"Read":           "core file primitive — reach via Agent skill",
	"Write":          "core file primitive — reach via Agent skill",
	"Edit":           "core file primitive — reach via Agent skill",
	"Grep":           "core search primitive — reach via Agent skill",
	"Glob":           "core search primitive — reach via Agent skill",
	"Bash":           "core shell primitive — reach via Agent skill",
	"SendMessage":    "addressed via /clawtool-search routing today; future /clawtool-send",
}

// repoRoot walks up from this test file to the repo root (the
// directory containing go.mod). Tests run from the package
// directory by default; we need the repo root to find commands/
// and skills/.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate repo root")
	}
	dir := filepath.Dir(here)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked to filesystem root without finding go.mod")
		}
		dir = parent
	}
}

// TestSurfaceDrift_ToolsHaveSkillRoutingRows asserts that every
// shipped core tool either appears in skills/clawtool/SKILL.md
// (verbatim name) OR is in surfaceAllowlist with a justification.
// This is the load-bearing check from the three-plane shipping
// contract.
func TestSurfaceDrift_ToolsHaveSkillRoutingRows(t *testing.T) {
	root := repoRoot(t)
	skill, err := os.ReadFile(filepath.Join(root, "skills", "clawtool", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	body := string(skill)

	var missing []string
	for _, doc := range core.CoreToolDocs() {
		// SKILL.md mentions tools by bare name (`Bash`, `AgentNew`)
		// or namespaced (`mcp__clawtool__Bash`). Either form
		// counts.
		if strings.Contains(body, doc.Name) {
			continue
		}
		if _, allowed := surfaceAllowlist[doc.Name]; allowed {
			continue
		}
		missing = append(missing, doc.Name)
	}
	if len(missing) > 0 {
		t.Errorf(
			"%d core tool(s) missing from skills/clawtool/SKILL.md: %v\n"+
				"Add a routing-map row OR allow-list with a reason in surfaceAllowlist.",
			len(missing), missing)
	}
}

// TestSurfaceDrift_SlashCommandsHaveBackingTool asserts the inverse
// of the above: every commands/clawtool-*.md file must correspond
// to a real MCP tool name (or a known plugin top-level — clawtool,
// search, source-add, source-list, tools-list).
func TestSurfaceDrift_SlashCommandsHaveBackingTool(t *testing.T) {
	root := repoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "commands", "clawtool-*.md"))
	if err != nil {
		t.Fatalf("glob commands: %v", err)
	}

	// Top-level slash commands that aren't bound to a single MCP
	// tool — they orchestrate a flow, render a status panel, or
	// surface a CLI verb (`clawtool unattended grant`, etc.) that
	// has no MCP-tool counterpart.
	topLevel := map[string]bool{
		"clawtool-search.md":      true,
		"clawtool-source-add.md":  true,
		"clawtool-source-list.md": true,
		"clawtool-tools-list.md":  true,
		"clawtool-unattended.md":  true, // CLI verb — `clawtool unattended <grant|revoke|...>`
		"clawtool-a2a.md":         true, // CLI verb — `clawtool a2a card` (no MCP-tool counterpart yet, phase 2 will add A2ACard / A2APeerList)
		"clawtool-task-watch.md":  true, // CLI verb — `clawtool task watch` is consumed by Monitor, not addressable as an MCP tool
		"clawtool-dashboard.md":   true, // CLI verb — `clawtool dashboard` is a TUI; no MCP-tool counterpart by design
		"clawtool-rules.md":       true, // CLI verb — `clawtool rules <list|show|new|remove|path>`. RulesAdd MCP tool covers the add half; the others are CLI-only.
	}

	known := map[string]bool{}
	for _, doc := range core.CoreToolDocs() {
		// Slash command name convention: `/clawtool-<lower-name>`.
		// Map AgentNew → agent-new, BashOutput → bash-output, etc.
		known[strings.ToLower(camelToKebab(doc.Name))] = true
	}

	var orphans []string
	for _, p := range matches {
		base := filepath.Base(p)
		if topLevel[base] {
			continue
		}
		// Strip the "clawtool-" prefix and the ".md" suffix.
		stem := strings.TrimSuffix(strings.TrimPrefix(base, "clawtool-"), ".md")
		if known[stem] {
			continue
		}
		orphans = append(orphans, base)
	}
	if len(orphans) > 0 {
		t.Errorf(
			"%d slash command(s) have no backing core tool: %v\n"+
				"Either add the tool, rename the command, or update topLevel allowlist.",
			len(orphans), orphans)
	}
}

// camelToKebab turns "BashOutput" → "bashoutput" → preserve as
// "bash-output" so commands/clawtool-bash-output.md matches.
// Simple two-pass: insert hyphen before each uppercase letter that
// follows a lowercase letter, then lowercase.
func camelToKebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prev := rune(s[i-1])
			if prev >= 'a' && prev <= 'z' {
				b.WriteByte('-')
			}
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// TestSurfaceDrift_AllowlistEntries asserts surfaceAllowlist only
// names tools that actually ship — a stale allowlist entry is its
// own form of drift.
func TestSurfaceDrift_AllowlistEntries(t *testing.T) {
	known := map[string]bool{}
	for _, doc := range core.CoreToolDocs() {
		known[doc.Name] = true
	}
	var stale []string
	for name := range surfaceAllowlist {
		if !known[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		t.Errorf("surfaceAllowlist references %d tool(s) not in CoreToolDocs: %v",
			len(stale), stale)
	}
}

// TestSurfaceDrift_SkillAllowedToolsCoversManifest asserts every
// tool in the manifest also appears in skills/clawtool/SKILL.md's
// frontmatter `allowed-tools` whitelist (with the mcp__clawtool__
// prefix). Without this, the SKILL routing-map can recommend a
// tool that the agent's runtime then refuses to call.
//
// Codex's pass-2 review (BIAM task 4538329f) flagged this as a
// concrete hostile-contributor failure mode: "add a tool + routing
// table entry but leave it unusable because SKILL.md frontmatter
// allowed-tools isn't checked — current test passes anyway."
func TestSurfaceDrift_SkillAllowedToolsCoversManifest(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "skills", "clawtool", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	src := string(body)

	// Locate the `allowed-tools:` frontmatter line (single line per
	// agentskills.io convention; whitespace-separated entries).
	allowedLine := ""
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "allowed-tools:") {
			allowedLine = strings.TrimPrefix(line, "allowed-tools:")
			break
		}
	}
	if allowedLine == "" {
		t.Fatal("SKILL.md missing `allowed-tools:` frontmatter line")
	}
	allowedSet := map[string]bool{}
	for _, tok := range strings.Fields(allowedLine) {
		allowedSet[strings.TrimPrefix(tok, "mcp__clawtool__")] = true
	}

	// SKILL allowlist exemptions: native (non-MCP) tools that the
	// SKILL declares but aren't shipped through clawtool's MCP
	// server. These never need a manifest entry.
	skillAllowlistExempt := map[string]bool{
		// Recipes invoke `Bash` / `Read` / `Edit` etc. natively when
		// clawtool's tools are gated off; the SKILL allowlist intentionally
		// stays narrow to clawtool's surface.
	}

	var missing []string
	for _, doc := range core.CoreToolDocs() {
		if surfaceAllowlist[doc.Name] != "" {
			// Same exemptions the SKILL routing-row test honours —
			// agent-facing primitives that don't need an explicit
			// allowed-tools entry. Re-using the existing allowlist
			// keeps the policy consistent.
			//
			// These are agent-facing primitives where the SKILL routing
			// row is enough; some don't need to appear in the
			// allowlist if Claude Code auto-grants them. But to be
			// safe, we still want them all listed.
		}
		if skillAllowlistExempt[doc.Name] {
			continue
		}
		if !allowedSet[doc.Name] {
			missing = append(missing, doc.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf(
			"%d core tool(s) missing from SKILL.md frontmatter `allowed-tools`: %v\n"+
				"The SKILL routing-map can recommend these tools but the agent's\n"+
				"runtime will refuse the call. Add them to the `allowed-tools` line\n"+
				"with the `mcp__clawtool__` prefix, OR add an exemption to\n"+
				"skillAllowlistExempt with a justification.",
			len(missing), missing)
	}
}

// TestCamelToKebab covers the slug helper.
func TestCamelToKebab(t *testing.T) {
	cases := map[string]string{
		"Bash":         "bash",
		"BashOutput":   "bash-output",
		"BashKill":     "bash-kill",
		"AgentNew":     "agent-new",
		"TaskNotify":   "task-notify",
		"WebFetch":     "web-fetch",
		"BrowserFetch": "browser-fetch",
		"McpNew":       "mcp-new",
		"PortalAsk":    "portal-ask",
		"RulesCheck":   "rules-check",
	}
	for in, want := range cases {
		if got := camelToKebab(in); got != want {
			t.Errorf("camelToKebab(%q) = %q, want %q", in, got, want)
		}
	}
}
