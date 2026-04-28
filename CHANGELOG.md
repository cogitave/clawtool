# Changelog

All notable changes to clawtool are documented here. Format adheres to
[Conventional Commits](https://www.conventionalcommits.org/) and this
project follows [Semantic Versioning](https://semver.org/) — see
ADR-009 for the policy details.

## [0.22.4] - 2026-04-28

### Features

- **telemetry:** Emit clawtool.install event once per fresh host (96a631a)
### Fixes

- **biam:** Summary lifts NDJSON agent_message text instead of thread.started header (fccbea5)## [0.22.3] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.2 [skip ci] (2ec9f0f)
### Features

- **plugin:** SessionStart auto-bootstrap hook — clawtool engages on first prompt of a fresh Claude Code session (83afb7d)## [0.22.2] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.1 [skip ci] (b752be6)
### Features

- **source:** Add `clawtool source rename` verb (alias `mv`) (2431c15)
### Fixes

- **tui:** Reap orphan tasks at daemon boot + drop stale snapshots from live UIs (f0105f6)## [0.22.1] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.0 [skip ci] (d340fd0)
### Features

- **tui:** Orchestrator Phase 3 — live byte stream + theme + sidebar layout (5e76d75)
- **telemetry:** Expand event coverage + pre-1.0 default-on consent (bb00e1b)
- **telemetry:** Bake cogitave PostHog defaults so opt-in Just Works (9de8e2e)
### Tests

- **biam:** Cover stream-frame broadcasting + watchsocket envelope multiplex (74b4a76)## [0.22.0] - 2026-04-28

### CI

- **integration:** Drop setup-node `cache: npm` — no lockfile in a Go repo (fd2b03e)
### Chores

- **rules:** Add race-clean pre_commit rule (5da4187)
- **rules:** Add gofmt-clean pre_commit rule (9b61a38)
### Documentation

- **changelog:** Regenerate for v0.21.7 [skip ci] (289958e)
### Features

- **tui:** Orchestrator Phase 2 — split-pane streaming TUI per dispatch (718107b)
- **cli:** Setup wizard Phase 2 — single huh form + per-feature matrix (aa585bf)
- **tui:** Orchestrator Phase 1 — dashboard subscribes to task-watch socket (7d5181b)
- **cli:** Clawtool setup — unified first-run entry (Phase 1) (cbc5bda)
- **biam:** Cross-host bidi via from_instance — codex/gemini/opencode can dispatch back (be7a5fa)
- **biam:** Push-based task watch via Unix socket — kill the 250ms poll (592ff37)
### Refactor

- **ux:** Strip internal doc IDs from user-facing surfaces (cabd434)
### Style

- Gofmt across all sources (6524b46)
### Tests

- **biam:** Fix data race in HonoursFromInstance — submit before goroutine (59b302f)## [0.21.7] - 2026-04-28

### Chores

- **release:** V0.21.7 — UX polish (overview + doctor sandbox-worker + ambiguity) (b25eed3)
### Documentation

- **onboard:** Surface sandbox-worker setup hint (ADR-029) (387e65d)
### Features

- **cli:** `clawtool overview` — one-screen system status (ca98eb7)
- **doctor:** Sandbox-worker section + guided agent-ambiguity error (ddeb308)## [0.21.6] - 2026-04-28

### Chores

- **release:** V0.21.6 — claude.ai sandbox parity (ADR-029) (a6b841f)
### Documentation

- **changelog:** Regenerate for v0.21.5 [skip ci] (9f6c33c)
### Features

- **egress:** Allowlist proxy binary (ADR-029 phase 4, #209) (ccd809b)
- **skill:** SkillList + SkillLoad — on-demand mount (ADR-029, #208) (44ee058)
- **sandbox:** Worker phase 2 — daemon-side routing for Bash (ADR-029) (b2f42d8)
- **sandbox:** Worker container — claude.ai parity (ADR-029 phase 1) (cf6f2c2)
- **doctor:** Surface daemon state (UX smoke pass #193) (68a8311)## [0.21.5] - 2026-04-27

### Chores

- **release:** V0.21.5 — Codex c1b00f10 audit fixes (security) (613e1d0)
### Documentation

- Clean stale "phase X lands later" comments (audit #206) (2d66cfa)
- **changelog:** Regenerate for v0.21.4 [skip ci] (51b4362)
### Features

- **biam:** Runner.Cancel + true async + `clawtool task cancel` (audit #204) (98de7d0)
- **agents:** Per-instance secrets-store env injection (audit #205) (23f4f7a)
### Fixes

- **sandbox:** Bwrap fail-closes when policy can't be enforced (audit #203) (3d60f2c)
- **sandbox:** Per-call resolution fail-closed (audit #202) (6c8fb55)
- **unattended:** Inject elevation flags into upstream CLI args (ADR-023) (5ba2370)## [0.21.4] - 2026-04-27

### Chores

- **release:** V0.21.4 — shared MCP fan-in + onboard wiring (b56440c)
### Features

- **onboard:** Wire MCP host claim + add hermes detection (36ab6a0)
- **agents:** Shared HTTP MCP fan-in via persistent daemon (codex/gemini) (b71bca5)
- **rules:** `clawtool rules` CLI surface + RulesAdd MCP tool (7f181bc)
### Fixes

- **tui:** Dashboard live tick + viewport-aware + plain mode (operator feedback) (0e351eb)
- **commit:** Populate ChangedPaths from staged index before rules eval (389bbd0)## [0.21.3] - 2026-04-27

### CI

- Bump every action to @v6 + fix dependabot Conventional-Commits prefix (e49b589)
### Chores

- **release:** V0.21.3 — TUI dashboard + release.yml CHANGELOG fix (c3ac2ea)
### Features

- **tui:** Clawtool dashboard — three-pane Bubble Tea runtime view (40ef761)
### Fixes

- **release:** Re-invoke git-cliff action for CHANGELOG regen step (d9f6c90)## [0.21.2] - 2026-04-27

### Chores

- **release:** V0.21.2 — re-tag (v0.21.1 trigger missed) (fabf572)## [0.21.1] - 2026-04-27

### Chores

- **release:** V0.21.1 — CHANGELOG auto-regen + sandbox dispatch + task watch + Hermes plugin fix (2fa6416)
### Features

- **task:** `clawtool task watch` — stream BIAM transitions to Monitor (ADR-026) (e057ba9)
- **supervisor:** Sandbox dispatch integration (#163 closes) (0c362c4)
### Fixes

- **surface:** Skill allowed-tools covers manifest + plugin includes hermes (abec5aa)## [0.21.0] - 2026-04-27

### Chores

- **release:** V0.21.0 — Tool Manifest Registry + A2A phase 1 + release plumbing (dcc85ca)
### Features

- **registry:** Step 4 — server.go flip + 30/30 tools manifest-driven (#173 closes) (1f0fb64)
- **registry:** Step 3a — 12 individual-Register tools join the manifest (#173) (a0dccc4)
- **registry:** Step 2 — typed manifest entries for 6 newest tools (#173) (bcf6a9e)
- **registry:** Typed ToolSpec manifest — Step 1 of #173 (Codex's #1 ROI refactor) (8206450)
- **a2a:** Phase 1 — Agent Card serializer + `clawtool a2a card` (ADR-024) (c35328a)
### Tests

- **version:** Release pipeline regression tests (2952842)## [0.20.2] - 2026-04-27

### Fixes

- **release:** V0.20.2 — go-selfupdate compat + retire Release Please (0f36d89)## [0.20.1] - 2026-04-27

### Documentation

- **readme:** Drop dead ADR links — wiki/ is gitignored (d071f3d)
### Fixes

- **release:** V0.20.1 — gitignore BODY.md so GoReleaser stops tripping (4b2e677)## [0.20.0] - 2026-04-27

### CI

- Bump Go to 1.26.0 (chromedp dep requires it) (4ab2eaf)
### Chores

- **release:** V0.20.0 — multi-agent supervisor + checkpoint + rules + unattended (bd4a704)
### Documentation

- **readme:** Full rewrite — "Tools. Agents. Wired." tagline + complete tool table (bb3811f)
- **plugin:** Adopt 'Tools. Agents. Wired.' tagline (1099ae5)
- **plugin:** Refresh About — canonical tool layer + multi-agent supervisor (ee17735)
- Three-plane feature shipping contract + SKILL.md routing map (cf43c92)
- **http:** Add docs/http-api.md + README link — Postman & cURL recipes (c45132c)
- **readme:** V0.14 / v0.15 surface — BIAM, bridges, send --async, worktree, upgrade (498a241)
### Features

- **unattended:** --unattended flag + per-repo trust + JSONL audit (ADR-023 phase 1) (474fa97)
- **checkpoint:** Commit core tool — Conventional Commits + Co-Authored-By block + rules gate (ADR-022 phase 1) (a9452be)
- **rules:** Predicate-based invariant engine + RulesCheck tool (9421e8c)
- **bridges:** Hermes-agent — fifth supported family (NousResearch, MIT, 120K stars) (16313bf)
- **agent:** User-defined personas — `clawtool agent new` + AgentNew tool (12c701c)
- **biam:** TaskNotify — edge-triggered fan-in completion push (9152d3d)
- **bash:** Background mode + BashOutput / BashKill (ADR-021 phase B) (3e9a055)
- **websearch:** Provider-neutral filter shape — domains / recency / country / topic (1ea710d)
- **v0.18.6:** Core tools polish phase B — Glob .gitignore + WebFetch SSRF guard (ab1647c)
- **v0.18.1:** Bwrap engine real Wrap — Profile→argv compiler + live sandbox enforcement (01cd88e)
- **v0.18.4:** Core tools polish phase A — Read hashes, Write Read-before-Write, Edit diff (ADR-021) (ec2dd44)
- Dockerize clawtool — 15MB distroless static image + Compose stack (0713937)
- **v0.18:** Clawtool sandbox surface + ADR-020 (bwrap/sandbox-exec/docker) (8c81e37)
- Clawtool uninstall — full footprint cleanup (ce9bed7)
- **v0.17:** Clawtool mcp generator — Go / Python / TypeScript scaffolds (b6a3359)
- **v0.16.4:** Clawtool mcp authoring noun + surface (ADR-019) (8301353)
- **v0.16.3:** Portal add interactive wizard (chromedp + Chrome) (3532ffa)
- **v0.16.2:** Portal CDP driver — Ask flow + per-portal MCP aliases (8067955)
- **v0.16.1:** Portal feature — saved web-UI targets (ADR-018) (0171284)
- **v0.16:** BrowserFetch + BrowserScrape — Obscura-backed JS render (6cbec23)
- **v0.15:** F5 telemetry + F6 hooks CLI + F7 process-group reaping + README (9096d7b)
- **v0.15:** F3 hooks subsystem + F4 clawtool onboard wizard (71334d8)
- **v0.15:** Per-instance rate limiter (F1) + clawtool upgrade subcommand (F2) (9b74041)
- **biam:** Ship ADR-015 Phase 1 (async dispatch + signed envelopes + SQLite store) + 3 polish fixes (42b4889)
- **v0.14:** T3 mem0 + T5 git-worktree isolation + T6 SemanticSearch (148f001)
- **v0.14:** T1 OTel + T2 auto-lint + T4 Verify MCP tool (22994f7)
- **serve:** POST /v1/recipe/apply + GET /v1/recipes + --mcp-http transport, plus claude/gemini transport fixes from live smoke (4b843ba)
- **supervisor:** Ship Phase 4 of ADR-014 — dispatch policies (round-robin, failover, tag-routed) (d806663)
- **relay:** Ship Phase 3 of ADR-014 — Docker image + clawtool-relay recipe (94130c2)
- **serve:** Ship Phase 2 of ADR-014 — clawtool serve --listen HTTP gateway (be91f9f)
- **agents:** Ship Phase 1 of ADR-014 — Transport, Supervisor, send/bridge CLI, MCP tools (c875a54)
### Fixes

- **test:** Allowlist clawtool-unattended.md as CLI-verb-only (e7c3c91)
- Fix(e2e) + feat(grep): repair CI + Grep context/multi-pattern/truncation

Two things in one commit because the e2e fix unblocks CI and the
Grep upgrades land cleanly together.

CI repair:
  test/e2e/run.sh asserted `Glob: engine == doublestar` literal,
  but the v0.18.6 .gitignore-aware path tags the engine as
  `doublestar+git-ls-files` when cwd is a Git worktree (which CI
  always is). Loosened the assertion to a regex that accepts
  either label. Local e2e + go test pass; CI should follow.

Grep upgrades (ADR-021 phase B continuation):

- context_before / context_after MCP args (default 0, hard cap 50)
  emit `rg -B` / `-A` and parse the resulting `context` events
  into per-match Before / After string slices. Codex called this
  "table stakes for code search".
- patterns MCP arg (newline-separated) OR's with the primary
  pattern via repeated `-e` flags so an agent can find both a
  function and its callers in one tool turn.
- Smart truncation footer now hints at the cap:
  "truncated at N (raise max_matches up to 10000 for more)"
  instead of just "truncated".
- Render gained context-aware output: lines before the match
  print as `path-N-: text`, the match keeps the conventional
  `path:line:col: text`, lines after also use the dash form,
  separator `--` between match groups (mirrors ripgrep CLI).

The rg-JSON parser had to be reworked because rg emits Before-
context events BEFORE the corresponding match, not after. New
loop buffers context events as they arrive, flushes them as
either Before of the next match (line < match.line) or After
of the previous match (line > match.line). Tail flush attaches
trailing context to the last match.

Tests:
- TestGrep_ContextLines drives a 5-line file through executeGrep
  with context_before=2, context_after=2, asserts both slices
  populate and contain the expected lines.
- TestGrep_MultiPattern asserts two patterns OR'd in one call
  return both matches.
- TestGrep_TruncationMessageMentionsHardCap pure-function check
  that the new render footer hints at the cap.
- All 8 Grep tests + 7 Glob tests + full suite race-clean. (c5f704f)
- **biam:** Surface NDJSON turn.failed/error events as TaskFailed (39a3b93)
- **v0.15:** MEDIUM polish — TaskGet/TaskWait surface MessagesFor errors; store decode failures stop silently dropping rows (758aea3)
- **v0.15:** Polish-worker HIGH+MEDIUM batch — limiter/round-robin singleton, BIAM Close errors, identity race, secret-aware index (deb19a1)
- **worktree:** EvalSymlinks comparison for macOS /var → /private/var (e0f2987)
- **agents:** Codex --skip-git-repo-check + transport closes stdin explicitly (aa52402)
- **ci:** Make e2e EXIT trap tolerate already-dead background process (4b4b269)
### Refactor

- **portal:** Swap hand-rolled CDP for chromedp (ADR-007) (e6af0f2)
### Style

- Gofmt -w . — fix drift in 7 files (c95a8f8)
### Tests

- **server:** Surface drift detection — three-plane contract enforced (f96de85)
- **portal:** Add Ask integration test (fake Browser + tagged real-Chrome) (5935e20)## [0.9.2] - 2026-04-26

### Chores

- **main:** Release 0.9.2 (60b1e58)
### Features

- **bridges:** Scaffold bridge install recipes for codex, opencode, gemini (9fa4481)
### Fixes

- **ci:** Install coreutils on macOS so gtimeout exists for e2e (f0fc3ca)
- **ci:** E2e script — detect timeout vs gtimeout for macOS runners (d92106f)
- **ci:** MacOS test failures + missing ripgrep on Ubuntu (1181728)
- **ci:** Correct gofmt invocation in lint step (53496ea)
### Other

- Merge pull request #8 from cogitave/release-please--branches--main--components--clawtool

chore(main): release 0.9.2 (644d29a)## [0.9.1] - 2026-04-26

### Chores

- **main:** Release 0.9.1 (9c09b6c)
- **main:** Release 0.9.1 (28ad4f6)
- Chore(ci)(deps): bump googleapis/release-please-action from 4 to 5

Dependabot PR. release-please-action@v5 picks up newer manifest
schema validation + faster Conventional Commits parsing. Our
existing config (release-please-config.json with bump-minor-pre-major
+ bump-patch-for-minor-pre-major) is forward-compatible. (5d3f774)
- Chore(ci)(deps): Bump googleapis/release-please-action from 4 to 5

Bumps [googleapis/release-please-action](https://github.com/googleapis/release-please-action) from 4 to 5.
- [Release notes](https://github.com/googleapis/release-please-action/releases)
- [Changelog](https://github.com/googleapis/release-please-action/blob/main/CHANGELOG.md)
- [Commits](https://github.com/googleapis/release-please-action/compare/v4...v5)

---
updated-dependencies:
- dependency-name: googleapis/release-please-action
  dependency-version: '5'
  dependency-type: direct:production
  update-type: version-update:semver-major
...

Signed-off-by: dependabot[bot] <support@github.com> (4db1ea8)
- Chore(ci)(deps): bump actions/setup-go from 5 to 6

Dependabot PR. setup-go@v6 brings Go 1.22+ defaults + fixes for
the v5 deprecated cache-key shape. No other behavioral change in
the workflows we ship; all matrix jobs continue to use 'go-version: stable'. (bacbac4)
- Chore(ci)(deps): Bump actions/setup-go from 5 to 6

Bumps [actions/setup-go](https://github.com/actions/setup-go) from 5 to 6.
- [Release notes](https://github.com/actions/setup-go/releases)
- [Commits](https://github.com/actions/setup-go/compare/v5...v6)

---
updated-dependencies:
- dependency-name: actions/setup-go
  dependency-version: '6'
  dependency-type: direct:production
  update-type: version-update:semver-major
...

Signed-off-by: dependabot[bot] <support@github.com> (81f7952)
### Fixes

- **ci:** Vet unreachable-code + gofmt across the tree (1830ee2)## [0.9.0] - 2026-04-26

### Build

- **install:** Post-install cleanup — drop duplicate manual MCP registration (bef3c3e)
- **integration:** Make integration target + nightly workflow (68f3ef9)
### Chores

- **main:** Release 0.9.0 (33b5790)
- **main:** Release 0.9.0 (746af63)
- **release:** Finish version sync to 0.8.6 (9f64b24)
- **release:** Sync version refs to 0.8.6 + tighten release-please policy (2283563)
- **repo:** Privatize wiki/.obsidian/_templates/.envrc/CLAUDE.md (4b3c1b6)
### Documentation

- **readme:** Pitch v0.9 — wizard + recipes lead the README (a1a7c69)
- **skill:** Onboarding mode — Claude can run init from a conversation (b449881)
- Strip internal ADR pointers from user-facing surfaces (a97ba57)
- **contributing:** Three-tier testing strategy (unit / e2e / integration) (daf90c6)
- **readme:** Reposition narrative around the toolset concept (a31ed68)
### Features

- **cli:** Clawtool source catalog (alias 'available') — browse before adding (e0d1cd9)
- **setup:** Lefthook + commitlint recipe — close release-please loop locally (f6bbb41)
- **agents:** Hermes-agent + openclaw adapters (b59b1d0)
- Claude-md + agents-md recipes + clawtool no-args TUI menu (4124290)
- **skill:** Clawtool skill new/list/path + SkillNew MCP tool (2cc78de)
- **setup:** Skill recipe pattern + Karpathy LLM Wiki (860166b)
- **setup:** Caveman + superclaude + claude-flow Claude-Code plugin recipes (115b7e6)
- **version:** Update-check + 6 new catalog entries (d08cb57)
- **cli:** Clawtool doctor — one-command diagnostic (4607fc4)
- **cli:** Wizard asks before overwriting unmanaged files (b6b7d0e)
- **setup:** --force flag for recipe apply (overwrite unmanaged) (0fe9e8d)
- **setup:** License — add AGPL-3.0 SPDX option (6e1b491)
- **cli:** Wizard install prompts + brain promoted to Stable (db88a7f)
- **setup:** Devcontainer — first runtime-category recipe (bfc14d3)
- **setup:** Prettier + golangci-lint — open the quality category (70701aa)
- **setup:** Gh-actions-test — first ci-category recipe (b283198)
- **setup:** Brain recipe — claude-obsidian wrapper (07863a6)
- Dual-scope init wizard + RecipeList/Status/Apply MCP tools (7da0632)
- **cli:** Clawtool init — interactive wizard via charmbracelet/huh (4cc54af)
- **setup:** Release-please + goreleaser recipes (04bb010)
- **setup:** Agent-claim recipe + fix marker reconciliation (86df90e)
- **cli:** Clawtool recipe list/status/apply (a6ec288)
- **setup:** Three more recipes — license, codeowners, dependabot (f3edfe7)
- **tools:** Split MCP output — pretty text + structuredContent (c45192d)
- **setup:** Foundation for clawtool init — recipes, runner, repo-config (1afde74)
- **install:** Add curl one-liner installer (aa20331)
### Fixes

- **doctor:** Quieter output + 5m update-cache (was 24h) (8107321)
- **agents:** Claim/release write to permissions.deny, not disabledTools (7eebd9f)
- **sources:** Expand ${VAR} in command argv, not just env (60c931b)
- **ci:** Bump orhun/git-cliff-action v3 to v4 (cf4daf8)
### Tests

- **e2e:** Assert all 12 v0.10 recipes + all 9 categories present (1b07c80)
- **e2e:** Cover the Recipe* MCP surface end-to-end (c5a296c)
- **cli:** Wizard helpers + dispatch + claim-diff coverage (dcf58c2)
- **integration:** Multi-instance soak against real upstream MCP servers (0cbb747)## [0.8.6] - 2026-04-26

### Features

- Initial public 0.8.6 release of clawtool (313a183)
