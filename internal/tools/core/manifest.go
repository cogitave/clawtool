// Package core — typed manifest of clawtool's MCP tools (#173
// Step 2 of Codex's #1 ROI refactor).
//
// BuildManifest assembles a *registry.Manifest with one ToolSpec
// per shipped tool. Step 2 (this commit) ONLY adds entries for
// the youngest six tools — Commit, RulesCheck, AgentNew,
// BashOutput, BashKill, TaskNotify. server.go still calls each
// tool's RegisterX directly; the manifest is not yet consumed
// at boot. That hookup lands in Step 3, after the older tools
// (Bash / Read / Edit / Write / Grep / Glob / WebFetch /
// WebSearch / ToolSearch) get the same treatment.
//
// Why incremental: a single big-bang manifest migration carries
// the risk that one register-fn signature mismatch (or one
// missed gate) breaks every tool at once. Doing it six tools at
// a time, with the surface_drift_test guarding cross-plane
// invariants, makes each step audit-able and rollback-able.
//
// Why the youngest first: they have the freshest test coverage
// and the smallest blast radius if a migration mistake slips
// through. By the time we reach the older core (Bash / Read /
// Edit / Write) the registry harness is battle-tested.
package core

import (
	"github.com/cogitave/clawtool/internal/tools/registry"
	"github.com/mark3labs/mcp-go/server"
)

// BuildManifest returns the typed manifest of every clawtool
// MCP tool. Caller (server.go in Step 3) walks it via
// manifest.Apply(s, runtime, cfg.IsEnabled).
//
// Step 2 scope: 6 specs (Commit, RulesCheck, AgentNew,
// BashOutput, BashKill, TaskNotify). Each spec's Register fn
// adapts the existing RegisterX(s) signature to the
// registry.RegisterFn shape (s, runtime).
//
// Specs added but Register-not-wired-yet are LEGAL — Apply
// silently skips them. We use that to document the older tools
// in the same manifest BEFORE migrating them, so search-index
// consumers (Step 4 work) can already see the canonical entry.
func BuildManifest() *registry.Manifest {
	m := registry.New()

	// ─── Checkpoint ─────────────────────────────────────────────
	m.Append(registry.ToolSpec{
		Name:        "Commit",
		Description: "Create a git commit with Conventional Commits validation, hard Co-Authored-By trailer block, and pre_commit rules.toml gate. Use INSTEAD OF `Bash git commit -m \"…\"` — Bash can't enforce policy. Returns SHA + branch + subject; rule/validation block returns violations and refuses to commit.",
		Keywords:    []string{"commit", "git", "save", "conventional", "conventional-commits", "checkpoint", "no-coauthor", "stage", "push"},
		Category:    registry.CategoryCheckpoint,
		Gate:        "", // always-on; the value of the tool IS the policy enforcement, not a feature toggle
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterCommit(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "RulesCheck",
		Description: "Evaluate .clawtool/rules.toml against a Context (event + changed paths + commit message + tool calls + args). Returns the Verdict — every applicable rule's pass/fail with reasons. Use BEFORE committing / dispatching / ending a session to confirm operator invariants hold.",
		Keywords:    []string{"rules", "policy", "guard", "invariant", "lint", "gate", "check", "validate", "pre-commit", "session-end", "doc-sync"},
		Category:    registry.CategoryCheckpoint,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterRulesCheck(s)
		},
	})

	// ─── Authoring ─────────────────────────────────────────────
	m.Append(registry.ToolSpec{
		Name:        "AgentNew",
		Description: "Scaffold a Claude Code subagent persona — a user-defined dispatcher with allowed-tools, optional default clawtool instance, and model preference. Writes ~/.claude/agents/<name>.md (or ./.claude/agents/<name>.md). Mirror of `clawtool agent new`.",
		Keywords:    []string{"agent", "subagent", "persona", "scaffold", "new", "create", "dispatcher", "claude-agent"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterAgentNew(s)
		},
	})

	// ─── Shell — companions to Bash ────────────────────────────
	// Gate uses "Bash" so disabling Bash also hides BashOutput +
	// BashKill — they're useless without the parent.
	m.Append(registry.ToolSpec{
		Name:        "BashOutput",
		Description: "Snapshot of a background Bash task — live stdout, stderr, status (active / done / failed / cancelled), exit_code once terminal. Pair with `Bash background=true`.",
		Keywords:    []string{"bash", "background", "poll", "tail", "output", "task", "async", "long-running"},
		Category:    registry.CategoryShell,
		Gate:        "Bash",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBashOutput(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "BashKill",
		Description: "Cancel a background Bash task — SIGKILL to the whole process group. No-op when terminal. Returns the task's snapshot post-kill.",
		Keywords:    []string{"bash", "background", "kill", "cancel", "stop", "abort", "task", "async"},
		Category:    registry.CategoryShell,
		Gate:        "Bash",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBashKill(s)
		},
	})

	// ─── Dispatch — fan-in completion push ─────────────────────
	m.Append(registry.ToolSpec{
		Name:        "TaskNotify",
		Description: "Block until ANY of the watched task_ids reaches terminal — first finisher wins. Edge-triggered via in-process notifier (no SQLite poll). Use when you have multiple async dispatches in flight and want to act on whichever returns first.",
		Keywords:    []string{"task", "biam", "notify", "wait", "any", "fan-in", "fan-out", "race", "first", "completion", "push", "subscribe"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterTaskNotify(s)
		},
	})

	return m
}
