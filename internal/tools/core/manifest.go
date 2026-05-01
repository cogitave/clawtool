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
	setuptools "github.com/cogitave/clawtool/internal/tools/setup"
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
		UsageHint:   "Always use Commit, never `Bash git commit -m ...` — only Commit enforces the operator's pre_commit rules.toml gate, Conventional Commits shape, and Co-Authored-By block. The subject MUST start with a Conventional Commits type (`feat:`, `fix:`, `docs:`, `chore:`, …) or the call refuses. If the rules gate blocks the commit, read the violation list and fix the underlying problem rather than retrying.",
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
		UsageHint:   "Run RulesCheck before Commit (or before a dispatch / session-end) to surface invariant violations without actually committing — Commit calls the same gate internally, but a dry-run via RulesCheck lets you fix the file shape first. Pass the event matching the imminent action (`pre_commit`, `pre_send`, `session_end`); a wrong event silently produces zero verdicts.",
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
		UsageHint:   "Pick RulesAdd when the operator describes a repeating bug or policy as 'don't ship this again' — encode it as a rule rather than relying on Claude-side memory. Choose `local` scope for repo-specific invariants, `user` scope only when the operator explicitly wants the rule to follow them across every project on the host.",
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
		UsageHint:   "Use AgentNew to author a NEW Claude Code subagent persona file; do not confuse with AgentList (snapshot of running peer agents) or SendMessage (dispatching a prompt). Default to project scope (`./.claude/agents/`) so the persona ships with the repo; only choose user scope when the operator explicitly asks for cross-project availability.",
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
		UsageHint:   "Only useful when paired with a `Bash background=true` call — call BashOutput with that returned task_id to read the latest stdout/stderr without blocking. Common mistake: polling in a tight loop; space calls a few seconds apart, or use TaskWait if blocking is fine.",
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
		UsageHint:   "Use BashKill when a backgrounded `Bash` task is hung or no longer needed — it sends SIGKILL to the whole process group, so child processes die too. Calling it on an already-terminal task is a safe no-op; do NOT use it to clean up a foreground (non-background) Bash call, which has already returned.",
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
		UsageHint:   "Reach for TaskNotify when you have several BIAM tasks in flight and want to act on the FIRST finisher — it's edge-triggered, so there's no polling overhead. Use TaskWait instead when you only care about one specific task. Pass every relevant task_id; an empty list returns immediately with no event.",
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
		UsageHint:   "Use SetContext when an IDE / wrapper integration wants to deposit 'operator is on file X line Y with intent Z' so dispatched peer agents (codex / gemini / opencode) can read it via GetContext without re-asking. Storage is process-local; a daemon restart wipes it, so don't treat it as durable. Updates merge — passing one field doesn't clobber the others.",
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
		UsageHint:   "Use GetContext to read whatever an editor / wrapper integration deposited via SetContext for the current session. Returns an empty struct when nothing has been stored — that's expected for fresh sessions, not an error. Pair every read with the same `session` value the writer used.",
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
		UsageHint:   "Bash runs commands via `/bin/bash -c`, so shell builtins and pipes work out of the box; structured JSON is returned for stdout/stderr/exit_code/duration_ms. For long-running work (build, test, server), pass `background=true` and poll with BashOutput / cancel with BashKill instead of letting the 2-minute default timeout kill it. To target a Linux mount path on WSL, set `cwd` explicitly — relative paths default to $HOME.",
		AlwaysLoad:  true, // hot tool — Anthropic's Code-execution-with-MCP recipe
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
		UsageHint:   "Pick Grep when you need to search file CONTENTS by regex; pick Glob when you only need filenames. By default it honors .gitignore via ripgrep — pass `path` to a directory outside the repo or set `glob` to broaden the search. Mistake to avoid: searching a literal `.` or `(` without escaping; the pattern is a regex.",
		AlwaysLoad:  true,
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
		UsageHint:   "Read returns clean text WITHOUT line numbers by default — pass `with_line_numbers=true` only when the agent's next step is an Edit and it needs to count lines. The Read-before-Write guardrail tracks whether this tool was called against a path before allowing Write to overwrite it. For binaries Read refuses with a typed error; use Glob to discover, then a format-aware engine inside Read for PDFs / .docx / .xlsx / .ipynb.",
		AlwaysLoad:  true,
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
		UsageHint:   "Pick Glob when you need a file LIST by name/path; pick Grep when you need to search file CONTENTS. Common mistake: using Glob with a substring like `config` — that won't match anything; the pattern is a glob, so use `**/*config*` instead. Honors .gitignore by default.",
		AlwaysLoad:  true,
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
		UsageHint:   "Pick WebFetch for static HTML / text endpoints; if the page is React / Next / hydrated SPA shell and comes back nearly empty, switch to BrowserFetch which renders via a real headless browser. WebFetch refuses binary MIME types and caps the body at 10 MB — that's intentional, not a bug. Returns Mozilla-Readability-extracted prose, not raw HTML.",
		AlwaysLoad:  true,
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
		UsageHint:   "Use Edit for surgical line-level changes to an existing file; use Write only when replacing the whole file. Edit refuses ambiguous matches (old_string appearing more than once) by default — fix that by adding more surrounding context to the match string, NOT by setting `replace_all=true` unless you genuinely want every occurrence replaced. Whitespace counts: copy the indentation byte-for-byte.",
		AlwaysLoad:  true,
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
		UsageHint:   "Use Write only for brand-new files or when replacing the whole file is genuinely intended. To change a few lines in an existing file, prefer Edit — it's safer (refuses ambiguous matches) and clearer in diffs. The Read-before-Write guardrail will refuse an overwrite the agent has not Read this session; that is intentional, not a bug.",
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
		UsageHint:   "Use Verify when the operator wants 'just run the tests' across whichever runner the repo declares (Make, pnpm, go, pytest, cargo, just) — it auto-detects and returns one structured pass/fail per check. Output is buffered into a single payload, so for streaming logs of one specific suite use Bash directly. A fast smoke pass typically takes seconds; full e2e gates can run minutes.",
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
		UsageHint:   "Use SemanticSearch when the question is conceptual ('where do we rotate auth tokens?', 'how is caching wired?') — Grep stays the literal-regex tool. The first call builds the index lazily; expect a slow first response, then fast follow-ups. Don't waste it on identifiers that grep would find immediately.",
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
		UsageHint:   "Reach for BrowserFetch when WebFetch returns a near-empty SPA shell — it renders the page through a real headless browser (Obscura) and waits for hydration. More expensive than WebFetch (CDP boot + a render budget) so use WebFetch first. Pass a `js` expression to extract a specific value; otherwise it returns the cleaned innerText.",
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
		UsageHint:   "Use BrowserScrape when you need to extract one JS-evaluated value from many SPA URLs in parallel; for a single page use BrowserFetch, for static pages WebFetch is cheaper. Configurable concurrency means 10+ URLs per call is reasonable; running it sequentially over a Bash loop defeats the purpose.",
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
		UsageHint:   "Use SkillNew to scaffold a fresh agentskills.io-format skill (SKILL.md + scripts/ + references/ + assets/); use SkillList to discover what's already installed, SkillLoad to read one. Default frontmatter `description` is the trigger phrase the model sees — write it as 'use when X', not as a marketing tagline.",
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
		UsageHint:   "Call SkillList first when the operator references a skill by approximate name or asks 'what skills are available' — it returns name + scope + description + path so you can pick the right one before SkillLoad pulls its body. Lookup precedence is project > user > catalog; a skill in `./.claude/skills` shadows a same-named user-level entry.",
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
		UsageHint:   "Use SkillLoad after SkillList narrows the candidate — it returns the full SKILL.md body plus frontmatter ready to mount into the current turn. Common mistake: SkillLoad-ing a skill the operator never installed; if the name isn't in SkillList output, suggest installing it via the relevant authoring path rather than guessing.",
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
		UsageHint:   "Reach for ToolSearch FIRST when the catalog is large or you only know roughly what you want — BM25 ranking surfaces the right tool from a fuzzy query like 'commit changes' or 'long-running shell'. If you already know the exact tool name, call it directly; ToolSearch is for discovery, not as a wrapper for every call.",
		AlwaysLoad:  true,
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
		UsageHint:   "Use WebSearch for general 'what's the current state of X' lookups; the result is ranked {title, url, snippet} from the configured backend (default Brave). It needs an API key configured under secrets[scope=websearch] — if calls fail with auth errors, the fix is configuration, not retry. For follow-up content extraction, hand the chosen URL to WebFetch or BrowserFetch.",
		AlwaysLoad:  true,
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
		UsageHint:   "Call RecipeList when the operator wants to know what project-setup bundles are available (governance, commits, release, CI, supply-chain, …) before applying any. Pair with RecipeStatus to see which are already in place; RecipeApply does the actual install. Each recipe is a curated config slice, not a free-form generator.",
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
		UsageHint:   "Run RecipeStatus before RecipeApply to avoid re-installing a recipe that's already applied or to surface drift (the recipe was applied once, but a file has since been hand-edited away). For a single repo it's a quick read-only probe; safe to call freely.",
		// Register=nil — companion to RecipeList; the bundle
		// is registered exactly once by RecipeList's spec.
	})
	m.Append(registry.ToolSpec{
		Name:        "RecipeApply",
		Description: "Apply one project-setup recipe by name (license, codeowners, conventional-commits, release-please, dependabot, brain, ...). Idempotent — re-applying is safe.",
		Keywords:    []string{"recipe", "apply", "install", "init", "setup", "scaffold"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use RecipeApply to install one named recipe (license, codeowners, conventional-commits, release-please, dependabot, brain, …). Idempotent — re-applying is safe and surfaces no diff when nothing changed. Common mistake: applying recipes ad-hoc when the operator wanted a bundle; check the recipe catalog with RecipeList first.",
	})

	// ─── Bridge* bundle ────────────────────────────────────────
	m.Append(registry.ToolSpec{
		Name:        "BridgeList",
		Description: "List installable bridges to other coding-agent CLIs (codex, opencode, gemini, hermes) with current install state.",
		Keywords:    []string{"bridges", "plugins", "install", "available", "codex", "opencode", "gemini", "hermes", "list"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Run BridgeList first to see which AI-coding-agent bridges (codex / opencode / gemini / hermes) are installed and which are available. Pair with BridgeAdd to install a missing one; SendMessage will refuse to dispatch to a family without an installed bridge.",
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
		UsageHint:   "Use BridgeAdd to install a bridge for the FIRST time; use BridgeUpgrade to refresh an existing install. Idempotent — re-running on an already-installed family produces no diff. Common mistake: BridgeAdd does NOT configure the upstream's auth (codex CLI login, gemini API key); that's a separate operator step the bridge documentation describes.",
	})
	m.Append(registry.ToolSpec{
		Name:        "BridgeRemove",
		Description: "Remove the bridge for a family. v0.10 ships as a manual hint; full uninstall lands in v0.10.x.",
		Keywords:    []string{"uninstall", "remove", "bridge", "plugin"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use BridgeRemove to uninstall a previously-added bridge family. v0.10 ships as a manual hint (it tells you which files to delete) rather than running the uninstall itself — so plan to do the removal yourself for now.",
	})
	m.Append(registry.ToolSpec{
		Name:        "BridgeUpgrade",
		Description: "Re-run the bridge install (idempotent; pulls the latest plugin version).",
		Keywords:    []string{"upgrade", "update", "bridge", "plugin", "refresh"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use BridgeUpgrade when the operator already has a bridge installed and you want to refresh to the latest plugin version; use BridgeAdd for the initial install. Common mistake: calling BridgeUpgrade as a way to fix a broken bridge — it just re-runs the same install path, so if BridgeAdd failed, Upgrade will fail the same way. Run BridgeList first to confirm the family is installed.",
	})

	// ─── Agent* bundle (SendMessage + AgentList) ───────────────
	m.Append(registry.ToolSpec{
		Name:        "SendMessage",
		Description: "Forward a prompt to another AI coding-agent CLI (claude / codex / opencode / gemini / hermes) and stream its reply. clawtool wraps each upstream's published headless mode; the bridge plugin must be installed first via BridgeAdd.",
		Keywords:    []string{"dispatch", "delegate", "forward", "prompt", "agent", "claude", "codex", "opencode", "gemini", "hermes", "relay", "ask", "ai"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		UsageHint:   "Use SendMessage to dispatch a prompt to a peer AI coding agent (claude / codex / opencode / gemini / hermes); the bridge plugin must be installed first via BridgeAdd. If the target agent isn't currently live as a BIAM peer, clawtool will auto-spawn it in a new tmux pane (when tmux is available) before routing the message — no manual `Spawn` call needed. Pass `mode=peer-only` to disable auto-spawn (typed error when no peer matches), `mode=auto-tmux` to require tmux delivery (typed error when no tmux), `mode=spawn-only` for the legacy fresh-subprocess path. Default `mode=peer-prefer` falls back gracefully: live peer first, then tmux auto-spawn, then a fresh subprocess. Routing rule: code-writing or structured spec → codex or gemini; opencode is research-only (read-only investigation), so re-dispatch tiny opencode replies to gemini if you need code. Pass `bidi=true` when you want a task_id back so you can pair with TaskWait or TaskNotify. Ask the operator a clarifying question when the target family is ambiguous (\"do you want codex or gemini?\") rather than guessing.",
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
		UsageHint:   "Use AgentList to see every configured peer agent and whether each is currently callable (bridge installed, auth ready). Run it before SendMessage when the operator says 'ask the codex / gemini one' — it confirms the instance exists and is healthy rather than dispatching blind.",
	})
	m.Append(registry.ToolSpec{
		Name:        "AgentDetect",
		Description: "Probe one host AI coding agent (claude-code, codex, …) and report whether it's detected on this host AND whether clawtool has claimed its native tools. Returns {adapter, detected, claimed, exit_code}; same exit-code contract as `clawtool agents detect` CLI (0=detected+claimed, 1=detected-not-claimed, 2=not-detected). Read-only.",
		Keywords:    []string{"detect", "probe", "agent", "claimed", "claim", "host", "adapter", "installer", "bootstrap", "introspect"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		UsageHint:   "Run AgentDetect to learn whether a specific host AI agent (claude-code, codex, …) is installed AND whether clawtool has claimed its native tools; the exit codes mirror the CLI (0=detected+claimed, 1=detected-not-claimed, 2=not-detected). Use this when an installer or onboarding flow needs to branch on host state; AgentList is for the registry of CONFIGURED peer instances, not host-installed agents.",
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
		UsageHint:   "Use SourceCheck to verify configured MCP source instances have all their required env vars resolvable — returns env-var NAMES only (never values), so it's safe to surface in logs. Pass `instance` to scope to one source; omit to probe all. Run it before debugging a 'why isn't this source working' moment.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSourceCheckTool(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "SourceRegistry",
		Description: "Probe the official MCP Registry (registry.modelcontextprotocol.io) and return the first N server entries. Returns {url, count, servers: [{name, description, version}]}. Pass `limit` (1..50, default 10) to control page size; pass `url` to override the base URL. Same wire shape as `clawtool source registry --json`. Read-only; no auth required (the registry is anonymous). Use to discover ecosystem-published MCP servers before `source add` falls back to the embedded catalog.",
		Keywords:    []string{"source", "registry", "mcp", "discover", "catalog", "ecosystem", "servers", "available", "browse", "remote", "introspect"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		UsageHint:   "Use SourceRegistry to browse the official MCP Registry (registry.modelcontextprotocol.io) for ecosystem-published servers BEFORE falling back to clawtool's embedded catalog. Tunable `limit` (1..50, default 10) controls page size; the call is anonymous, so no auth setup is needed. Pair with `clawtool source add` once you find the right server.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			RegisterSourceRegistryTool(s)
		},
	})

	// ─── Version probe (CategoryDiscovery — read-only metadata) ─
	m.Append(registry.ToolSpec{
		Name:        "Version",
		Description: "Snapshot of the running clawtool binary's identity: name, semver, Go runtime, GOOS/GOARCH, VCS commit + modified flag. Same shape as `clawtool version --json` and the `build` field of GET /v1/health. Read-only.",
		Keywords:    []string{"version", "build", "info", "go", "platform", "commit", "identity", "introspect"},
		Category:    registry.CategoryDiscovery,
		Gate:        "",
		UsageHint:   "Use Version to confirm exactly which clawtool binary the daemon is running — semver, Go runtime, GOOS/GOARCH, VCS commit + modified flag. The same data lives in `clawtool version --json` and the GET /v1/health `build` field; choose Version inside MCP flows and the CLI form when answering an operator at the shell.",
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
		UsageHint:   "Use TaskGet for a non-blocking SNAPSHOT of a BIAM task's status + persisted messages; use TaskWait when you can't proceed without the result; use TaskNotify when racing several tasks. Output includes everything the dispatched peer pushed via TaskReply, so it's the right tool for 'show me what codex has said so far'.",
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
		UsageHint:   "Use TaskWait for ONE specific task_id when you can't proceed without its result; use TaskNotify for several in-flight tasks when you want the first finisher; use TaskGet for a non-blocking snapshot. Common mistake: calling TaskWait in a loop — that's a polling re-implementation; TaskNotify already pushes on completion.",
	})
	m.Append(registry.ToolSpec{
		Name:        "TaskList",
		Description: "Recent BIAM tasks (default 50). Use to find task_ids when the caller forgot one mid-conversation.",
		Keywords:    []string{"task", "biam", "list", "recent", "history"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		UsageHint:   "Use TaskList when the caller forgot a task_id mid-conversation or wants the most-recent dispatches. Default page is 50; if the operator dispatched dozens, ask whether they want the latest or a specific time window before assuming. It's a read-only history probe — safe to call freely.",
	})
	m.Append(registry.ToolSpec{
		Name:        "TaskReply",
		Description: "Append a structured reply envelope to an existing BIAM task. Used by dispatched peer agents (codex / gemini / opencode / claude) to push chunked findings back to their caller without dumping a giant blob through stdout. Read CLAWTOOL_TASK_ID + CLAWTOOL_FROM_INSTANCE from the process env when running as a dispatched peer.",
		Keywords:    []string{"task", "biam", "reply", "respond", "append", "callback", "fan-in", "peer"},
		Category:    registry.CategoryDispatch,
		Gate:        "",
		UsageHint:   "Only call TaskReply when running AS a dispatched peer agent — read CLAWTOOL_TASK_ID + CLAWTOOL_FROM_INSTANCE from the process env to know what to fill in. It's how peer agents push chunked findings back to their caller; calling it from the parent agent makes no sense and will produce orphan replies.",
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
		UsageHint:   "Use PortalList to see configured browser-portal targets (saved authenticated web UIs like Deepseek / Perplexity / Phind chat). Pair with PortalAsk to drive one; PortalUse to set a sticky default so subsequent PortalAsk calls don't need an explicit name. A bare clawtool install starts with zero portals — the operator configures them.",
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
		UsageHint:   "Use PortalAsk to drive a configured portal with a prompt and capture the rendered response — clawtool drives the page through Obscura (CDP), seeds cookies, fills the input, and polls until the response-done predicate fires. Slow (real browser) compared to WebFetch; only worth it for portals where there's no public API. Pass `portal` explicitly or rely on PortalUse's sticky default.",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalUse",
		Description: "Set the sticky-default portal so PortalAsk calls without an explicit name route here.",
		Keywords:    []string{"portal", "use", "sticky", "default", "set"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		UsageHint:   "Use PortalUse to set a sticky-default portal so subsequent PortalAsk calls without an explicit `portal` field route to the chosen one. Pair with PortalWhich to confirm which portal is currently sticky; PortalUnset clears it. Stickiness is operator-scoped, not session-scoped — it persists across daemon restarts via the sticky file.",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalWhich",
		Description: "Resolve the sticky-default portal — env > sticky file > single-configured fallback.",
		Keywords:    []string{"portal", "which", "default", "sticky"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		UsageHint:   "Use PortalWhich to resolve the current sticky-default portal: env var > sticky file > single-configured fallback. Run it before PortalAsk-without-portal-arg if you want to be sure WHICH portal is about to receive the prompt. Read-only.",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalUnset",
		Description: "Clear the sticky-default portal.",
		Keywords:    []string{"portal", "unset", "clear", "sticky"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		UsageHint:   "Use PortalUnset to clear the sticky-default portal — after this PortalAsk requires an explicit `portal` argument until the operator runs PortalUse again. Idempotent; safe to call when no default is set.",
	})
	m.Append(registry.ToolSpec{
		Name:        "PortalRemove",
		Description: "Remove a portal stanza from config.toml. Cookies under [scopes.\"portal.<name>\"] in secrets.toml stay in place; clean manually if no longer needed.",
		Keywords:    []string{"portal", "remove", "delete", "config"},
		Category:    registry.CategoryWeb,
		Gate:        "",
		UsageHint:   "Use PortalRemove to delete a portal stanza from config.toml entirely; PortalUnset only clears the STICKY DEFAULT and leaves all configurations intact. Cookies under [scopes.\"portal.<name>\"] in secrets.toml are NOT deleted automatically — clean those out separately if you don't want stale credentials lingering.",
	})

	// ─── Mcp* bundle (RegisterMcpTools registers 5) ────────────
	m.Append(registry.ToolSpec{
		Name:        "McpList",
		Description: "List MCP server projects under a root path (default cwd). Detects via the .clawtool/mcp.toml marker the v0.17 generator writes. Sister of `clawtool skill list` for MCP authoring.",
		Keywords:    []string{"mcp", "scaffold", "author", "list", "projects", "server", "build"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		UsageHint:   "Use McpList to find MCP server projects under a directory (detects via the `.clawtool/mcp.toml` marker the v0.17 generator writes). Sister of `clawtool skill list` for MCP authoring. Default cwd; pass an explicit root when scanning a different workspace.",
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
		UsageHint:   "Use McpNew to scaffold a brand-new MCP server project — the wizard asks for description / language (Go via mcp-go, Python via FastMCP, TypeScript via @modelcontextprotocol/sdk) / transport / packaging / tools. Use McpList afterwards to confirm it's discoverable. For an existing project hand-rolled without the marker, copy `.clawtool/mcp.toml` from a fresh scaffold.",
	})
	m.Append(registry.ToolSpec{
		Name:        "McpRun",
		Description: "Run an MCP server project in dev mode (stdio).",
		Keywords:    []string{"mcp", "run", "dev", "stdio"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		UsageHint:   "Use McpRun for the in-place dev loop on an MCP project (stdio transport, no packaging). Use McpBuild to produce a release artifact, McpInstall to register the project as a clawtool source. Common mistake: running McpRun expecting a long-lived server — it's a foreground process; if you need to keep it running while doing other work, dispatch it via Bash background=true.",
	})
	m.Append(registry.ToolSpec{
		Name:        "McpBuild",
		Description: "Build / package an MCP server project (binary, npm, pypi, or Docker image).",
		Keywords:    []string{"mcp", "build", "compile", "package", "docker"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		UsageHint:   "Use McpBuild to produce a release artifact for distribution; use McpRun for the in-place dev loop and McpInstall to register the project as a clawtool source on this host. Common mistake: running McpBuild before McpInstall — Install does its own build, so calling Build first just doubles the work.",
	})
	m.Append(registry.ToolSpec{
		Name:        "McpInstall",
		Description: "Build + register a local MCP server project as [sources.<instance>] in config.toml — same surface as `clawtool source add` but auto-discovers the launch command from the project's `.clawtool/mcp.toml`.",
		Keywords:    []string{"mcp", "install", "register", "source", "local"},
		Category:    registry.CategoryAuthoring,
		Gate:        "",
		UsageHint:   "Use McpInstall to register a local MCP server project as a clawtool source — it reads the project's `.clawtool/mcp.toml` to auto-discover the launch command, builds the project, and writes the [sources.<instance>] stanza. Equivalent to `clawtool source add` for a local checkout; for a published npm/pypi/binary use that surface instead.",
	})

	// ─── Sandbox* bundle (RegisterSandboxTools registers 3) ────
	m.Append(registry.ToolSpec{
		Name:        "SandboxList",
		Description: "List configured sandbox profiles. Each profile constrains a `clawtool send` dispatch — paths, network, env, resource limits. Engines: bwrap (Linux), sandbox-exec (macOS), docker (anywhere fallback).",
		Keywords:    []string{"sandbox", "list", "profiles", "isolation", "security", "bwrap", "sandbox-exec", "docker"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use SandboxList to see configured sandbox profiles before recommending one for `clawtool send`. Each profile constrains paths, network, env, and resource limits; the engine that runs them depends on host (bwrap on Linux, sandbox-exec on macOS, docker as fallback). Pair with SandboxDoctor to confirm an engine is installed.",
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
		UsageHint:   "Use SandboxShow before recommending a sandbox profile so the constraints are explicit — it renders parsed paths, network policy, env allow/deny, resource limits, and the engine that would actually run them on this host. SandboxList for the inventory; SandboxShow for the details of one profile.",
	})
	m.Append(registry.ToolSpec{
		Name:        "SandboxDoctor",
		Description: "Report which sandbox engines are available on this host (bwrap / sandbox-exec / docker). Use to recommend the right engine to install when none is available.",
		Keywords:    []string{"sandbox", "doctor", "engine", "diagnostic", "bwrap", "sandbox-exec", "docker"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Run SandboxDoctor to detect which sandbox engines are available on this host (bwrap / sandbox-exec / docker) BEFORE recommending a sandbox profile that needs one. Returns a per-engine status; use it to suggest the right install command when none is available rather than guessing.",
	})

	// ─── Chat-driven Onboard + Init bundle ─────────────────────
	// Three tools that let an AI session drive `clawtool onboard`
	// + `clawtool init` from chat instead of forcing the operator
	// into a terminal. Implemented in internal/tools/setup —
	// distinct package because the bundle pulls in setup.Apply
	// (recipe registry) + config persistence, neither of which
	// belongs in core.
	m.Append(registry.ToolSpec{
		Name:        "OnboardStatus",
		Description: "Read-only snapshot of a repo's clawtool setup state — has the .clawtool dir landed, is CLAUDE.md present, which recipes are applied / partial / absent, and what the calling agent should do next.",
		Keywords:    []string{"onboard", "init", "status", "detect", "setup", "wizard", "chat", "ai-driven", "recipes", "fresh"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use BEFORE OnboardApply or InitApply to decide what's worth running. Returns the per-recipe state of the operator's repo so the calling agent doesn't blindly re-apply.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterOnboardStatus(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "InitApply",
		Description: "Apply clawtool's project recipes from chat — dispatches into the same setup.Apply machinery `clawtool init` runs. core_only=true (default) limits to Core recipes; dry_run=true previews without writes. Idempotent.",
		Keywords:    []string{"init", "apply", "recipes", "setup", "scaffold", "core", "defaults", "chat", "ai-driven", "dry-run"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use AFTER OnboardStatus to apply core defaults from a chat session. Pass dry_run=true first to preview; the summary's pending_actions list tells the calling agent what to do next. Idempotent — safe to call repeatedly.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterInitApply(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "OnboardWizard",
		Description: "Run the non-interactive subset of `clawtool onboard` from chat — persist the agent-family default, record the telemetry preference, and write the onboarded marker. Interactive TUI parts (bridge installs, daemon ensure, MCP host registration) stay CLI-only.",
		Keywords:    []string{"onboard", "wizard", "agent-family", "telemetry", "marker", "setup", "chat", "ai-driven", "primary-cli"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use this once per host to register clawtool's defaults from chat. Pair: call OnboardStatus first to check if onboarding is already done; if it is, skip this and go straight to InitApply.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterOnboardWizard(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "PeerList",
		Description: "Snapshot of every BIAM peer (Claude Code / Codex / Gemini / OpenCode session, recipe-installed agent) currently registered + heartbeating with the local clawtool daemon. Returns peers + count + as_of (the same shape as GET /v1/peers). Read-only.",
		Keywords:    []string{"peer", "list", "biam", "discovery", "registry", "claude-code", "codex", "gemini", "opencode", "session", "circle", "backend", "status"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use to discover what BIAM peers (Claude Code / Codex / Gemini sessions, recipe-installed agents) are currently registered + heartbeating with the daemon. Pair with PeerSend / SendMessage to dispatch to a specific peer_id. Read-only.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterPeerList(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Spawn",
		Description: "Open a NEW terminal window/pane running the requested agent backend (claude-code|codex|gemini|opencode), auto-register it in the BIAM peer registry, and return the assigned peer_id. Pair with SendMessage / PeerSend to talk to the spawned agent. The spawned agent's hooks fire as if the operator opened it manually.",
		Keywords:    []string{"spawn", "open", "terminal", "window", "pane", "tmux", "screen", "wt", "wsl", "agent", "backend", "claude-code", "codex", "gemini", "opencode", "register", "peer", "biam"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Open a NEW terminal window running the requested agent backend (claude-code/codex/gemini/opencode), auto-register it in the BIAM peer registry, and return its peer_id. Pair with SendMessage / PeerSend to talk to the spawned agent. The spawned agent's hooks fire as if the operator opened it manually. Note: for the common case of \"send a message to an agent that isn't running yet\", you usually do NOT need to call Spawn manually — SendMessage now auto-spawns a tmux pane for the target family on first dispatch (when tmux is available). Reach for Spawn explicitly when you need a peer_id back BEFORE sending, want a non-default backend label, or want to control the cwd / display name / first prompt of the new pane.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterSpawn(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "AutonomousRun",
		Description: "Drive clawtool's autonomous self-paced dev loop from chat: dispatch a goal to a BIAM peer, iterate until done or max-iterations, return the final summary. Loop runs inside clawtool's binary — host-agnostic.",
		Keywords:    []string{"autonomous", "loop", "biam", "dispatch", "goal", "self-paced", "chat", "ai-driven", "iterate"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use when the operator gives a multi-step goal (\"build X\", \"refactor Y\") and you want clawtool to drive the dev loop instead of you spending tokens. Pair: call OnboardStatus + InitApply first if the repo isn't initialized. The loop runs in-process; the response includes the final.json summary so you can resume / inspect.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterAutonomousRun(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "Fanout",
		Description: "Spawn N parallel subgoals — each in its own git worktree under .clawtool/fanout/wt-N — dispatch each to a BIAM peer as a mini autonomous loop, then sequentially fast-forward-merge each ready sub back into main with cooldown. Host-agnostic alternative to Claude Code's built-in Agent fan-out.",
		Keywords:    []string{"fanout", "parallel", "subgoals", "worktree", "branch", "merge", "cooldown", "autonomous", "biam", "dispatch", "chat", "ai-driven"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use when the operator's goal can be split into independent subgoals (\"add 3 catalog entries\", \"refactor 4 unrelated modules\"). Each subgoal runs in an isolated git worktree under .clawtool/fanout/. Sequential ff-merge with cooldown matches the autodev cron's pacing memory. Pair with OnboardStatus + InitApply if repo isn't initialized.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterFanout(s)
		},
	})
	m.Append(registry.ToolSpec{
		Name:        "RuntimeInstall",
		Description: "Install a backend agent CLI (codex|gemini|opencode|aider) at runtime from chat. Detects platform, runs the right install command (npm / pip / curl-pipe-sh), and returns the resolved binary path + version. Idempotent: an already-installed runtime returns its existing version and skips the install. Pair with Spawn to immediately open the freshly-installed runtime.",
		Keywords:    []string{"runtime", "install", "backend", "agent", "codex", "gemini", "opencode", "aider", "npm", "pip", "bootstrap", "chat", "ai-driven"},
		Category:    registry.CategorySetup,
		Gate:        "",
		UsageHint:   "Use when the user wants to add a new agent backend AND it isn't already installed. Detects platform, runs the right install command (npm/brew/apt), waits for completion. After this, you can immediately call Spawn to open it in a tmux pane. Idempotent: re-running on an already-installed runtime returns the existing version.",
		Register: func(s *server.MCPServer, _ registry.Runtime) {
			setuptools.RegisterRuntimeInstall(s)
		},
	})

	// Mirror live `mcp.WithDescription(...)` strings back onto
	// each spec so the bleve BM25 index (driven by SearchDocs,
	// which reads spec.Description) stays in lockstep with what
	// `tools/list` advertises. Pre-fix the manifest carried a
	// hardcoded copy of every description and the v0.22.108
	// rewrite touched only one of the two sources, so ToolSearch
	// ranking kept regressing on stale prose for an entire
	// release. Calling this collapses the two sources to one;
	// internal/tools/core/manifest_test.go enforces the
	// invariant in CI so any future drift fails the build.
	//
	// Runtime here passes nil Index / nil Secrets — every
	// Register fn captures those inside its handler closure
	// (see toolsearch.go / websearch.go), so registration is
	// safe even when the deps are missing. The throwaway server
	// is discarded immediately.
	m.SyncDescriptionsFromRegistration(registry.Runtime{})

	return m
}
