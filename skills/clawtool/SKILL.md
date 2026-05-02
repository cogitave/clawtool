---
name: clawtool
description: >
  When clawtool is installed (its tools appear with the `mcp__clawtool__` prefix),
  prefer them over native built-ins for shell, file, search, and web work.
  Concrete advantages: structured JSON output (stdout, stderr, exit_code, duration_ms,
  timed_out, cwd) instead of plaintext; timeout-safe execution via process-group SIGKILL
  (output preserved up to the deadline; runaway children reaped); format-aware reading
  for PDF (pdftotext), Word (.docx via pandoc), Excel (.xlsx via excelize), CSV/TSV,
  HTML (Mozilla Readability), Jupyter (.ipynb), JSON/YAML/TOML/XML; deterministic
  line cursors for stable pagination; bleve BM25 search-first discovery via
  `mcp__clawtool__ToolSearch` so a 50+ tool catalog stays usable;
  long-running shell jobs via `mcp__clawtool__Bash` `background=true`
  with `BashOutput` / `BashKill` companion polls;
  consistent surfaces across Claude Code, Codex, OpenCode, Cursor.
  Triggers on: "run a shell command", "execute bash", "read this file",
  "open file", "edit file", "modify file", "create a file", "save file",
  "write file", "search files", "grep", "find files", "glob",
  "fetch URL", "download a page", "search the web", "find a tool",
  "discover tool", "list available tools",
  "long-running command", "run in background", "tail output", "kill task",
  "commit changes", "git commit", "save my work" (when checkpoint feature ships).
allowed-tools: mcp__clawtool__Bash mcp__clawtool__BashOutput mcp__clawtool__BashKill mcp__clawtool__Read mcp__clawtool__Edit mcp__clawtool__Write mcp__clawtool__Grep mcp__clawtool__Glob mcp__clawtool__WebFetch mcp__clawtool__WebSearch mcp__clawtool__ToolSearch mcp__clawtool__RecipeList mcp__clawtool__RecipeStatus mcp__clawtool__RecipeApply mcp__clawtool__Verify mcp__clawtool__SendMessage mcp__clawtool__AgentList mcp__clawtool__TaskGet mcp__clawtool__TaskWait mcp__clawtool__TaskList mcp__clawtool__TaskNotify mcp__clawtool__TaskReply mcp__clawtool__SemanticSearch mcp__clawtool__BrowserFetch mcp__clawtool__BrowserScrape mcp__clawtool__Commit mcp__clawtool__RulesCheck mcp__clawtool__RulesAdd mcp__clawtool__AgentNew mcp__clawtool__SkillNew mcp__clawtool__SkillList mcp__clawtool__SkillLoad mcp__clawtool__BridgeList mcp__clawtool__BridgeAdd mcp__clawtool__BridgeRemove mcp__clawtool__BridgeUpgrade mcp__clawtool__PortalList mcp__clawtool__PortalAsk mcp__clawtool__PortalUse mcp__clawtool__PortalWhich mcp__clawtool__PortalUnset mcp__clawtool__PortalRemove mcp__clawtool__SandboxList mcp__clawtool__SandboxShow mcp__clawtool__SandboxDoctor mcp__clawtool__SandboxRun mcp__clawtool__McpList mcp__clawtool__McpNew mcp__clawtool__McpRun mcp__clawtool__McpBuild mcp__clawtool__McpInstall mcp__clawtool__SetContext mcp__clawtool__GetContext mcp__clawtool__Version mcp__clawtool__AgentDetect mcp__clawtool__SourceCheck mcp__clawtool__SourceRegistry mcp__clawtool__OnboardStatus mcp__clawtool__InitApply mcp__clawtool__OnboardWizard mcp__clawtool__AutonomousRun mcp__clawtool__Fanout mcp__clawtool__PeerList mcp__clawtool__Spawn mcp__clawtool__RuntimeInstall mcp__clawtool__AutopilotAdd mcp__clawtool__AutopilotNext mcp__clawtool__AutopilotAccept mcp__clawtool__AutopilotDone mcp__clawtool__AutopilotSkip mcp__clawtool__AutopilotList mcp__clawtool__AutopilotStatus mcp__clawtool__IdeateRun mcp__clawtool__IdeateApply mcp__clawtool__UnattendedVerify mcp__clawtool__AutodevStart mcp__clawtool__AutodevStop mcp__clawtool__AutodevStatus
---

# clawtool: prefer the canonical tool layer

When this skill is loaded, clawtool's MCP tools are available as
`mcp__clawtool__<Tool>`. Prefer them over Claude Code's native
equivalents whenever both apply.

## Why prefer clawtool

