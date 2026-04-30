// Package core — typed manifest of clawtool's MCP tools (#173, the
// "Tool Manifest Registry" refactor).
//
// BuildManifest assembles a *registry.Manifest with one ToolSpec
// per shipped tool. server.go reads this manifest at boot and
// invokes each ToolSpec.Register; there is no separate per-tool
// init wiring. Adding a new tool is one ToolSpec entry plus one
// RegisterX function — no surface_drift_test edits required since
// the manifest is the single source of truth (Bash / Read / Edit
// / Write / Grep / Glob / WebFetch /
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
	"github.com/cogitave/clawtool/internal/secrets"
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
	m.Append(registry.ToolSpec{
		Name:        "RulesAdd",
		Description: "Append a new rule to .clawtool/rules.toml (local) or ~/.config/clawtool/rules.toml (user). Same writer `clawtool rules new` uses — both surfaces share the canonical TOML emitter. Use this when the operator wants to enforce an invariant programmatically (e.g. 'README must update when core tools change') without hand-editing the toml.",
		Keywords:    []string{"rules", "add", "new", "create", "policy", "invariant", "lint", "gate", "doc-sync", "pre-commit", "scope", "user", "local"},
		Category:    registry.CategoryCheckpoint,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterRulesAdd(s)
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

	// ─── Ambient editor context (octopus pattern) ──────────────
	// SetContext + GetContext share an in-process map keyed by
	// session_id. Lets an agent / IDE integration deposit "user
	// is editing X line Y, intent Z" once and have other tools /
	// agents read it without re-asking.
	m.Append(registry.ToolSpec{
		Name:        "SetContext",
		Description: "Store ambient editor context (file path, selected lines, project root, intent) for the current session. Merges with existing state — supplying just `start_line` updates the cursor without clobbering the file path. Lifetime: process-local (daemon restart wipes).",
		Keywords:    []string{"context", "editor", "ambient", "session", "scratchpad", "intent", "file", "selection", "cursor", "set", "store"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSetContext(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "GetContext",
		Description: "Read the ambient editor context previously set via SetContext. Returns the merged state for the named session or empty when nothing has been stored. Pair with SetContext when an agent / tool needs the operator's current focus without re-asking.",
		Keywords:    []string{"context", "editor", "ambient", "session", "scratchpad", "intent", "file", "selection", "cursor", "get", "read"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			// RegisterSetContext registers BOTH SetContext and
			// GetContext on the same MCP server. The second
			// ToolSpec is here for surface-discovery purposes
			// (manifest-driven listing, search index) — calling
			// the registrar twice is safe because the underlying
			// AddTool is idempotent on tool name.
			RegisterSetContext(s)
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
	// ─── Skill discovery: SkillList → SkillLoad ────────────────
	// On-demand mount pattern (ADR-029 phase 3). Model lists
	// installed skills, picks one, loads its full content into
	// the current turn — same shape claude.ai's /mnt/skills
	// filesystem mount provides via view/read.
	m.Append(registry.ToolSpec{
		Name:        "SkillList",
		Description: "Enumerate Agent Skills installed on this host. Returns name, scope (project|user|catalog), description, and absolute path. Pair with SkillLoad to pull a skill's full content.",
		Keywords:    []string{"skill", "list", "enumerate", "discover", "agentskills", "claude-skill", "available", "installed"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSkillList(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "SkillLoad",
		Description: "Load one Agent Skill's content (frontmatter + body) by name. Use after SkillList narrows the candidate. Lookup precedence: project ./.claude/skills > user ~/.claude/skills > $CLAWTOOL_SKILLS_DIR.",
		Keywords:    []string{"skill", "load", "read", "fetch", "view", "agentskills", "claude-skill", "on-demand", "mount"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSkillLoad(s)
		},
	})

	// ─── Step 4: Runtime-dependent + multi-tool wrappers ───────
	//
	// Two patterns at play:
	//
	// 1) Tools that need a Runtime field (ToolSearch / WebSearch).
	//    The Register fn closes over rt.Index / rt.Secrets and
	//    delegates to the existing RegisterX(s, dep) signature.
	//
	// 2) Multi-tool wrappers (Recipe / Bridge / Agent / Task /
	//    Portal / Mcp / Sandbox) where a single RegisterX call
	//    registers N tools at once. Pattern: the FIRST spec for
	//    the bundle has Register set; the others have Register=nil
	//    so manifest.Apply skips them. Search docs still pick
	//    every spec up because SearchDocs walks every entry. This
	//    keeps the manifest shape "1 tool = 1 spec" without
	//    forcing us to split the wrapper functions.
	//
	// ToolSearch — bleve BM25 over the full catalog. Closes over
	// rt.Index built at boot.
	m.Append(registry.ToolSpec{
		Name:        "ToolSearch",
		Description: "Find tools by natural-language query. BM25 ranking via bleve. Use this first when you have a large catalog.",
		Keywords:    []string{"discover", "find", "search", "query", "tools"},
		Category:    registry.CategoryDiscovery,
		Gate:        "ToolSearch",
		Register: func(s *server.MCPServer, rt registry.Runtime) {
			RegisterToolSearch(s, rt.Index)
		},
	})

	// WebSearch — backend selection + API key from rt.Secrets.
	// Adapter casts our slim SecretsStore interface back to
	// *secrets.Store via type assertion; the real wiring in
	// server.go always supplies the concrete pointer.
	m.Append(registry.ToolSpec{
		Name:        "WebSearch",
		Description: "Run a web search via the configured backend (default Brave). Returns ranked {title, url, snippet}. API key in secrets[scope=websearch].",
		Keywords:    []string{"search", "web", "google", "brave", "tavily", "duckduckgo", "results", "query", "engine"},
		Category:    registry.CategoryWeb,
		Gate:        "WebSearch",
		Register: func(s *server.MCPServer, rt registry.Runtime) {
			// rt.Secrets is `any`; the caller (server.go) always
			// passes *secrets.Store, so a nil assertion here would
			// be a programmer error worth a typed nil at the call
			// site rather than a silent skip.
			store, _ := rt.Secrets.(*secrets.Store)
			RegisterWebSearch(s, store)
		},
	})

	// ─── Recipe* bundle (RegisterRecipeTools registers all 3) ──
	m.Append(registry.ToolSpec{
		Name:        "RecipeList",
		Description: "List clawtool's project-setup recipes (governance, commits, release, CI, quality, supply-chain, knowledge, agents, runtime). Each recipe injects a canonical config slice so a fresh repo gets the operator's standards in one apply.",
		Keywords:    []string{"recipe", "recipes", "list", "init", "setup", "scaffold", "release-please", "dependabot", "codeowners", "license"},
		Category:    registry.CategorySetup,
		Gate:        "",
		// First spec in bundle invokes the wrapper.
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterRecipeTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "RecipeStatus",
		Description: "Report which recipes are already applied vs absent for the current repo. Use BEFORE RecipeApply to avoid re-installing or to surface drift.",
		Keywords:    []string{"recipe", "status", "detect", "absent", "applied", "drift"},
		Category:    registry.CategorySetup,
		Gate:        "",
		// Register=nil — companion to RecipeList; the bundle
		// is registered exactly once by RecipeList's spec.
	})
	m.Append(registry.ToolSpec{
		Name:        "RecipeApply",
		Description: "Apply one project-setup recipe by name (license, codeowners, conventional-commits, release-please, dependabot, brain, ...). Idempotent — re-applying is safe.",
		Keywords:    []string{"recipe", "apply", "install", "init", "setup", "scaffold"},
		Category:    registry.CategorySetup,
		Gate:        "",
	})

	// ─── Bridge* bundle ────────────────────────────────────────
	m.Append(registry.ToolSpec{
		Name:        "BridgeList",
		Description: "List installable bridges to other coding-agent CLIs (codex, opencode, gemini, hermes) with current install state.",
		Keywords:    []string{"bridges", "plugins", "install", "available", "codex", "opencode", "gemini", "hermes", "list"},
		Category:    registry.CategorySetup,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterBridgeTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "BridgeAdd",
		Description: "Install the canonical bridge for a family (codex / opencode / gemini / hermes). Wraps the upstream's Claude Code plugin or built-in subcommand. Idempotent.",
		Keywords:    []string{"install", "bridge", "plugin", "add", "codex", "opencode", "gemini", "hermes", "setup"},
		Category:    registry.CategorySetup,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "BridgeRemove",
		Description: "Remove the bridge for a family. v0.10 ships as a manual hint; full uninstall lands in v0.10.x.",
		Keywords:    []string{"uninstall", "remove", "bridge", "plugin"},
		Category:    registry.CategorySetup,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "BridgeUpgrade",
		Description: "Re-run the bridge install (idempotent; pulls the latest plugin version).",
		Keywords:    []string{"upgrade", "update", "bridge", "plugin", "refresh"},
		Category:    registry.CategorySetup,
		Gate:        "",
	})

	// ─── Agent* bundle (SendMessage + AgentList) ───────────────
	m.Append(registry.ToolSpec{
		Name:        "SendMessage",
		Description: "Forward a prompt to another AI coding-agent CLI (claude / codex / opencode / gemini / hermes) and stream its reply. clawtool wraps each upstream's published headless mode; the bridge plugin must be installed first via BridgeAdd.",
		Keywords:    []string{"dispatch", "delegate", "forward", "prompt", "agent", "claude", "codex", "opencode", "gemini", "hermes", "relay", "ask", "ai"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterAgentTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "AgentList",
		Description: "Snapshot of the supervisor's agent registry — every configured instance with family, bridge, callable status, and auth scope.",
		Keywords:    []string{"list", "agents", "instances", "registry", "available", "callable"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "AgentDetect",
		Description: "Probe one host AI coding agent (claude-code, codex, …) and report whether it's detected on this host AND whether clawtool has claimed its native tools. Returns {adapter, detected, claimed, exit_code}; same exit-code contract as `clawtool agents detect` CLI (0=detected+claimed, 1=detected-not-claimed, 2=not-detected). Read-only.",
		Keywords:    []string{"detect", "probe", "agent", "claimed", "claim", "host", "adapter", "installer", "bootstrap", "introspect"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterAgentDetectTool(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "SourceCheck",
		Description: "Probe configured MCP source instances and report which env vars resolve via the secrets store. Returns {entries: [{name, ready, missing[]}], ready}. Pass `instance` to filter to one source; omit to probe all. Same wire shape as `clawtool source check [<instance>] --json`. Read-only; emits env-var NAMES only, never values.",
		Keywords:    []string{"source", "check", "probe", "ready", "secrets", "env", "credentials", "missing", "installer", "bootstrap", "introspect"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSourceCheckTool(s)
		},
	})

	// ─── Version probe (CategoryDiscovery — read-only metadata) ─
	m.Append(registry.ToolSpec{
		Name:        "Version",
		Description: "Snapshot of the running clawtool binary's identity: name, semver, Go runtime, GOOS/GOARCH, VCS commit + modified flag. Same shape as `clawtool version --json` and the `build` field of GET /v1/health. Read-only.",
		Keywords:    []string{"version", "build", "info", "go", "platform", "commit", "identity", "introspect"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterVersionTool(s)
		},
	})

	// ─── Task* bundle (TaskGet + TaskWait + TaskList; TaskNotify
	//     already shipped above as its own RegisterTaskNotify) ──
	m.Append(registry.ToolSpec{
		Name:        "TaskGet",
		Description: "Snapshot of one BIAM task: status + every message persisted under task_id. Pair with SendMessage --bidi to dispatch async and poll without blocking.",
		Keywords:    []string{"task", "biam", "async", "poll", "result", "snapshot"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterTaskTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "TaskWait",
		Description: "Block until a BIAM task reaches a terminal state. Use when the caller has nothing else to do until the upstream finishes.",
		Keywords:    []string{"task", "biam", "wait", "block", "result", "terminal"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "TaskList",
		Description: "Recent BIAM tasks (default 50). Use to find task_ids when the caller forgot one mid-conversation.",
		Keywords:    []string{"task", "biam", "list", "recent", "history"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "TaskReply",
		Description: "Append a structured reply envelope to an existing BIAM task. Used by dispatched peer agents (codex / gemini / opencode / claude) to push chunked findings back to their caller without dumping a giant blob through stdout. Read CLAWTOOL_TASK_ID + CLAWTOOL_FROM_INSTANCE from the process env when running as a dispatched peer.",
		Keywords:    []string{"task", "biam", "reply", "respond", "append", "callback", "fan-in", "peer"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterTaskReply(s)
		},
	})

	// ─── Portal* bundle (RegisterPortalTools registers 6) ──────
	m.Append(registry.ToolSpec{
		Name:        "PortalList",
		Description: "List configured web-UI portals (saved authenticated browser targets). A portal pairs a base URL with login cookies, selectors, and a 'response done' predicate so PortalAsk can drive the page through Obscura.",
		Keywords:    []string{"portal", "portals", "list", "browser", "target", "saved", "config", "registry"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterPortalTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalAsk",
		Description: "Drive a saved portal with the given prompt and return the rendered response. Spawns Obscura's CDP server, seeds cookies + extra headers, navigates to start_url, runs login_check + ready_predicate, fills the input selector, clicks submit (or dispatches Enter), polls response_done_predicate, and extracts the last response selector's innerText.",
		Keywords:    []string{"portal", "ask", "browser", "chat", "deepseek", "perplexity", "phind", "send", "drive", "automate", "cdp"},
		Category:    registry.CategoryWeb,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalUse",
		Description: "Set the sticky-default portal so PortalAsk calls without an explicit name route here.",
		Keywords:    []string{"portal", "use", "sticky", "default", "set"},
		Category:    registry.CategoryWeb,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalWhich",
		Description: "Resolve the sticky-default portal — env > sticky file > single-configured fallback.",
		Keywords:    []string{"portal", "which", "default", "sticky"},
		Category:    registry.CategoryWeb,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalUnset",
		Description: "Clear the sticky-default portal.",
		Keywords:    []string{"portal", "unset", "clear", "sticky"},
		Category:    registry.CategoryWeb,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalRemove",
		Description: "Remove a portal stanza from config.toml. Cookies under [scopes.\"portal.<name>\"] in secrets.toml stay in place; clean manually if no longer needed.",
		Keywords:    []string{"portal", "remove", "delete", "config"},
		Category:    registry.CategoryWeb,
		Gate:        "",
	})

	// ─── Mcp* bundle (RegisterMcpTools registers 5) ────────────
	m.Append(registry.ToolSpec{
		Name:        "McpList",
		Description: "List MCP server projects under a root path (default cwd). Detects via the .clawtool/mcp.toml marker the v0.17 generator writes. Sister of `clawtool skill list` for MCP authoring.",
		Keywords:    []string{"mcp", "scaffold", "author", "list", "projects", "server", "build"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterMcpTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "McpNew",
		Description: "Scaffold a new MCP server project (Go via mcp-go, Python via FastMCP, TypeScript via @modelcontextprotocol/sdk). Wizard asks for description / language / transport / packaging / tools.",
		Keywords:    []string{"mcp", "scaffold", "new", "create", "generate", "author", "go", "python", "typescript"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "McpRun",
		Description: "Run an MCP server project in dev mode (stdio).",
		Keywords:    []string{"mcp", "run", "dev", "stdio"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "McpBuild",
		Description: "Build / package an MCP server project (binary, npm, pypi, or Docker image).",
		Keywords:    []string{"mcp", "build", "compile", "package", "docker"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "McpInstall",
		Description: "Build + register a local MCP server project as [sources.<instance>] in config.toml — same surface as `clawtool source add` but auto-discovers the launch command from the project's `.clawtool/mcp.toml`.",
		Keywords:    []string{"mcp", "install", "register", "source", "local"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
	})

	// ─── Sandbox* bundle (RegisterSandboxTools registers 3) ────
	m.Append(registry.ToolSpec{
		Name:        "SandboxList",
		Description: "List configured sandbox profiles. Each profile constrains a `clawtool send` dispatch — paths, network, env, resource limits. Engines: bwrap (Linux), sandbox-exec (macOS), docker (anywhere fallback).",
		Keywords:    []string{"sandbox", "list", "profiles", "isolation", "security", "bwrap", "sandbox-exec", "docker"},
		Category:    registry.CategorySetup,
		Gate:        "",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSandboxTools(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "SandboxShow",
		Description: "Render a parsed sandbox profile — paths, network policy, env allow/deny, resource limits — plus the engine that would run it on this host. Use BEFORE recommending a profile so the constraints are explicit.",
		Keywords:    []string{"sandbox", "show", "profile", "isolation", "constraints"},
		Category:    registry.CategorySetup,
		Gate:        "",
	})
	m.Append(registry.ToolSpec{
		Name:        "SandboxDoctor",
		Description: "Report which sandbox engines are available on this host (bwrap / sandbox-exec / docker). Use to recommend the right engine to install when none is available.",
		Keywords:    []string{"sandbox", "doctor", "engine", "diagnostic", "bwrap", "sandbox-exec", "docker"},
		Category:    registry.CategorySetup,
		Gate:        "",
	})

	return m
}
