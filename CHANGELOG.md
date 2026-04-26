# Changelog

All notable changes to clawtool are documented here. Format adheres to
[Conventional Commits](https://www.conventionalcommits.org/) and this
project follows [Semantic Versioning](https://semver.org/) — see
ADR-009 for the policy details.

## [0.8.2] - 2026-04-26

### Build

- **ci:** Add GitHub Actions matrix + GoReleaser pipeline (d4f04c8)
### Chores

- **github:** Add CODEOWNERS + Dependabot config (615ac42)
### Documentation

- Add CONTRIBUTING + SECURITY + issue/PR templates (7770140)
### Fixes

- **changelog:** Guard cliff.toml template against unreleased-commit null version (306eac8)## [0.8.1] - 2026-04-26

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