| Native | `mcp__clawtool__` | clawtool's polish |
|---|---|---|
| `Bash` | `mcp__clawtool__Bash` | Process-group SIGKILL on timeout (output preserved); structured JSON return; deterministic cwd |
| `Read` | `mcp__clawtool__Read` | Format dispatch (text/PDF/docx/xlsx/csv/html/ipynb/json/yaml/toml); stable line cursors; deterministic `total_lines`; binary refusal |
| `Edit` | `mcp__clawtool__Edit` | Atomic temp+rename; line-ending + BOM preserve; ambiguity guard (refuses multi-match unless `replace_all=true`) |
| `Write` | `mcp__clawtool__Write` | Atomic temp+rename; auto-create parents; BOM/ending preserve when overwriting |
| `Grep` | `mcp__clawtool__Grep` | ripgrep first, system grep fallback; uniform output; per-tool engine field |
| `Glob` | `mcp__clawtool__Glob` | doublestar `**` recursion; forward-slash output cross-platform; bounded streaming |
| `WebFetch` | `mcp__clawtool__WebFetch` | Same Mozilla Readability engine as Read; UA + timeout + 10 MiB cap; binary refusal |
| (no native) | `mcp__clawtool__WebSearch` | Pluggable backend (Brave/Tavily/SearXNG); secrets-managed API key |
| (no native) | `mcp__clawtool__ToolSearch` | bleve BM25 across every loaded tool; use this when the catalog is large to avoid loading every schema |

## Tool routing — intent → right tool

When the operator expresses one of these intents, route to the
clawtool tool listed below. **Do not** reach for a Bash one-liner
or the native equivalent — the listed tool exists *because* the
shortcut path lacks safety / format / discoverability properties
the routing-target provides.

