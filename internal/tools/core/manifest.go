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

	// ─── Step 3a: gateable file + shell + web tools ────────────
	// All have a `(s *server.MCPServer)` Register signature today.
	// ToolSearch + WebSearch are deferred to Step 4 because they
	// take additional dependencies (search.Index / secrets.Store);
	// adding those to Runtime is part of Step 4's hookup commit.
	m.Append(registry.ToolSpec{
		Name:        "Bash",
		Description: "Run a shell command via /bin/bash. Returns structured JSON with stdout, stderr, exit_code, duration_ms, timed_out, cwd. Output preserved on timeout via process-group SIGKILL. Set background=true to fire-and-forget — returns a task_id you poll via BashOutput / kill via BashKill.",
		Keywords:    []string{"shell", "execute", "run", "command", "terminal", "background", "async", "long-running"},
		Category:    registry.CategoryShell,
		Gate:        "Bash",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBash(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Grep",
		Description: "Search file contents for a regular-expression pattern. Powered by ripgrep (rg) with .gitignore-aware traversal and --type aliases; falls back to system grep.",
		Keywords:    []string{"search", "find", "regex", "ripgrep", "rg", "match", "pattern"},
		Category:    registry.CategoryFile,
		Gate:        "Grep",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterGrep(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Read",
		Description: "Read a file with stable line cursors and deterministic line counts. Format-aware: text, PDF (pdftotext), Jupyter (.ipynb), Word (.docx via pandoc), Excel (.xlsx via excelize), CSV/TSV, HTML (Mozilla Readability), and JSON/YAML/TOML/XML pass-through.",
		Keywords:    []string{"file", "open", "cat", "view", "pdf", "docx", "word", "xlsx", "excel", "spreadsheet", "csv", "tsv", "html", "json", "yaml", "toml", "xml", "ipynb", "notebook", "office"},
		Category:    registry.CategoryFile,
		Gate:        "Read",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterRead(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Glob",
		Description: "List files matching a glob pattern (** double-star supported). Powered by github.com/bmatcuk/doublestar.",
		Keywords:    []string{"find", "match", "files", "pattern", "wildcard", "ls", "list"},
		Category:    registry.CategoryFile,
		Gate:        "Glob",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterGlob(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "WebFetch",
		Description: "Retrieve a URL and return clean article text via Mozilla Readability for HTML, or raw text for text/* MIME types. Binary refused. 10 MB body cap.",
		Keywords:    []string{"http", "https", "url", "fetch", "download", "web", "page", "article", "scrape", "readability"},
		Category:    registry.CategoryWeb,
		Gate:        "WebFetch",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterWebFetch(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Edit",
		Description: "Replace a substring in an existing file. Atomic temp+rename, line-ending and BOM preserve, binary refusal. Refuses ambiguous matches unless replace_all=true.",
		Keywords:    []string{"replace", "modify", "change", "patch", "substitute", "search-and-replace", "sed", "fix"},
		Category:    registry.CategoryFile,
		Gate:        "Edit",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterEdit(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Write",
		Description: "Create or replace a whole file. Atomic temp+rename, parent directory auto-create, line-ending and BOM preserve when overwriting.",
		Keywords:    []string{"create", "save", "overwrite", "tee", "echo", "new", "file"},
		Category:    registry.CategoryFile,
		Gate:        "Write",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterWrite(s)
		},
	})

	// ─── Always-on individual tools (single-Register-fn shape) ─
	m.Append(registry.ToolSpec{
		Name:        "Verify",
		Description: "Run a repo's tests / lints / typechecks via whichever runner it declares (Make / pnpm / npm / go / pytest / ruby / cargo / just). Returns one structured pass/fail per check. Buffered single payload — for streaming output use Bash.",
		Keywords:    []string{"verify", "test", "tests", "check", "ci", "make", "pnpm", "npm", "go-test", "pytest", "cargo", "just", "validate"},
		Category:    registry.CategorySetup,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterVerify(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "SemanticSearch",
		Description: "Semantic (intent-based) code search. Use for conceptual queries like 'where do we rotate auth tokens?' or 'how is caching wired?' — Grep stays the literal-regex tool. Wraps chromem-go + an embedding provider; index is built lazily on first call.",
		Keywords:    []string{"semantic", "embeddings", "vector", "concept", "intent", "find-code", "rag", "search-code", "discover", "where"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSemanticSearch(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "BrowserFetch",
		Description: "Render a URL inside a real headless browser (Obscura, V8 + Chrome DevTools Protocol) and return clean prose for HTML or the value of a custom JS expression. Use when WebFetch returns empty SPA shells (Next.js / React / hydrated pages). Stateless per call.",
		Keywords:    []string{"browser", "headless", "spa", "javascript", "render", "obscura", "puppeteer", "playwright", "fetch", "scrape", "react", "next", "hydrated", "cdp"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBrowserFetch(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "BrowserScrape",
		Description: "Render many URLs in parallel through a real browser engine (Obscura) and capture a JS expression's value per page. Bulk SPA scraping with configurable concurrency. Stateless per URL.",
		Keywords:    []string{"browser", "headless", "scrape", "bulk", "parallel", "spa", "obscura", "crawler", "harvest"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBrowserScrape(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "SkillNew",
		Description: "Scaffold a Claude Code skill (agentskills.io standard): SKILL.md with frontmatter + scripts/ + references/ + assets/. Same template the `clawtool skill new` CLI emits.",
		Keywords:    []string{"skill", "scaffold", "new", "create", "agentskills", "skill-md", "claude-skill"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSkillNew(s)
		},
	})

	return m
}
