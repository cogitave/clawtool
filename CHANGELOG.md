# Changelog

All notable changes to clawtool are documented here. Format adheres to
[Conventional Commits](https://www.conventionalcommits.org/) and this
project follows [Semantic Versioning](https://semver.org/) — see
ADR-009 for the policy details.

## [0.9.3](https://github.com/cogitave/clawtool/compare/v0.9.2...v0.9.3) (2026-04-27)


### Features

* **agents:** ship Phase 1 of ADR-014 — Transport, Supervisor, send/bridge CLI, MCP tools ([c875a54](https://github.com/cogitave/clawtool/commit/c875a549fe48f4e96707b1d5f05c39c8721d7173))
* **agent:** user-defined personas — `clawtool agent new` + AgentNew tool ([12c701c](https://github.com/cogitave/clawtool/commit/12c701c865ba582a861040684d9f6b844009bb9d))
* **bash:** background mode + BashOutput / BashKill (ADR-021 phase B) ([3e9a055](https://github.com/cogitave/clawtool/commit/3e9a05552fe94f7fbac61593a50c1558d9459ecf))
* **biam:** ship ADR-015 Phase 1 (async dispatch + signed envelopes + SQLite store) + 3 polish fixes ([42b4889](https://github.com/cogitave/clawtool/commit/42b48892aa78d55a5593d9a78161bffd71d09d2d))
* **biam:** TaskNotify — edge-triggered fan-in completion push ([9152d3d](https://github.com/cogitave/clawtool/commit/9152d3d9bd9129b62030d5745c5c66e07a4092ce))
* **bridges:** hermes-agent — fifth supported family (NousResearch, MIT, 120K stars) ([16313bf](https://github.com/cogitave/clawtool/commit/16313bfe15080efb908674e6a7b16483b1ef1d53))
* clawtool uninstall — full footprint cleanup ([ce9bed7](https://github.com/cogitave/clawtool/commit/ce9bed7208ac465b6ab56ae4105eb06f4f8d6503))
* dockerize clawtool — 15MB distroless static image + Compose stack ([0713937](https://github.com/cogitave/clawtool/commit/07139377f38e2b06dc635b003fe4c908e0838e48))
* **relay:** ship Phase 3 of ADR-014 — Docker image + clawtool-relay recipe ([94130c2](https://github.com/cogitave/clawtool/commit/94130c279b945f58864181c75a09721088f9976a))
* **rules:** predicate-based invariant engine + RulesCheck tool ([9421e8c](https://github.com/cogitave/clawtool/commit/9421e8cb350e4af8ab80ce82a5d2301296e9c256))
* **serve:** POST /v1/recipe/apply + GET /v1/recipes + --mcp-http transport, plus claude/gemini transport fixes from live smoke ([4b843ba](https://github.com/cogitave/clawtool/commit/4b843bad5b60e11f464a327199cb2a8ba2582fb1))
* **serve:** ship Phase 2 of ADR-014 — clawtool serve --listen HTTP gateway ([be91f9f](https://github.com/cogitave/clawtool/commit/be91f9f3d9ab7cf5a0bf9f0e0b81477189949977))
* **supervisor:** ship Phase 4 of ADR-014 — dispatch policies (round-robin, failover, tag-routed) ([d806663](https://github.com/cogitave/clawtool/commit/d806663e3aa97b8ab17fda4ded29a8f5847606ff))
* **v0.14:** T1 OTel + T2 auto-lint + T4 Verify MCP tool ([22994f7](https://github.com/cogitave/clawtool/commit/22994f76931016d1093874500fc11ef460d9c506))
* **v0.14:** T3 mem0 + T5 git-worktree isolation + T6 SemanticSearch ([148f001](https://github.com/cogitave/clawtool/commit/148f0013913a4fd45aba5bcf7ecb00819f2a4518))
* **v0.15:** F3 hooks subsystem + F4 clawtool onboard wizard ([71334d8](https://github.com/cogitave/clawtool/commit/71334d81a005f7206c9e2eb05ead45fc5a666b68))
* **v0.15:** F5 telemetry + F6 hooks CLI + F7 process-group reaping + README ([9096d7b](https://github.com/cogitave/clawtool/commit/9096d7bf2d5dc37384d228827603d6f57bdab1b5))
* **v0.15:** per-instance rate limiter (F1) + clawtool upgrade subcommand (F2) ([9b74041](https://github.com/cogitave/clawtool/commit/9b7404190fe57070c55311fb9043102f0f16a90b))
* **v0.16.1:** Portal feature — saved web-UI targets (ADR-018) ([0171284](https://github.com/cogitave/clawtool/commit/01712846edd7341e1c0b74cda9fe41a7e6ff9561))
* **v0.16.2:** Portal CDP driver — Ask flow + per-portal MCP aliases ([8067955](https://github.com/cogitave/clawtool/commit/8067955424ec52a8a5b67a013dd94180fa1878f3))
* **v0.16.3:** portal add interactive wizard (chromedp + Chrome) ([3532ffa](https://github.com/cogitave/clawtool/commit/3532ffaaa8014c0706caa950b12a9d677c163fb7))
* **v0.16.4:** clawtool mcp authoring noun + surface (ADR-019) ([8301353](https://github.com/cogitave/clawtool/commit/8301353ebd5bcfea344d9dc93020cba0604ff36e))
* **v0.16:** BrowserFetch + BrowserScrape — Obscura-backed JS render ([6cbec23](https://github.com/cogitave/clawtool/commit/6cbec23e895f201bf3c72d8837b28f0af60d8342))
* **v0.17:** clawtool mcp generator — Go / Python / TypeScript scaffolds ([b6a3359](https://github.com/cogitave/clawtool/commit/b6a33593eabff109ddf6ebc3ef842d074c7cc67f))
* **v0.18.1:** bwrap engine real Wrap — Profile→argv compiler + live sandbox enforcement ([01cd88e](https://github.com/cogitave/clawtool/commit/01cd88e6b5c5b83d9b0f55a032b192f687d7e924))
* **v0.18.4:** core tools polish phase A — Read hashes, Write Read-before-Write, Edit diff (ADR-021) ([ec2dd44](https://github.com/cogitave/clawtool/commit/ec2dd44d9da2a5f1b16c456615fa75a463e4ad27))
* **v0.18.6:** core tools polish phase B — Glob .gitignore + WebFetch SSRF guard ([ab1647c](https://github.com/cogitave/clawtool/commit/ab1647c68634478e7dc242f33762e219b4a6e559))
* **v0.18:** clawtool sandbox surface + ADR-020 (bwrap/sandbox-exec/docker) ([8c81e37](https://github.com/cogitave/clawtool/commit/8c81e37f488e9d188ddd5d18804b4c6c7f0d1702))
* **websearch:** provider-neutral filter shape — domains / recency / country / topic ([1ea710d](https://github.com/cogitave/clawtool/commit/1ea710d42434bd04a64b48604d224f3882b502a3))


### Fixes

* **agents:** codex --skip-git-repo-check + transport closes stdin explicitly ([aa52402](https://github.com/cogitave/clawtool/commit/aa52402749b33c6bb57ece7ddaac1835bbaeac12))
* **biam:** surface NDJSON turn.failed/error events as TaskFailed ([39a3b93](https://github.com/cogitave/clawtool/commit/39a3b93c704160e55f497c54483d78b22deb7b7d))
* **ci:** make e2e EXIT trap tolerate already-dead background process ([4b4b269](https://github.com/cogitave/clawtool/commit/4b4b269945daf81524625c527b4bb1aa341a8ec6))
* **v0.15:** MEDIUM polish — TaskGet/TaskWait surface MessagesFor errors; store decode failures stop silently dropping rows ([758aea3](https://github.com/cogitave/clawtool/commit/758aea346a85882abb8abb660315b5ff921b1bbe))
* **v0.15:** polish-worker HIGH+MEDIUM batch — limiter/round-robin singleton, BIAM Close errors, identity race, secret-aware index ([deb19a1](https://github.com/cogitave/clawtool/commit/deb19a1e08a9b5f2e935e3be542e5801b3769db6))
* **worktree:** EvalSymlinks comparison for macOS /var → /private/var ([e0f2987](https://github.com/cogitave/clawtool/commit/e0f2987c7ae36db7287bf548649acf20f3d55d9b))


### Refactor

* **portal:** swap hand-rolled CDP for chromedp (ADR-007) ([e6af0f2](https://github.com/cogitave/clawtool/commit/e6af0f2641f9140e008567bf13a88c77e2cc3940))


### Documentation

* **http:** add docs/http-api.md + README link — Postman & cURL recipes ([c45132c](https://github.com/cogitave/clawtool/commit/c45132c22c6ca7570c91b82f68fa122d986396d4))
* **plugin:** adopt 'Tools. Agents. Wired.' tagline ([1099ae5](https://github.com/cogitave/clawtool/commit/1099ae5b44d1894b9f298334361f9d67b49b13ec))
* **plugin:** refresh About — canonical tool layer + multi-agent supervisor ([ee17735](https://github.com/cogitave/clawtool/commit/ee17735c23e49ff347acf72abbdd6aab2eeabb40))
* **readme:** full rewrite — "Tools. Agents. Wired." tagline + complete tool table ([bb3811f](https://github.com/cogitave/clawtool/commit/bb3811f0a0f60c3ac775d3a9cc172c56b65ff737))
* **readme:** v0.14 / v0.15 surface — BIAM, bridges, send --async, worktree, upgrade ([498a241](https://github.com/cogitave/clawtool/commit/498a24174655abe282755ecaec6bf90eb3b3e20b))
* three-plane feature shipping contract + SKILL.md routing map ([cf43c92](https://github.com/cogitave/clawtool/commit/cf43c92858e77a22083864ad13bcd5a2d8047638))


### Tests

* **portal:** add Ask integration test (fake Browser + tagged real-Chrome) ([5935e20](https://github.com/cogitave/clawtool/commit/5935e2072776fb2bb78ad7649b961a99f756e6a0))
* **server:** surface drift detection — three-plane contract enforced ([f96de85](https://github.com/cogitave/clawtool/commit/f96de8523e71af8e407f1ca05fd5c3c515f3cc9e))


### CI

* bump Go to 1.26.0 (chromedp dep requires it) ([4ab2eaf](https://github.com/cogitave/clawtool/commit/4ab2eafad8cff1b6e43bb9f915bc23183828e34c))

## [0.9.2](https://github.com/cogitave/clawtool/compare/v0.9.1...v0.9.2) (2026-04-26)


### Features

* **bridges:** scaffold bridge install recipes for codex, opencode, gemini ([9fa4481](https://github.com/cogitave/clawtool/commit/9fa448189dfd757ff6ec89f2017bf81386113337))


### Fixes

* **ci:** correct gofmt invocation in lint step ([53496ea](https://github.com/cogitave/clawtool/commit/53496ea450d8202e8542164dd0980e60ac860db4))
* **ci:** e2e script — detect timeout vs gtimeout for macOS runners ([d92106f](https://github.com/cogitave/clawtool/commit/d92106f7f3d2da2dad839eef064ea46ec7042912))
* **ci:** install coreutils on macOS so gtimeout exists for e2e ([f0fc3ca](https://github.com/cogitave/clawtool/commit/f0fc3cae462815f5937e204647c45f907a66844a))
* **ci:** macOS test failures + missing ripgrep on Ubuntu ([1181728](https://github.com/cogitave/clawtool/commit/1181728fde7bd3c922b48b7e79f4ae97f8bfe1e1))

## [0.9.1](https://github.com/cogitave/clawtool/compare/v0.9.0...v0.9.1) (2026-04-26)


### Fixes

* **ci:** vet unreachable-code + gofmt across the tree ([1830ee2](https://github.com/cogitave/clawtool/commit/1830ee21d151f6d839da96cd6c92b0553b6da3be))

## [0.9.0](https://github.com/cogitave/clawtool/compare/v0.8.6...v0.9.0) (2026-04-26)


### ⚠ BREAKING CHANGES

* **tools:** split MCP output — pretty text + structuredContent

### Features

* **agents:** hermes-agent + openclaw adapters ([b59b1d0](https://github.com/cogitave/clawtool/commit/b59b1d000b6611562e4c397ea55e2199e4ff3ac5))
* claude-md + agents-md recipes + clawtool no-args TUI menu ([4124290](https://github.com/cogitave/clawtool/commit/41242904d09f08329fd918dc6968aa5ed127915c))
* **cli:** clawtool doctor — one-command diagnostic ([4607fc4](https://github.com/cogitave/clawtool/commit/4607fc4642a20b4713e4a14f15c3ba4afbcf4527))
* **cli:** clawtool init — interactive wizard via charmbracelet/huh ([4cc54af](https://github.com/cogitave/clawtool/commit/4cc54af77b7f2c8f1dd47ec91584991e865cd311))
* **cli:** clawtool recipe list/status/apply ([a6ec288](https://github.com/cogitave/clawtool/commit/a6ec2885f491cd040572f091584e675d9cd22a93))
* **cli:** clawtool source catalog (alias 'available') — browse before adding ([e0d1cd9](https://github.com/cogitave/clawtool/commit/e0d1cd981c63cb27781a2460f27d87ece659cfae))
* **cli:** wizard asks before overwriting unmanaged files ([b6b7d0e](https://github.com/cogitave/clawtool/commit/b6b7d0ee1dac80b8d45e5ba790a5688252eb4dff))
* **cli:** wizard install prompts + brain promoted to Stable ([db88a7f](https://github.com/cogitave/clawtool/commit/db88a7f4db317797dd52bf2deb9d279b5934c511))
* dual-scope init wizard + RecipeList/Status/Apply MCP tools ([7da0632](https://github.com/cogitave/clawtool/commit/7da06329861039449f69cbe970b24a39bfc66791))
* **install:** add curl one-liner installer ([aa20331](https://github.com/cogitave/clawtool/commit/aa203315fd98423b0d9cfd4dc1767b79d1cbcdd1))
* **setup:** --force flag for recipe apply (overwrite unmanaged) ([0fe9e8d](https://github.com/cogitave/clawtool/commit/0fe9e8d0bb83c61c86e50bcd916f228f69d29d4b))
* **setup:** agent-claim recipe + fix marker reconciliation ([86df90e](https://github.com/cogitave/clawtool/commit/86df90e5074106b61d66023f927154a45cf0d327))
* **setup:** brain recipe — claude-obsidian wrapper ([07863a6](https://github.com/cogitave/clawtool/commit/07863a6e1197b799f8a82952185851343722acbe))
* **setup:** caveman + superclaude + claude-flow Claude-Code plugin recipes ([115b7e6](https://github.com/cogitave/clawtool/commit/115b7e69e877a91402971e6f4ebac3044040c30b))
* **setup:** devcontainer — first runtime-category recipe ([bfc14d3](https://github.com/cogitave/clawtool/commit/bfc14d366a35a9f0700e66807818d6bf1eb2caa5))
* **setup:** foundation for clawtool init — recipes, runner, repo-config ([1afde74](https://github.com/cogitave/clawtool/commit/1afde74dcfbe2ad08d681da2ab224d96416817b3))
* **setup:** gh-actions-test — first ci-category recipe ([b283198](https://github.com/cogitave/clawtool/commit/b283198a3c9378ada97c209f42270429c2fbc42d))
* **setup:** lefthook + commitlint recipe — close release-please loop locally ([f6bbb41](https://github.com/cogitave/clawtool/commit/f6bbb41ace0b1c60b4e357cc416affcc4f585dab))
* **setup:** license — add AGPL-3.0 SPDX option ([6e1b491](https://github.com/cogitave/clawtool/commit/6e1b4915543b7c558b99d57c38f0d2fce1d9085c))
* **setup:** prettier + golangci-lint — open the quality category ([70701aa](https://github.com/cogitave/clawtool/commit/70701aad9a48a9be258ded76dad13fa6c944fdc3))
* **setup:** release-please + goreleaser recipes ([04bb010](https://github.com/cogitave/clawtool/commit/04bb010533c972b7ed4d1d02e1feaf964edaaed9))
* **setup:** skill recipe pattern + Karpathy LLM Wiki ([860166b](https://github.com/cogitave/clawtool/commit/860166bf63879a3b85df9e8e1c12276629a88f28))
* **setup:** three more recipes — license, codeowners, dependabot ([f3edfe7](https://github.com/cogitave/clawtool/commit/f3edfe78c73d27889c28065090641f8ede1630ea))
* **skill:** clawtool skill new/list/path + SkillNew MCP tool ([2cc78de](https://github.com/cogitave/clawtool/commit/2cc78de5dc71399c9fb37e83ea39035629ff802d))
* **tools:** split MCP output — pretty text + structuredContent ([c45192d](https://github.com/cogitave/clawtool/commit/c45192d487c065a7feb6bbd487d068d8227c9f5c))
* **version:** update-check + 6 new catalog entries ([d08cb57](https://github.com/cogitave/clawtool/commit/d08cb57a70d1e8fbf98cee5e42ab9dd02fcfaa0d))


### Fixes

* **agents:** claim/release write to permissions.deny, not disabledTools ([7eebd9f](https://github.com/cogitave/clawtool/commit/7eebd9f0cfb5d532dda1bd7adad36a66da14942b))
* **ci:** bump orhun/git-cliff-action v3 to v4 ([cf4daf8](https://github.com/cogitave/clawtool/commit/cf4daf8768093c117d1822301e1d53ef1ddf6cbc))
* **doctor:** quieter output + 5m update-cache (was 24h) ([8107321](https://github.com/cogitave/clawtool/commit/8107321324f6861695dfc5e9065bea082a0f6483))
* **sources:** expand ${VAR} in command argv, not just env ([60c931b](https://github.com/cogitave/clawtool/commit/60c931bf9b6cbf7b69f82d618222ec45c311688f))


### Documentation

* **contributing:** three-tier testing strategy (unit / e2e / integration) ([daf90c6](https://github.com/cogitave/clawtool/commit/daf90c6760d8ab6b299e864186d33b3014b22885))
* **readme:** pitch v0.9 — wizard + recipes lead the README ([a1a7c69](https://github.com/cogitave/clawtool/commit/a1a7c6997f07a97692fae02a2f4f6fe7f70c5d68))
* **readme:** reposition narrative around the toolset concept ([a31ed68](https://github.com/cogitave/clawtool/commit/a31ed687bc1fb879e13cd41214d16593bc39635f))
* **skill:** onboarding mode — Claude can run init from a conversation ([b449881](https://github.com/cogitave/clawtool/commit/b44988199140f841002ef59993d66d9ecc95fc11))
* strip internal ADR pointers from user-facing surfaces ([a97ba57](https://github.com/cogitave/clawtool/commit/a97ba57a9f405115ec4bd04818aeddc2c85e27c0))


### Tests

* **cli:** wizard helpers + dispatch + claim-diff coverage ([dcf58c2](https://github.com/cogitave/clawtool/commit/dcf58c2352d9962413204579e3aa814a85895906))
* **e2e:** assert all 12 v0.10 recipes + all 9 categories present ([1b07c80](https://github.com/cogitave/clawtool/commit/1b07c80444836d1371c0156dc11a6c5440176f74))
* **e2e:** cover the Recipe* MCP surface end-to-end ([c5a296c](https://github.com/cogitave/clawtool/commit/c5a296cbf3b4255414faaf08035ebe6942032199))
* **integration:** multi-instance soak against real upstream MCP servers ([0cbb747](https://github.com/cogitave/clawtool/commit/0cbb747b09a42a52e37ed862f9dfc75c9fdf0b61))


### Build

* **install:** post-install cleanup — drop duplicate manual MCP registration ([bef3c3e](https://github.com/cogitave/clawtool/commit/bef3c3ed39d0946407e1e43dc15b23b14d6a8585))
* **integration:** make integration target + nightly workflow ([68f3ef9](https://github.com/cogitave/clawtool/commit/68f3ef94db99e0a5f3c4f2d6a72493358463137f))


### Chores

* **release:** finish version sync to 0.8.6 ([9f64b24](https://github.com/cogitave/clawtool/commit/9f64b24d8c4d1fe95844e4dc180e30df35b942f9))
* **release:** sync version refs to 0.8.6 + tighten release-please policy ([2283563](https://github.com/cogitave/clawtool/commit/228356393aa61c93e596cb863cbde17ab43ab735))
* **repo:** privatize wiki/.obsidian/_templates/.envrc/CLAUDE.md ([4b3c1b6](https://github.com/cogitave/clawtool/commit/4b3c1b65d26c39f71f40bd5f746a90ed2a3a424d))

## [0.8.4] - 2026-04-26

### Features

- **agents:** Add 'clawtool agents claim/release/status' for hard native-tool replacement (ADR-011) (468a082)## [0.8.3] - 2026-04-26

### Features

- **plugin:** Add Claude Code plugin packaging (ADR-010) (86dd403)
### Other

- Auto backup 2026-04-26 18:18:52 (d01990a)## [0.8.2] - 2026-04-26

### Build

- **ci:** Add GitHub Actions matrix + GoReleaser pipeline (d4f04c8)
### Chores

- **github:** Add CODEOWNERS + Dependabot config (615ac42)
### Documentation

- Add CONTRIBUTING + SECURITY + issue/PR templates (7770140)
### Fixes

- **changelog:** Guard cliff.toml template against unreleased-commit null version (e3df3cd)## [0.8.1] - 2026-04-26

### Documentation

- **adr-009:** Adopt versioning policy + git-cliff for changelog (1ad7798)## [0.8.0] - 2026-04-26

### Decisions

- Instance scoping and tool naming convention (75479bd)
- Positioning — replace native agent tools (98b7101)
- ADR-004 add Distribution & Usage Scenarios

Define the two-layer model:
- Layer 1: standalone binary (~/.local/bin/clawtool) via npm/brew/curl,
  generic MCP server, the actual product
- Layer 2: per-agent plugins (Claude Code, Codex, ...) as thin
  install+registration wrappers; no state fork

Three usage scenarios:
A) power-user manual mcp add
B) CC-only plugin (zero friction)
C) multi-agent shared state via single ~/.config/clawtool/

Key invariant: 'install once, use everywhere' means *shared config*,
not just a portable binary. State lives in one place per device;
agents are thin readers; hot-reload propagates to all clients. (961aa43)
- ADR-004 refine: multi-level tool selectors

Add server, tag, and group selectors alongside per-tool dot-notation.
Define precedence (tool > group > tag > server) with deny-wins
at same level. New CLI commands: clawtool group create,
clawtool tools status <selector> for resolution debugging.

Addresses real-workflow gap: docker-mcp-gateway forces one-tool-at-a-time
and 1mcp-agent only does server-level. Tags exploit the
annotations.clawtool.tags field already spec'd in decision 3.

Updated hot.md and log.md to reflect the change. (a8b3a7b)
### Features

- **tools:** Add Edit and Write core tools (canonical core complete) (8ab46fd)
### Genesis

- Initial vault scaffold — clawtool brain layer

- Standard wiki structure (sources, entities, concepts, decisions, comparisons, questions, meta)
- Pre-seeded ADRs (001-003) for choices made today
- Memory tools comparison + key entities and concepts
- _templates/ for each note type
- vault-colors.css for Obsidian
- CLAUDE.md with project context

Built on AgriciDaniel/claude-obsidian Karpathy LLM Wiki pattern. (22b7910)
### Other

- Auto backup 2026-04-26 18:03:51 (4c6c977)
- Auto backup 2026-04-26 17:48:50 (b7f68f1)
- Auto backup 2026-04-26 17:33:49 (5f387cf)
- Auto backup 2026-04-26 17:18:49 (511a37a)
- Remove accidentally-committed stub-server binary

The test fixture binary was committed in the v0.4 turn 2 commit. It's
build output, not source. Add to .gitignore (rebuild via 'make
stub-server'). The source at test/e2e/stub-server/main.go remains
tracked. (48b472d)
- Auto backup 2026-04-26 17:03:47 (35d3b21)
- Auto backup 2026-04-26 16:48:46 (1ac4968)
- Auto backup 2026-04-26 16:33:45 (4a9b619)
- Auto backup 2026-04-26 16:18:44 (ba50dd4)
- Fix Obsidian wikilink resolution

Add aliases frontmatter to all ADRs and key comparisons so
title-form wikilinks (e.g. [[004 clawtool initial architecture
direction]]) resolve to kebab-case filenames. Without aliases,
Obsidian creates empty stub files at vault root.

Removed one such stub created earlier.

Pattern: each file gets aliases for its full title and a short
ADR-NNN form for quick references. (0b8d52c)
- Auto backup 2026-04-26 16:03:43 (9f24ce5)
- Research phase round 1 — universal-toolset survey + ADR-004

Surveyed 4 candidate projects (mcp-router, 1mcp-agent, metamcp,
docker-mcp-gateway) and filed each as a wiki entity. Synthesis in
Universal Toolset Projects Comparison identifies search-first /
deferred tool loading as the universally-uncovered gap.

ADR-004 locks initial architecture direction:
- MCP-native single user-local binary, no Docker requirement
- Search-first = deferred loading + semantic discovery
- Tool manifest extends MCP schema via annotations.clawtool namespace
- CLI dot-notation config + declarative file + hot-reload
- Build new (not fork 1mcp-agent), borrow shamelessly

Open: language, license, ranking model, catalog source — deferred
to prototype phase.

Index, log, hot cache, and per-folder _index files updated to reflect
the new pages. (222cd03)
### Releases

- WebFetch + WebSearch (web tier) (d9afc35)
- Read expanded to 9 formats (docx, xlsx, csv/tsv, html, +structured) (71891c9)
- ToolSearch (bleve BM25) + Glob (doublestar) (92fe210)
- V0.4 turn 2: MCP client/server proxy

ADR-008's runtime substance: clawtool now spawns each configured source
as a child MCP server, aggregates its tools under wire-form
<instance>__<tool> names per ADR-006, and routes tools/call.

- internal/sources/{instance,manager}.go: lifecycle manager built on
  mark3labs/mcp-go/client.NewStdioMCPClient. Per-instance Status
  (Starting/Running/Down/Unauthenticated) with reason strings.
  Non-fatal start: one source failing does not block others.
- internal/server/server.go: ServeStdio loads config + secrets, builds
  Manager, starts sources, registers core tools (filtered by
  config.IsEnabled), then registers aggregated source tools. Stop on
  shutdown.
- test/e2e/stub-server/main.go: tiny Go MCP server (echo tool) used
  as a deterministic test fixture for both unit and e2e suites — no
  external npm/pip dependencies needed.
- Makefile: e2e now depends on stub-server; new 'make stub-server'
  target.
- internal/sources/manager_test.go: 7 unit tests + 6 SplitWireName
  subtests. Spawns the real stub-server subprocess to exercise the
  full stdio + protocol + lifecycle path.
- test/e2e/run.sh: 6 new proxy assertions. Verifies stub__echo gets
  aggregated alongside core tools, wire form uses double underscore,
  tools/call routes correctly, and config core_tools disable still
  works alongside source tools.
- Smoke: clawtool serve with [sources.stub] exposes Bash/Grep/Read +
  stub__echo; tools/call stub__echo {text: hello-routing} returns
  echo:hello-routing routed through the proxy end-to-end.

Tests: 65 Go unit + 29 e2e = 94 green. New: sources 7, e2e proxy 6. (5cc6ba0)
- V0.4 turn 1: source catalog + secrets store + source CLI

Implements ADR-008's user-facing UX. Sources are config-only this
turn — actual MCP client/server proxy spawn lands in turn 2.

Built-in catalog (internal/catalog/builtin.toml, embedded via go:embed):
12 entries — github, slack, postgres, sqlite, filesystem, fetch,
brave-search, google-maps, memory, sequentialthinking, time, git.
Per-runtime command synthesis (npx/uvx/docker/binary), env templates,
bidirectional fuzzy SuggestSimilar.

Secrets store (internal/secrets) at ~/.config/clawtool/secrets.toml
mode 0600, separate from config.toml so config can be committed.
Scope-based (instance | global), atomic save, ${VAR} interpolation
against secrets-first then process env.

CLI subcommands (internal/cli/source.go):
- source add <name> [--as <instance>]: catalog lookup, write config,
  print copy-paste set-secret command for missing env
- source list: auth status per instance
- source remove <instance>
- source set-secret <instance> <KEY> [--value V]: stdin fallback
- source check: verify required env per source

Fixed stdlib-flag-doesn't-intersperse via reorderFlagsFirst helper
so 'source add github --as github-work' parses correctly.

Tests: 58 Go unit + 23 e2e = 81 green. New: catalog 11, secrets 7,
cli source 13.

Naming + invariants from ADR-006 enforced: instance kebab-case,
multi-instance forces --as, secrets scoped per instance with
global fallback. Long-form 'source add custom -- <command>' and
proxy spawning are turn 2. (813773c)
- Grep (ripgrep) + Read (stdlib/pdftotext/ipynb) + ADR-008 (f9eb60e)
- Tests + config + CLI + ADR-007 leverage-best-in-class (fee08d0)
- V0.1 prototype: working clawtool MCP server with Bash tool

End-to-end loop proven: build → install → register with Claude Code →
tools/list shows Bash → tools/call returns structured JSON.

Stack:
- Go 1.25.5, github.com/mark3labs/mcp-go v0.49.0
- module github.com/cogitave/clawtool
- cmd/clawtool/main.go entrypoint with serve/version/help
- internal/server, internal/version, internal/tools/core

Bash tool quality bar (ADR-005):
- timeout-safe via process-group SIGKILL (Setpgid + Kill -PGID)
- stdout preserved on timeout
- structured result JSON: stdout/stderr/exit_code/duration_ms/timed_out/cwd
- 500ms timeout test with 'sleep 3' returns at 501ms

Naming (ADR-006):
- PascalCase 'Bash' for core tool
- Wire form mcp__clawtool__Bash

Installed at ~/.local/bin/clawtool; registered with claude mcp
add-json at user scope; claude mcp list reports Connected.

Documented in wiki/sources/prototype-bringup-2026-04-26.md.
Deferred to v0.2: other core tools, ToolSearch, config.toml,
CLI subcommands, source instances, secret redaction. (f9c3b03)