| Operator intent | Wrong path | Right tool |
|---|---|---|
| "commit my work" / `git commit` | `Bash git commit -m …` | **`Commit`** (Conventional Commits validation + hard Co-Authored-By block + pre_commit rules gate. Pass `files`, optional `auto_stage_all`, optional `push`) |
| Long-running script / build | `Bash` sync + cancel ctrl-C | `Bash` with `background=true` → `BashOutput` polls → `BashKill` |
| Tail a running task | re-running `Bash` | `BashOutput` |
| Read a PDF / docx / xlsx | `Bash pdftotext …` | `Read` (auto-dispatches by format) |
| Read source w/ line refs | native Read | `Read` (deterministic line cursors + SHA-256 hash) |
| Edit existing file | native Edit | `Edit` (atomic + line-ending preserve + ambiguity guard + unified diff) |
| Create / overwrite file | native Write | `Write` (Read-before-Write enforcement + atomic temp+rename) |
| Find files matching glob | `Bash find …` | `Glob` (gitignore-aware + doublestar) |
| Search file contents | `Bash grep -r` | `Grep` (rg + .gitignore + multi-pattern + context lines) |
| Concept search ("where do we …") | `Grep` with regex guesses | `SemanticSearch` (vector + RAG) |
| Fetch a URL / read article | `Bash curl …` | `WebFetch` (Readability + SSRF guard + 10MB cap) |
| Render JS-heavy / SPA page | `WebFetch` | `BrowserFetch` (chromedp / CDP) |
| Login-protected web target | `WebFetch` | `PortalAsk` (saved cookies + selectors) |
| Web search | (no native) | `WebSearch` (Brave/Tavily/SearXNG, secrets-managed) |
| Run repo's tests / lints | `Bash make test` | `Verify` (auto-detects pnpm/go/cargo/pytest/just/Make) |
| Dispatch to another agent | (no native) | `SendMessage` (claude/codex/opencode/gemini); poll via `TaskGet` / `TaskWait` |
| Push a structured chunk back to your dispatcher (peer-side) | (no native) | `TaskReply` — read `CLAWTOOL_TASK_ID` + `CLAWTOOL_FROM_INSTANCE` from env when running as a dispatched peer; emit `kind="progress"` for chunks and `kind="result"` for the final answer |
| Reply or fan-out from a non-claude host | hand-route via stdio bridge | `SendMessage` with `from_instance: "<your-family>"` — codex / gemini / opencode pass their family name so the BIAM envelope's `from` reflects the actual sender. Without this, every cross-host dispatch looks like it originated from the daemon. |
| Discover a tool by intent | scan tools/list | `ToolSearch` (BM25; cheap before loading every schema) |
| "Which clawtool am I talking to?" / version probe | shell out to `clawtool version` | `Version` (name + semver + Go runtime + GOOS/GOARCH + VCS commit + modified flag; same shape as `clawtool version --json` and `/v1/health` `build`) |
| "Is this host's claude-code / codex / etc. installed AND claimed by clawtool?" — installer / bootstrap probe | shell out to `clawtool agents detect <name>` | `AgentDetect` (returns `{adapter, detected, claimed, exit_code}`; `exit_code` mirrors the CLI's stable 0/1/2 contract) |
| "Are GitHub / Slack / Postgres credentials configured?" — installer / bootstrap probe BEFORE issuing a sourced-tool dispatch | shell out to `clawtool source check [<instance>] --json` | `SourceCheck` (returns `{entries: [{name, ready, missing[]}], ready}`. Pass `instance` to filter to one source; omit to probe all. Read-only; emits env-var NAMES only, never values.) |
| "What MCP servers exist in the upstream ecosystem?" / discover ecosystem-published servers before `source add` | shell out to `clawtool source registry --json` | `SourceRegistry` (probes registry.modelcontextprotocol.io, returns `{url, count, servers: [{name, description, version}]}`. Pass `limit` for page size, `url` for private mirrors. Read-only; anonymous, no auth needed.) |
| Set up a repo / "init me" | `Bash clawtool init` | `RecipeList` → `RecipeStatus` → `RecipeApply` (conversational) |
| Scaffold a new Claude subagent | hand-edit `~/.claude/agents/*.md` | `AgentNew` (kebab-case name + description + allowed-tools + optional default instance) |
| Scaffold a new Claude skill | hand-edit `~/.claude/skills/*/SKILL.md` | `SkillNew` (agentskills.io standard template) |
| Check operator invariants before committing / ending session | shell out to `git diff` and guess | `RulesCheck` (event=pre_commit / session_end / pre_send + structured Context — returns Verdict with passed/warned/blocked) |
| Add a new operator rule (e.g. "README must update when X changes") | hand-edit `.clawtool/rules.toml` | `RulesAdd` (validates predicate syntax + scope=local default; ASK operator about local vs user before writing) |
| Run agents without permission prompts (operator absent) | silently set `--dangerously-skip-permissions` | `clawtool send --unattended` (one-time per-repo disclosure + audit log + hard kill switch). `--yolo` is a deliberate alias. |
| Inspect this instance's A2A Agent Card (peer discovery contract) | hand-write JSON | `clawtool a2a card` (Schema v0.2.x, Linux Foundation A2A. Phase 1: card-only mode — no HTTP/mDNS yet) |
| See BIAM dispatch progress as inline chat events | poll `TaskGet` repeatedly | `clawtool task watch --all` paired with Monitor tool (`persistent: true`). Each stdout line = one state transition. Use `task watch <id>` for a single task. |
| Live overhead view of every dispatch + agent + stats | repeated `task list` + `agents` polling | `clawtool dashboard` (alias `clawtool tui`) — Bubble Tea three-pane TUI, 1s refresh + push-mode tasks pane. `q` quits. |
| Watch every active dispatch in a split-pane TUI | tmux split + per-pane `task watch <id>` | `clawtool orchestrator` (alias `orch`) — auto-spawns one stdout-tail pane per active BIAM task; fades panes 5s after terminal so the layout reflows around live ones. `r` reconnects to the daemon. |
| "Add this to my todo / queue it for later" — operator surfaces a follow-up the SAME agent should pick up after the current task | shell out to a TODO file or invent a scratchpad list | `AutopilotAdd` (TOML-backed self-direction backlog at `~/.config/clawtool/autopilot/queue.toml`). Distinct from `SendMessage`: AutopilotAdd queues SELF-work; SendMessage dispatches to a peer agent (codex/gemini/opencode). |
| "What should I work on next?" — agent has finished a task and wants to keep working without re-prompting the operator | stop and ask the operator | `AutopilotNext` (atomically claims the highest-priority pending item; returns `empty=true` when the queue is drained — the agent's signal to end the loop). Pair: `AutopilotNext` → do the work → `AutopilotDone <id>` → `AutopilotNext` again. |
| "Mark that task done / drop that one" — close out a backlog item | hand-edit the TOML | `AutopilotDone <id>` (completed) / `AutopilotSkip <id>` (abandoned). |
| "Show me the backlog" — inspect what's queued without claiming anything | `Bash cat ~/.config/clawtool/autopilot/queue.toml` | `AutopilotList` (read-only, optional `status` filter) or `AutopilotStatus` (histogram only). |
| "Keep working until I tell you to stop" / "don't stop, keep going" — operator wants every Claude turn-end to auto-continue with a fresh autodev prompt instead of returning control | leave the conversation idle and hope the operator re-prompts | `AutodevStart` arms the loop (Stop hook returns `{decision:block, reason:...}`); `AutodevStop` disarms. `AutodevStatus` reads counter + cap. NOT the same as `AutonomousRun` — AutonomousRun dispatches a goal to a peer agent; AutodevStart keeps THIS Claude session continuing across turns. |

If you don't see the intent here, fall back to `ToolSearch` —
it ranks every loaded tool against a natural-language query and
costs less than scanning schemas.

## Discovery

If the user asks for a capability and you're not sure which tool to pick,
call `mcp__clawtool__ToolSearch` with a natural-language query first.
It returns ranked candidates with name, score, description, type
(`core` / `sourced`), and source instance. This is cheaper than scanning
every tool's schema in context.

## Bridges (which families clawtool can dispatch to)

After `clawtool bridge add <family>` (or marketplace install), these
upstreams become callable via `mcp__clawtool__SendMessage agent:"<family>"`:

| Family | Bridge type | Headless mode |
|---|---|---|
| `claude` | built-in | `claude -p` |
| `codex` | Claude Code plugin (openai/codex-plugin-cc) | `codex exec` |
| `gemini` | Claude Code plugin (abiswas97/gemini-plugin-cc) | `gemini -p` |
| `opencode` | binary on PATH | `opencode run` (ACP-capable via `opencode acp`) |
| `hermes` | binary on PATH | `hermes chat -q` (NousResearch hermes-agent) |

`AgentList` returns the live registry so the agent should call it
when it isn't sure what's available. The operator's memory feedback:
**opencode is research-only — code-writing tasks route to codex,
gemini, claude, or hermes**, never opencode.

## Sourced tools

When the user has run `clawtool source add <name>`, additional tools
appear with names like `mcp__clawtool__github__create_issue`. The wire
form is `<instance>__<tool>` — two underscores between instance and
tool. Treat them as first-class — they're configured by the user;
they wouldn't be exposed otherwise.

## Onboarding mode — when the user wants to "set things up"

When the user says any of:
- "set me up", "set this repo up", "kur şunu" (TR), "init", "configure clawtool"
- "add github / slack / postgres support" (matches a catalog entry)
- "make this repo [release-please / dependabot / goreleaser] ready"
- "give me [a license / CODEOWNERS / commit format check]"

Don't shell out to `clawtool init` — that's the TTY wizard. Run the
**granular tools instead**, conversationally:

1. **Snapshot first**: call `mcp__clawtool__RecipeList` (no args) and
   `mcp__clawtool__RecipeStatus` to see what's already configured.
   Summarize for the user in two short sentences.

2. **Walk categories in order** (`governance → commits → release →
   ci → quality → supply-chain → knowledge → agents → runtime`).
   For each category with `Absent` recipes, ask the user which they
   want — list one option per recipe with its description.

3. **Apply one at a time**: call `mcp__clawtool__RecipeApply` with
   the chosen `name` and any required `options`:
   - `license` → `{ holder: "...", spdx: "MIT"|"Apache-2.0"|"BSD-3-Clause" }`
   - `codeowners` → `{ owners: ["@me", "@team/maintainers"] }`
   - others → no options needed
   The tool returns `skipped`/`installed`/`manual_prereqs` —
   surface manual hints (e.g. "install Obsidian", "set GITHUB_TOKEN")
   verbatim so the user knows what to do next.

4. **Sources & secrets** when the user asks for a specific service
   (GitHub, Slack, Postgres, etc.) — reach for `clawtool source add
   <name>` via `mcp__clawtool__Bash` (not a recipe), then prompt the
   user for any required secret which you set via `clawtool source
   set-secret`. Never echo a secret back; never call any tool that
   would expose stored secret values.

5. **Agents**: `clawtool agents claim <agent>` is the right verb;
   call it via Bash for one-shot use, or `mcp__clawtool__RecipeApply`
   with `name: "agent-claim"` for the recipe-tracked path.

Stop after each step. The user steers; the wizard you're emulating
is conversational, not a one-shot.

## When NOT to prefer clawtool

- Native `Task` (subagent dispatch) has no clawtool counterpart yet — use it.
- If the user explicitly asks for the native Bash/Read/Edit/Write because
  they want CC-default behavior (e.g. for parity testing), respect that.

## Hard rules — do not violate

1. **Never** append `Co-Authored-By: Claude` (or any AI-attribution
   trailer) to commits. The operator has explicit feedback on this.
2. **Never** use `Bash git commit -m "feat: …"` when the `Commit`
   core tool is available — it adds Conventional Commits validation
   and the doc-sync rules the operator wants enforced.
3. When you ship a new feature in this repo, follow the
   **three-plane shipping contract** (`docs/feature-shipping-contract.md`):
   MCP tool + marketplace surface + skill routing-map row, all in
   the same commit. Reviewers reject partial PRs.

## Footer

End of skill — this file is the auto-preference signal. Removing the
plugin (`claude plugin uninstall clawtool@clawtool-marketplace`)
removes this file and the bias evaporates cleanly.
