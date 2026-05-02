# Changelog

All notable changes to clawtool are documented here. Format adheres to
[Conventional Commits](https://www.conventionalcommits.org/) and this
project follows [Semantic Versioning](https://semver.org/).

## [0.22.116] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.115 [skip ci] (130fea2)
### Features

- **mcp:** Mcp-Method and Mcp-Name headers on Streamable HTTP per SEP-2243 (c1ef522)## [0.22.115] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.113 [skip ci] (e5000df)
### Features

- **mcp:** Name-sorted tools/list + alwaysLoad hot tools + ToolSearch detail_level (faf2703)## [0.22.114] - 2026-05-01

### Features

- **send:** --no-auto-close CLI flag for per-task pane preservation (c16bac3)## [0.22.113] - 2026-05-01

### Fixes

- **upgrade-test:** Drop bearer-token read in --no-auth health probe (e3c084d)## [0.22.112] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.111 [skip ci] (d24d40a)
### Tests

- **e2e:** Exercise v0.22.109 lifecycle features (window cleanup + grace + per-task override) (a18ebeb)## [0.22.111] - 2026-05-01

### Fixes

- **e2e:** Inject real version into Dockerfile go build via -ldflags (dc5a71b)## [0.22.110] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.109 [skip ci] (7a37658)
### Fixes

- **toolsearch:** Mirror tool descriptions into bleve index manifest (dd7fc80)## [0.22.109] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.108 [skip ci] (5247c1c)
### Features

- **agents:** Resolve follow-up questions (window cleanup, grace period, per-task override) (b7651c2)## [0.22.108] - 2026-05-01

### Documentation

- **mcp:** Rewrite tool descriptions for proactive ToolSearch ranking (e195b30)
- **changelog:** Regenerate for v0.22.107 [skip ci] (f675b39)## [0.22.107] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.106 [skip ci] (04b470c)
### Features

- **peer:** Auto-close auto-spawned tmux panes on terminal task status (6e4b78e)## [0.22.106] - 2026-05-01

### Features

- **install:** Install.sh handles tmux + claude-code + node deps; add RuntimeInstall MCP tool for chat-driven backend install (9eecc8e)
### Tests

- **e2e:** Add fullstack Docker harness exercising install→daemon→tmux→peer-register→peer-send (3f7fc4d)## [0.22.105] - 2026-05-01

### Features

- **agents:** SendMessage auto-spawns tmux pane when no live peer (zero-touch peer creation) (02b8150)## [0.22.104] - 2026-05-01

### Features

- **bootstrap:** First-run onboarding prompt on UserPromptSubmit (idempotent via marker) (691cba0)## [0.22.103] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.102 [skip ci] (5deec46)
### Fixes

- **peer:** Make tmux send-keys stub path-portable (macos-latest CI red) (7ed27d7)## [0.22.102] - 2026-05-01

### Features

- **peer:** Add tmux send-keys push for real-time agent-to-agent delivery (repowire-style) (c579a52)## [0.22.101] - 2026-05-01

### Features

- **install:** --auto-spawn flag opens detected agents in tmux panes + auto-registers pane_ids (c7e3983)## [0.22.100] - 2026-05-01

### Fixes

- **hooks:** Auto-deliver peer inbox on UserPromptSubmit (Stop drain never reached agent context) (99118a5)## [0.22.99] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.98 [skip ci] (67f0aec)
### Fixes

- **hooks:** Move SessionStart context-load to UserPromptSubmit (Claude Code v2.1.126 ToolUseContext regression workaround) (639fb87)## [0.22.98] - 2026-05-01

### Features

- **spawn:** Add 'spawn' verb + Spawn MCP tool to open new terminals running agents (auto peer-registered) (2438c74)## [0.22.97] - 2026-05-01

### Features

- **agents:** SendMessage prefers live BIAM peer over spawning fresh subprocess (peer-prefer mode default) (ced7440)## [0.22.96] - 2026-05-01

### Features

- **peer:** Add 'peer drain' verb + bundled session-tick inbox hook for live message delivery (128f140)## [0.22.95] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.94 [skip ci] (0fcb170)
### Features

- **cli:** Add 'install' verb for zero-touch first-run setup (daemon + hosts + hooks + peer + init) (5485725)## [0.22.94] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.93 [skip ci] (b0ed860)
### Features

- **peer:** Add peer list verb + PeerList MCP tool for BIAM peer discovery (13c352c)## [0.22.93] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.92 [skip ci] (8354924)
### Fixes

- **mcp:** Accept-header-aware /mcp content negotiation for rmcp Streamable-HTTP clients (9c4f96c)## [0.22.92] - 2026-05-01

### Fixes

- **commands:** Repair YAML frontmatter that broke ubuntu-latest CI (e7914c4)## [0.22.91] - 2026-05-01

### Tests

- **server:** Add commands/ slash-command lint covering frontmatter + verb-existence + allowed-tools (f70494d)## [0.22.90] - 2026-05-01

### Build

- **docker:** Finalize unified Dockerfile by removing legacy ones + swapping all consumers (f0bf60f)
### Documentation

- **changelog:** Regenerate for v0.22.88 [skip ci] (366a4e5)## [0.22.89] - 2026-05-01

### Fixes

- **mcp:** Make CLAWTOOL_TOKEN optional in default install (codex was refusing to start without it) (a89946e)## [0.22.88] - 2026-05-01

### Documentation

- **playbooks:** Add aider/semble/mcp-toolbox/shell-mcp/promptfoo/rtk/archon setup playbooks (3d2cfeb)## [0.22.87] - 2026-05-01

### Documentation

- Add autonomous/bootstrap/fanout/release-notes/telemetry pages + refresh rules/portals/mcp-authoring for v0.22.50-.82 surface (440665f)## [0.22.86] - 2026-05-01

### Documentation

- **commands:** Add slash-commands for new verbs (init/onboard/bootstrap/autonomous/fanout/apm/source-inspect/source-registry/playbook-list-archon) (857581e)## [0.22.85] - 2026-05-01

### Features

- **fanout:** Add 'fanout' verb + Fanout MCP tool for parallel-subgoal orchestration (026dca4)## [0.22.84] - 2026-05-01

### Documentation

- **readme:** Audit + update for v0.22.50-.74 surface (BIAM peers, MCP tools, catalog, autonomous mode) (c68be5f)## [0.22.83] - 2026-05-01

### Build

- **docker:** Consolidate Dockerfiles into unified multi-stage Dockerfile.unified (6892a09)
### Documentation

- **changelog:** Regenerate for v0.22.82 [skip ci] (28c9d89)## [0.22.82] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.81 [skip ci] (0f9c7d5)
### Tests

- **e2e:** Add bootstrap container test verifying zero-click install flow (cc3d9ce)## [0.22.81] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.80 [skip ci] (e4b8bab)
### Fixes

- **ci:** Force release body via gh CLI post-step (goreleaser silently drops --release-notes) (ba1da75)
- **telemetry:** No-op in CI by default + filter Go pseudo-versions from version reporting (853f511)## [0.22.80] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.79 [skip ci] (49e67df)
### Fixes

- **release:** Drop static header/footer templates that suppressed BODY.md + leaked ADR reference (a5ac217)## [0.22.79] - 2026-05-01

### Documentation

- **changelog:** Regenerate for v0.22.78 [skip ci] (3c2caac)
### Fixes

- **release:** Drop release.mode=append so --release-notes=BODY.md actually populates body (8dbfdf8)## [0.22.78] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.76 [skip ci] (eeae5a5)
### Fixes

- **ci:** Drop pre-goreleaser stash that swept BODY.md, breaking rich release notes (9721118)## [0.22.77] - 2026-04-30

### Features

- **ci:** Replace git-cliff release body with rich self-hosted release-notes script (fcf687c)## [0.22.76] - 2026-04-30

### Features

- **cli:** Add 'bootstrap' verb spawning chosen agent + auto-running init from chat (db8ebd1)## [0.22.75] - 2026-04-30

### Features

- **rules:** Add guardians taint+Z3 pre_send predicate (phase 1 stub) (ff76b0e)## [0.22.74] - 2026-04-30

### Features

- **cli:** Add 'autonomous --resume' + '--watch' for chat-driven loop continuity (4518cac)## [0.22.73] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.72 [skip ci] (da7a6b3)
### Features

- **recipes:** Add clawtool-autonomous-loop SKILL.md teaching tick.json contract (2df7512)## [0.22.72] - 2026-04-30

### Features

- **tools:** Expose AutonomousRun MCP tool for chat-driven self-paced dev loops (a266e5e)## [0.22.71] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.70 [skip ci] (e562396)
### Features

- **cli:** Add 'autonomous' verb for self-paced single-message dev loop (8fc73f8)## [0.22.70] - 2026-04-30

### Features

- **catalog:** Add shell-mcp sandbox-aware shell MCP source entry + recipe (0c755ed)## [0.22.69] - 2026-04-30

### Features

- **rules:** Add interceptor:pre_tool_use alias mirroring MCP upstream RFC (f52477c)## [0.22.68] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.67 [skip ci] (4429703)
### Features

- **catalog:** Add MinishLab/semble code-search MCP source entry + recipe (c078414)## [0.22.67] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.66 [skip ci] (38db2ff)
### Features

- **playbooks:** Add Archon YAML workflow loader + recipe (phase 1, read-only) (1294ad8)## [0.22.66] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.65 [skip ci] (9444c87)
### Fixes

- **ci:** Stop CHANGELOG.md drift breaking goreleaser checkout + add release-health helper (2a6685c)## [0.22.65] - 2026-04-30

### Features

- **portal:** Add Bifrost portal stub + config template (phase 1, no runtime dep) (3f69c0d)## [0.22.64] - 2026-04-30

### Features

- **cli:** Add 'apm import' verb (apm.yml → clawtool source registry, phase 1) (126795e)## [0.22.63] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.62 [skip ci] (b43445f)
### Features

- **catalog:** Add mcp-toolbox source entry + recipe for DB-MCP onboarding (9e2a2b7)## [0.22.62] - 2026-04-30

### Features

- **tools:** Expose chat-driven Onboard + Init MCP tools (OnboardStatus / InitApply / OnboardWizard) (f7782dd)## [0.22.61] - 2026-04-30

### Features

- **tools:** Populate UsageHint on every registered tool + coverage test (86c27e4)## [0.22.60] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.58 [skip ci] (1f1a5ff)
### Features

- **cli:** Emit structured InitSummary + ChatRender for chat-driven onboarding (d9110db)## [0.22.59] - 2026-04-30

### Tests

- **cli:** Add smoke-test covering every verb's --help and read-only listings (4683988)## [0.22.58] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.57 [skip ci] (01683b9)
### Features

- **tools:** Add UsageHint field surfacing curated guidance via annotations.clawtool (48cc03a)## [0.22.57] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.56 [skip ci] (92336d5)
### Features

- **rules:** Add rtk pre_tool_use rewrite rule + recipe for Bash token compression (8f4ed6f)## [0.22.56] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.54 [skip ci] (fc27276)
### Features

- **recipes:** Mark mattpocock-skills as Core for default install (4899fe2)
- **setup:** Add Core recipe flag + auto-install path in onboard / init --all (deb3f45)## [0.22.55] - 2026-04-30

### Fixes

- **cli:** Distinguish dev-build-ahead-of-latest from already-on-latest in upgrade UX (d6ea921)## [0.22.54] - 2026-04-30

### Features

- **recipes:** Add mattpocock skills recipe for engineering daily-use playbook (db972aa)## [0.22.53] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.50 [skip ci] (18675f3)
### Features

- **agents:** Wire rules engine pre_send evaluation into dispatch path (dd38f88)## [0.22.52] - 2026-04-30

### Features

- **cli:** Add 'source inspect' verb wrapping MCP Inspector (d106334)## [0.22.51] - 2026-04-30

### Features

- **recipes:** Add promptfoo redteam recipe for BIAM dispatch eval (18cff98)## [0.22.50] - 2026-04-30

### Features

- **cli:** Add --backend flag to 'source registry' for Smithery probe (34a166a)## [0.22.49] - 2026-04-30

### Documentation

- **playbooks:** Add Mastra HTTP-agent setup playbook (93b9575)## [0.22.48] - 2026-04-30

### Features

- **agents:** Add Aider as BIAM transport peer #6 (9748391)## [0.22.47] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.46 [skip ci] (03324b5)
### Features

- **catalog:** Add Smithery registry probe alongside MCP Registry (1b22a81)## [0.22.46] - 2026-04-30

### Chores

- **deps:** Bump charmbracelet x/ansi v0.11.7 + colorprofile v0.4.3 (d09daaf)
### Features

- **playbooks:** Add 10xProductivity-style markdown playbook layer (d49230a)## [0.22.45] - 2026-04-30

### Tests

- **server:** Extend surface-drift to slash-command body references (9856982)## [0.22.44] - 2026-04-30

### Features

- **tools:** Expose `SourceRegistry` MCP tool for ecosystem discovery (20d4f6a)## [0.22.43] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.42 [skip ci] (1a8bf08)
### Features

- **cli:** Add `source registry` verb to probe the MCP Registry (cdecb8c)## [0.22.42] - 2026-04-30

### Features

- **catalog:** Add MCP Registry probe foundation (bcfb0c3)## [0.22.41] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.40 [skip ci] (c131a2a)
### Features

- **cli:** Add `--dry-run` to `agent new` (2ec79b9)## [0.22.40] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.39 [skip ci] (eb72756)
### Features

- **cli:** Add `--dry-run` to `portal remove` (7043893)
- **cli:** Add `--dry-run` to `source rename` (ca918b8)
- **cli:** Add `--dry-run` to `source remove` (b364ec6)
- **tools:** Expose `SourceCheck` MCP tool for credential probe (61b9828)
- **cli:** Add --json + single-instance filter to `source check` (ddabc05)
- **cli:** Add --json output to `bridge add` and `bridge upgrade` (63dea0c)
- **tools:** Expose `AgentDetect` MCP tool for host-adapter probe (47dba2f)
- **cli:** Add --json output to `sandbox doctor` (0759329)
- **cli:** Add `agents detect <agent>` exit-code probe (ef4c698)
- **cli:** Add --dry-run to `skill new` (44a9819)
- **cli:** Add --dry-run to `rules remove` (71f07fc)
- **cli:** Add --dry-run to `rules new` (5824012)
- **cli:** Add --json output to `agents claim` and `agents release` (64e02fc)
- **cli:** Add --format json|tsv|table to `skill list` (49c4bc5)
- **cli:** Add --json output to `rules show` (278bc1f)
- **cli:** Add --json output to `rules list` (7d337a8)
- **cli:** `version --check` exit-code probe for monitoring scripts (df8ab31)
- **tools:** Expose `Version` MCP tool with BuildInfo snapshot (45a79f2)
- **server:** Expose BuildInfo on /v1/health under `build` key (54bf658)
- **version:** Structured `clawtool version --json` output (239eede)
- **cli:** Onboard wizard `b` keybind navigates back one step (c85d974)
- **cli:** Add --json output to agents list (7dc8ad0)
- **cli:** Add --json output to agents status (6ae3fbf)
### Fixes

- **cli:** Emit header-only TSV from `skill list` on empty state (012abc0)
- **cli:** Emit `[]` from `hooks list --format json` on empty state (3af47eb)
- **cli:** Emit `[]` from `portal list --format json` on empty state (d71b78e)
- **cli:** Emit `[]` from `sandbox list --format json` on empty state (83436bf)
- **cli:** Emit `[]` from `source list --format json` on empty state (18aed7e)
- **build:** Makefile resolves GO via PATH first, fallback to legacy (7c16197)
- **test/e2e:** Switch echo|grep assertions to here-strings (d53ee15)
- **version:** Update-check reads Resolved() instead of bare const (3acf2ee)
- **bash:** Drain stdout/stderr before cmd.Wait to satisfy os/exec contract (acbb134)
### Refactor

- **cli:** Migrate `hooks list` to listfmt.RenderOrHint (ff1e9cb)
- **cli:** Migrate `portal list` to listfmt.RenderOrHint (7b5be2e)
- **cli:** Migrate `sandbox list` to listfmt.RenderOrHint (b6d7caf)
- **cli:** Migrate `source list` to listfmt.RenderOrHint (f024f06)
- **cli:** Introduce listfmt.RenderOrHint helper for empty-state contract (d7cbe60)## [0.22.39] - 2026-04-30

### Documentation

- **changelog:** Regenerate for v0.22.38 [skip ci] (2857f25)
### Features

- **cli:** Responsive onboard layout for narrow terminals (2720a87)
- **cli:** Polish onboard TUI with sidebar layout + ASCII banner (35cd4d8)
- **cli:** Onboard wizard resume + re-entry guard (492f544)
- **cli:** Rewrite onboard as Bubble Tea wizard with alt-screen (9551018)
### Fixes

- **a2a:** Drain in-flight peer-registry saves before t.TempDir cleanup (cfba933)
- **cli:** Onboard form renders all options at natural size (d1a5e74)
- **cli:** Onboard form fills card area instead of compressing to one row (8540a69)
- **cli:** Onboard TUI gate falls back to os.Stdin when App.Stdin nil (df44f29)
### Refactor

- **cli:** Pin onboard step card to fixed silhouette + centre content (695bce6)
- **cli:** Drop huh embed; ship custom Select / MultiSelect / Confirm (9ed5304)
- **cli:** Bring back outer rounded card; fix form clamping properly (bb7202c)
- **cli:** Drop nested card frame around onboard step content (67a107b)
- **cli:** Onboard TUI fills viewport responsively (2ec851c)
- **cli:** Redesign onboard TUI per Charm style patterns (7b5980d)
### Style

- **cli:** Bottom-align logo with metaCol + balance body slack vertically (bed72f5)
- **cli:** Vertically centre logo against meta column in header (ba404aa)
- **cli:** Move animation onto the clawtool logo (gradient shimmer) (fe5e269)
- **cli:** Fix W glyph in logo + add Braille spinner to step indicator (062e012)
- **cli:** Swap onboard logo + animate active progress dot (b6c8534)
- **cli:** Widen onboard card + polish header banner (c30561b)
- **cli:** Centre onboard wizard horizontally in viewport (e17bd19)## [0.22.38] - 2026-04-29

### Documentation

- **changelog:** Regenerate for v0.22.36 [skip ci] (4a4448c)
### Features

- **onboard:** Clear-screen entry + boxed header + structured phase output (9749d4f)
- **telemetry:** Host fingerprint + GeoIP suppression for Microsoft-level diagnostics (bec137f)## [0.22.36] - 2026-04-29

### CI

- **scripts:** Single-command CI runner with all gates including container e2e (7e173e1)
### Documentation

- Surface peer mesh + audit cleanup in README (57af3f8)
- **changelog:** Regenerate for v0.22.35 [skip ci] (44fc8f6)
### Features

- **telemetry:** Auto-stamp $lib_version on every event for PostHog version filtering (2370d8b)
- **telemetry:** Forward classified daemon log events to PostHog (2c184e4)
- Feat(a2a): peer-to-peer messaging — inbox primitive + status-fidelity hooks Phase 1 was discovery-only (registry + listing). This adds
the *messaging* half so two live sessions on the same host actually
talk to each other without going through MCP or the BIAM bridge
layer — answering "iki instance konuşabiliyor mu?" with a yes.

Daemon side (internal/a2a/inbox.go):
* Per-peer in-memory queue, soft cap 256 (drops oldest on overflow).
* Persisted at ~/.config/clawtool/peers.d/<peer_id>.inbox.json so
  daemon restart loses at most the last in-flight message.
* Wire shape mirrors repowire/protocol/messages.py — Query / Response
  / Notification / Broadcast — so a runtime hook can surface pending
  messages as additionalContext without inventing its own format.
* Deregister clears the inbox (no orphan state).

REST surface (internal/server/peers_handler.go):
* POST /v1/peers/{id}/messages — enqueue (404 on unknown peer)
* GET  /v1/peers/{id}/messages[?peek=1] — drain or peek
* POST /v1/peers/broadcast — fan-out, skips sender by from_peer

Runtime side (internal/cli/peer.go):
* clawtool peer send <peer_id|--name N|--broadcast> "<text>"
* clawtool peer inbox [--peek] [--format table|json|tsv]
* --name resolves via daemon's /v1/peers list; ambiguous names fail.

Status-fidelity hooks (hooks/hooks.json):
* UserPromptSubmit → heartbeat busy   (Claude is thinking)
* Notification    → heartbeat online  (Claude went idle)
So `clawtool a2a peers` STATUS column reflects "actually working"
vs "waiting at prompt", lifted from repowire's notification_handler.

Tests: 6 new httptest cases (send/drain, peek-keeps, 404 unknown,
empty-text rejection, broadcast skips sender, deregister clears
inbox). Existing claude-bootstrap, registry, and cli suites still
green — go test ./... clean.

Verified live round-trip: alice (claude-code) → bob (codex) by
display_name delivers; second drain empty; broadcast hits bob but
not alice's own inbox; peek-twice shows same messages without
consuming; UserPromptSubmit-style busy heartbeat flips status
correctly. (4431499)
- **a2a:** Peer discovery — registry, REST surface, runtime-side primitives (336d6b6)
- **telemetry:** Pre-v1.0 opt-out lock — telemetry stays on through the development cycle (9c100bd)
- **telemetry:** PostHog session boundaries + LLM observability allow-list (95bc9b7)
- **doctor:** Repowire uninstall-plan section + close SetContext drift (f0ad75f)
- **tools:** Octopus SetContext + GetContext — ambient editor context for the daemon (c39519e)
- **cli:** Repowire listfmt rollout — source/sandbox/portal/hooks list grow --format (bd3e25e)
- **cli:** Repowire listfmt — table | tsv | json output for `clawtool bridge list` (ae05078)
- **secrets:** Octopus env-scrub — strip secret-shaped vars from Bash + bg subprocess spawn (7fb9f3c)
- Feat(telemetry): wire $session_id + $lib so PostHog Sessions view lights up's first parking-table row (sessions) was the operator's
2026-04-29 observation: events flow but PostHog's Sessions tab is
empty + the live feed reads as sparse. Root cause: we never set
the PostHog-reserved $session_id, $lib, or $lib_version
properties — the strict allow-list dropped them silently if a
caller did try, and Track itself never injected them.

Fix:
1. Generate a 16-byte hex sessionID on Client construction
   (newSessionID, fresh per New() — i.e. per daemon / CLI
   invocation, the right boundary for a CLI tool).
2. Allow-list $session_id, $lib, $lib_version so they survive
   the property filter when callers do supply them.
3. Auto-inject $session_id and $lib="clawtool-go" in Track when
   the caller didn't set them. Caller-supplied values still win
   (e.g. a future cross-process trace propagation can override).

What this lights up in PostHog: the Sessions view groups events
emitted from the same daemon process, the live feed renders
"session X did A then B then C in 4s" rather than a flat row of
isolated events, and funnel queries can now filter on
$session_id to compute "of users who ran clawtool init, how many
ran clawtool send within the same session?"

Init log now reports the session ID alongside the distinct ID
(`enabled (host=…, distinct_id=abc12345…, session=xyz98765)`)
so the operator can correlate a local daemon to the rows
landing in PostHog when debugging.

Tests:
- TestAllowedKeys_PostHogSessionConventions — locks $session_id,
  $lib, $lib_version into the allow-list against future blind
  removals.
- TestNewSessionID_UniquePerCall — 100-iteration uniqueness
  smoke test (no collisions, ≥16-byte length, never empty). (0ddaeaa)
- **star:** Clawtool star — OAuth Device Flow (no CSRF replay) (31e350e)
- **upgrade:** Polished UX — boxed header, phased progress, release notes, next steps (ac2bfe5)
- **upgrade:** Self-restart daemon + auto-reconnect dashboard/orchestrator (6bc2e2e)
- **tools:** Redact secrets in BaseResult MarshalJSON + ErrorLine (96c3f0e)
### Fixes

- **upgrade:** Respawn daemon from install path, not the CLI's own executable (11295f5)
- **tools:** Drop BaseResult.MarshalJSON shadowing every tool's structured fields (5df6675)
- **a2a:** Thread session_id into identity tuple + read os.Stdin in peer (2cabe62)
- **e2e:** Unblock both container tests — version-prefix + Dockerfile heredoc + Debian base-files username collision (7d20a07)
### Refactor

- **xdg:** Add ConfigDirIfHome / DataDirIfHome / CacheDirIfHome (f7f21b0)
- **unattended:** Trust file round-trips through go-toml (b75a8cd)
- **xdg:** Add CacheDirOrTemp + collapse setup.WriteAtomic onto atomicfile (66e2c9c)
- **xdg:** Collapse 17 inline XDG-env-resolution callsites (b26a925)
- **atomicfile:** Collapse 14 inline temp+rename copies into one helper (fb093b7)
- **daemon:** Lift daemonRequest to internal/daemon as exported HTTPRequest (a32efb1)
- **cli:** A2a peers reuses peer.go's daemonRequest helper (5e81679)
- **core:** DefaultCwd helper for the cwd-defaulting pattern (0a547ca)
- **xdg:** One helper for XDG_CONFIG_HOME / STATE / DATA / CACHE (4376ad9)
- Bağla veya sil — yarım-kalmış test seam'leri (60be7fa)
- Drop 5 dead helpers, keep 6 yarım-kalmış future seams (b883ff1)
- Collapse 12-line + 8-line micro-files into their callers (a8608d3)
- Drop 4 dead min() shims + rename misleading read_legacy.go (2d97211)
- **cli:** Merge dashboard+orchestrator into one handler, share peers.d helper (9d508b1)
- **tui:** Collapse dashboard into orchestrator + add Peers tab (786eb2a)
### Tests

- **worker:** Cover Client.Read / Client.Write transport-error path (f22c193)
- **e2e:** Real-install Alpine fixture — install.sh + GitHub release + onboard end-to-end (568c542)
- **e2e:** Name + label e2e containers + add live-container upgrade scenario (befe1fe)
- **e2e:** Container test for binary-swap + daemon-restart flow (e887441)## [0.22.35] - 2026-04-29

### Documentation

- **changelog:** Regenerate for v0.22.34 [skip ci] (5ba4491)
### Tests

- **tui:** Orchestrator regression suite + LocalRulesPath walk-up (e0c81f7)## [0.22.34] - 2026-04-29

### Documentation

- **changelog:** Regenerate for v0.22.33 [skip ci] (6cd1418)
### Features

- **serve:** --debug flag + loud telemetry init + version.Resolved() in every emit (91f3d20)
### Fixes

- **rules:** Walk up to project root for .clawtool/rules.toml + RulesCheck wiring (c6bf1d2)## [0.22.33] - 2026-04-29

### Documentation

- **changelog:** Regenerate for v0.22.32 [skip ci] (745a055)
### Fixes

- **config:** Round-2 audit batch — secret leak, races, signal handling (eea198f)## [0.22.32] - 2026-04-29

### Documentation

- **changelog:** Regenerate for v0.22.31 [skip ci] (86c5fd6)
### Features

- **tui:** Orchestrator probes daemon /v1/health on connect, banners on version mismatch (0a677e1)## [0.22.31] - 2026-04-28

### Features

- **cli:** Tools export-typescript — code-mode stub generator (MVP) (0a261a0)## [0.22.30] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.29 [skip ci] (d4024e4)
### Fixes

- **egress:** Join CONNECT tunnels + force-close on shutdown (de4ece9)
- **daemon:** Flock spawn race + Runner.Stop join + ordered teardown (a5080f9)
- **biam:** Error-aware result publish, locked Close, awaited HTTP shutdown (a182a4f)## [0.22.29] - 2026-04-28

### Fixes

- **security:** Unattended trust+audit files 0o600; hooks shared-buffer race; SKILL routing for TaskReply (d96d23b)## [0.22.28] - 2026-04-28

### Features

- **biam:** TaskReply MCP tool + CLAWTOOL_TASK_ID env injection (fan-in) (5e7b44e)## [0.22.27] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.26 [skip ci] (e2bb088)
### Fixes

- **tui:** Orchestrator right pane streams frames + uses real CreatedAt (c3b6389)## [0.22.26] - 2026-04-28

### Documentation

- Strip ADR refs from runtime user-facing strings (2f41735)
### Fixes

- **concurrency:** Join in-flight handlers + bound mergeCtx watcher (7feaf24)## [0.22.25] - 2026-04-28

### Documentation

- Strip internal doc IDs from user-facing surface (bbbdeda)
- **changelog:** Regenerate for v0.22.24 [skip ci] (521a7f0)
### Fixes

- **bash:** Join drain goroutines before flipping bg task to terminal (91eb514)## [0.22.24] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.23 [skip ci] (0fac54d)
### Fixes

- **server:** Use version.Resolved() for /v1/health + MCP serverInfo.version (f4d92c9)## [0.22.23] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.22 [skip ci] (154fc91)
### Fixes

- **server:** Kill stdio update_check spam + tag transport on every server.* event (b92783b)## [0.22.22] - 2026-04-28

### Fixes

- **biam:** Close broadcast-vs-unsubscribe race in WatchHub (573d9af)
### Refactor

- **biam:** Collapse no-op if/else in recordResult into linear flow (35ca6ff)## [0.22.21] - 2026-04-28

### Features

- **cli:** Tools list now shows the full MCP surface (dispatch, agent, task, recipe, bridge…) (4304148)## [0.22.20] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.19 [skip ci] (049111f)
### Fixes

- **config:** Make telemetry default-on honest on upgrade + persist explicit opt-out (5daa42b)## [0.22.19] - 2026-04-28

### Documentation

- **readme:** Note v0.22.18 telemetry verb + e2e harness, drop done roadmap items (9e0d992)
### Features

- **config:** Default telemetry on so the wizard's "pre-1.0 default = on" claim is honest (2493fcc)
- **doctor:** Add [telemetry] section with config-vs-process drift detection (54a092e)
### Tests

- **e2e:** Finish docker harness for `clawtool onboard --yes` (bd4e278)## [0.22.18] - 2026-04-28

### CI

- **release:** Handle goreleaser drift + concurrent-tag race in changelog regen (7278a5b)
### Documentation

- **readme:** Refresh roadmap — split shipped from pending, drop done items (51dedfb)
- **changelog:** Regenerate for v0.22.17 [skip ci] (612c8bd)
### Features

- **cli:** Wire `clawtool telemetry` subcommand + onboard `--yes` for unattended runs (0be7694)## [0.22.17] - 2026-04-28

### Documentation

- **cli:** Drop "Future:" section + dead "long form" hint from help (0ec89dc)## [0.22.16] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.15 [skip ci] (1960b5c)
### Features

- **onboard:** Auto-launch from install.sh + per-step telemetry + star CTA + dashboard banner (b1fc838)## [0.22.15] - 2026-04-28

### Tests

- **biam:** Also short-path the missing-socket dial test on darwin (d7eb4c6)## [0.22.14] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.13 [skip ci] (30e5a64)
### Tests

- **biam:** Use /tmp-rooted sockpath helper to dodge darwin 104-byte limit (3e7e992)## [0.22.13] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.12 [skip ci] (d17f7e7)
### Features

- **onboard:** Post-install nudges + README expansion (40c8778)## [0.22.12] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.11 [skip ci] (7bac219)
### Features

- **tui:** Orchestrator renders SystemNotification banner with 30s auto-fade (75d875c)## [0.22.11] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.10 [skip ci] (8b7da7b)
### Features

- **cli:** Onboard wizard asks for primary CLI + drives smart defaults (0f8617a)## [0.22.10] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.9 [skip ci] (fc2679c)
### Fixes

- **tui:** Orchestrator pane alignment + bound order list against snapshot floods (764a02b)## [0.22.9] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.8 [skip ci] (4fe0d59)
### Features

- **version:** Daemon-side update poller pushes inline banner via WatchHub on new release (454d092)## [0.22.8] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.7 [skip ci] (99b254f)
### Fixes

- **version:** Unify Resolved() so overview / upgrade / bootstrap report the same number (3167a7f)## [0.22.7] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.6 [skip ci] (651a232)
### Features

- **plugin:** SessionStart surfaces "clawtool update available" when newer release ships (2216e97)## [0.22.6] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.5 [skip ci] (1cb5809)
### Fixes

- **biam:** Route `clawtool send --async` through daemon dispatch socket so frames reach the orchestrator (6979e71)## [0.22.5] - 2026-04-28

### Documentation

- **changelog:** Regenerate for v0.22.4 [skip ci] (d8925c5)
### Features

- **tui:** Orchestrator Active/Done tabs + viewport-bounded sidebar; task list active-default (e54bce2)## [0.22.4] - 2026-04-28

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

- Feat(tui): orchestrator Phase 3 — live byte stream + theme + sidebar layout Phase 3. Orchestrator becomes the production "teammate panel":
left sidebar (sticky 28col) lists every active dispatch with status
pill + agent + message count, right pane is a bubbles/viewport that
renders the selected task's StreamFrame ringbuffer line by line as
the agent emits them. Tail-follow toggle, scrollback (pgup/pgdn,
home/end), reconnect (r), quit (q).

Layout inspired by gh-dash / k9s / lazygit conventions: header bar
+ sidebar + flex detail pane + status bar with key hints. Theme
package added — Catppuccin-ish palette, AdaptiveColor for light/dark
terminals, status pills with bg colour, focus borders.

Backend:

- internal/agents/biam/watchhub.go: StreamFrame type + SubscribeFrames /
  BroadcastFrame channel. Cap-256 buffer, drop-on-full so a slow
  consumer doesn't stall the publisher.
- internal/agents/biam/runner.go: readCappedBroadcast replaces
  readCapped — line-by-line scan via bufio, every line both appended
  to the persisted body AND broadcast as a StreamFrame. Body bytes
  are byte-identical to the old path; live consumers now see lines
  as they arrive rather than waiting for the final result envelope.
- internal/agents/biam/watchsocket.go: WatchEnvelope wrapping
  ({"kind":"task"|"frame", ...}) so a single connection multiplexes
  state transitions and stream lines. handleWatchClient subscribes
  to BOTH channels and emits one envelope per event.

Front:

- internal/tui/theme/theme.go: 22-style theme set — pane borders,
  status pills, stream caret, help-bar key/desc, success/warning/
  error semantics. AdaptiveColor everywhere. Default() singleton.
- internal/tui/orchestrator.go: rewritten end-to-end. OrchModel
  carries map[string]*orchTask (frames ringbuffer) + bubbles/viewport
  for the live stream. Sidebar + detail layout via lipgloss.JoinHorizontal.
  Header / footer rendered with theme styles.
- internal/tui/dashboard.go: reads new WatchEnvelope shape — task
  events still update the tasks pane, frames are skipped (orchestrator
  is the canonical live-stream surface).
- internal/cli/task_watch.go: envelope-aware. Stream frames render as
  inline tail lines with status="stream" so `task watch <id>` also
  shows live output without changing flags.

Tests:

- internal/tui/orchestrator_test.go rewritten — insert / terminal-
  stamp / sweep grace window / frame appending / ringbuffer cap.
- All packages race-clean (`go test -race ./...` green). (5e76d75)
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

- **onboard:** Surface sandbox-worker setup hint (387e65d)
### Features

- **cli:** `clawtool overview` — one-screen system status (ca98eb7)
- **doctor:** Sandbox-worker section + guided agent-ambiguity error (ddeb308)## [0.21.6] - 2026-04-28

### Chores

- **release:** V0.21.6 — claude.ai sandbox parity (a6b841f)
### Documentation

- **changelog:** Regenerate for v0.21.5 [skip ci] (9f6c33c)
### Features

- **egress:** Allowlist proxy binary (ccd809b)
- **skill:** SkillList + SkillLoad — on-demand mount (44ee058)
- **sandbox:** Worker phase 2 — daemon-side routing for Bash (b2f42d8)
- **sandbox:** Worker container — claude.ai parity (cf6f2c2)
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
- **unattended:** Inject elevation flags into upstream CLI args (5ba2370)## [0.21.4] - 2026-04-27

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

- **task:** `clawtool task watch` — stream BIAM transitions to Monitor (e057ba9)
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
- **a2a:** Phase 1 — Agent Card serializer + `clawtool a2a card` (c35328a)
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

- **unattended:** --unattended flag + per-repo trust + JSONL audit (474fa97)
- **checkpoint:** Commit core tool — Conventional Commits + Co-Authored-By block + rules gate (a9452be)
- **rules:** Predicate-based invariant engine + RulesCheck tool (9421e8c)
- **bridges:** Hermes-agent — fifth supported family (NousResearch, MIT, 120K stars) (16313bf)
- **agent:** User-defined personas — `clawtool agent new` + AgentNew tool (12c701c)
- **biam:** TaskNotify — edge-triggered fan-in completion push (9152d3d)
- **bash:** Background mode + BashOutput / BashKill (3e9a055)
- Feat(websearch): provider-neutral filter shape — domains / recency / country / topic continuation — WebSearch's last gap. Adds five
optional MCP args that map onto Brave's native API where possible
and fall back to local post-filtering otherwise.

- include_domains / exclude_domains (newline- or comma-separated):
  allow / deny lists matched as either exact host or registrable-
  suffix (so 'python.org' covers 'docs.python.org'). Applied locally
  in filterHitsByDomain() AFTER the backend call so the contract
  holds even when the backend silently ignored the flag.
- recency: '24h' | '1d' | '1w' | '7d' | '1m' | '1y'. Brave maps
  these to its 'pd' / 'pw' / 'pm' / 'py' freshness param via
  braveFreshness().
- country: ISO 3166-1 alpha-2. Brave reads it directly.
- topic: free-form string passed through; backends honour what
  they support.

Backend interface change: Backend.Search now takes a fifth arg,
SearchOptions{}. Brave updated; the mock test path passes
SearchOptions{}. Future backends (Tavily, Google CSE, SearXNG)
get the same shape and can map each field idiomatically.

Per we don't reimplement domain filtering — net/url
parsing isn't needed since backends emit normalised URLs and the
extractHost helper is 6 lines of strings.TrimPrefix + IndexAny.
Cheap, correct, no allocation per hit.

Tests: 3 new — splitFilterList covers comma + newline + space +
case folding; filterHitsByDomain covers include / exclude / suffix
match; braveFreshness covers the 7 mappings + bogus input. All
existing WebSearch tests preserved (signature update threaded
through one mock-Brave call site). (1ea710d)
- Feat(v0.18.6): core tools polish phase B — Glob .gitignore + WebFetch SSRF guard (partial — Glob + WebFetch). Grep / Bash / WebSearch
follow-ups land separately so each diff stays auditable.

Glob:
- .gitignore-aware traversal default-on. Inside a Git worktree
  shell to `git ls-files --cached --others --exclude-standard -z
  --deduplicate`, then run doublestar.PathMatch over the candidate
  set. Outside a worktree (or when the operator sets
  respect_gitignore=false) the legacy doublestar walker stays. Same
  ignore semantics as ripgrep, no new in-process gitignore matcher
  needed for v1 — Codex flagged the hybrid approach.
- include_hidden=false (default) drops paths whose any segment
  starts with '.'. Patterns that explicitly name a dot segment
  (e.g. '**/.env', '.config/**') override the filter so the agent
  can still target dotfiles when it means to.
- Engine label switches between 'doublestar' and
  'doublestar+git-ls-files' so the operator can see which path
  ran without re-reading the source.
- 2 new tests, 5 existing tests preserved (executeGlob signature
  changed to globArgs struct — call sites updated in-place).

WebFetch SSRF guard:
- Refuses targets whose hostname resolves to private / loopback /
  link-local / cloud-metadata IPs BEFORE the GET. Codex flagged
  this as 'security-first, do this BEFORE adding features'.
- 14 deny-list CIDRs cover RFC1918, loopback (v4 + v6),
  link-local + AWS/Azure/GCP metadata (169.254.169.254),
  carrier-grade NAT, IPv6 unique-local, multicast, unspecified.
- Redirect chain re-runs the guard via http.Client.CheckRedirect
  so a public 302 → private redirect can't slip through. Userinfo
  in redirect URLs refused (phishing vector).
- allow_private MCP arg lets operators opt back in for legitimate
  localhost fetches (dev server, /etc/resolv.conf-style probes).
  Default false. executeWebFetch threads the flag via context so
  CheckRedirect honours it on every hop.
- 3 new tests: loopback blocked, AWS metadata blocked, range
  membership table covers public IPs (8.8.8.8, 1.1.1.1) staying
  green. Existing 6 webfetch tests updated to pass
  allowPrivate=true since httptest binds 127.0.0.1.

Both verified locally (clawtool's full suite race-clean) plus
the CI Go-1.26 fix from 4ab2eaf is now green across Lint /
ubuntu / macOS / cross-compile. (ab1647c)
- Feat(v0.18.1): bwrap engine real Wrap — Profile→argv compiler + live sandbox enforcement. The bwrap adapter ships its actual Wrap() now:
the Profile compiles into bubblewrap CLI flags, cmd.Path becomes
the bwrap binary, the original argv lands as exec args after `--`,
and cmd.Env is rebuilt to honour the EnvPolicy allow/deny.
Per we never reimplement namespace setup — bwrap owns
that. clawtool's polish layer is the typed Profile-to-argv
translator.

Real-process verified (bwrap available on this WSL2 host):
  TestBwrap_LiveCat       — sandboxed `cat /etc/hostname` runs
                            inside bwrap and returns the host name
                            correctly while inhabiting an isolated
                            namespace tree.
  TestBwrap_LiveNetUnshare — sandboxed `bash -c 'echo > /dev/tcp/1.1.1.1/53'`
                            FAILS as expected (network mode
                            "none" → --unshare-net → empty network
                            namespace, no route to anywhere).

The compiler:
- Baseline flags (always on): --die-with-parent, --unshare-pid,
  --unshare-ipc, --unshare-uts, --unshare-cgroup-try, plus
  --proc /proc, --dev /dev, --tmpfs /tmp so almost every program
  finds its expected pseudo-fs without exposing host details.
- Network modes:
    none / loopback → --unshare-net (loopback is treated like
                       none for now; bwrap can't filter egress
                       and a future commit pairs this with an
                       nftables layer).
    allowlist       → --share-net + warning (egress filtering
                      lives outside bwrap's scope).
    open            → --share-net.
- Filesystem rules: ro → --ro-bind-try, rw → --bind-try,
  none → no flag (default "not visible"). Path expansion
  honours ${VAR} substitution against the host env, then makes
  relative paths absolute via filepath.Abs.
- Env policy: --setenv each survivor; deny patterns trump
  matching allow entries (operator can say "AWS_*" allow +
  "AWS_SECRET" deny → only AWS_DEFAULT_REGION makes it
  through). Wildcard support via filepath.Match.
- --chdir picks the first rw directory in the rule set, so
  CLI tools that need a sane cwd don't blow up landing in /.

Tests:
- 4 unit tests over buildBwrapArgs (network modes, env
  allow/deny, rw bind shape, baseline flags).
- 2 LIVE tests that actually exec bwrap and assert on the
  outcome (cat works, network really is unshared). Skipped
  cleanly when bwrap isn't on PATH so the suite stays
  portable.

Phase 3 deferred: --share-net + nftables egress allowlist
(Codex flagged this as "bwrap doesn't filter; needs an
external firewall"). Tracked in open questions. (01cd88e)
- Feat(v0.18.4): core tools polish phase A — Read hashes, Write Read-before-Write, Edit diff. Synthesised from parallel Codex (BIAM task 6435286b)
and Gemini (task c977810b) audits against Cursor / Cline / Aider /
Cody best practice. Codex flagged the critical correctness point:
MCP session_id is NOT model-supplied — must come from
server.ClientSessionFromContext(ctx). Implemented exactly that.

Live-tested end-to-end against built binary:
  Read .../existing.txt → file_hash=a948904f2f0f... (SHA-256 verified)
  Read .../existing.txt with_line_numbers=true → render carries '   1 | hello world' prefix
  Write .../existing.txt content='new'  → REFUSED:
    'has not Read /tmp/.../existing.txt — Read it first (or pass mode="create" ...)'
  Edit .../multiline.go old='old' new='NEW' → returns diff_unified:
    --- a/.../multiline.go
    +++ b/.../multiline.go
    @@ -1,3 +1,3 @@

- internal/tools/core/session_state.go — SessionState + SessionKey,
  Sessions singleton, RecordRead / ReadOf / SessionKeyFromContext
  (uses server.ClientSessionFromContext, anonymous fallback for
  stdio/tests). HashFile + HashString + hashBytes helpers.
- internal/tools/core/session_state_helpers.go — readFileForHash
  shim so tests can stub disk reads without touching production
  ReadFile callers.
- internal/tools/core/read.go — ReadResult gains FileHash +
  RangeHash. runRead computes both after a successful read and
  records into the session registry. New with_line_numbers flag
  (default false) prefixes the rendered text with '%4d | ' —
  agents can reference lines accurately, JSON content stays raw
  so Edit's exact-substring matching keeps working.
- internal/tools/core/write.go — Read-before-Write guardrail.
  guardReadBeforeWrite() runs before executeWrite. Three new args:
    mode: 'create' | 'overwrite' (default '')
    must_not_exist: bool
    unsafe_overwrite_without_read: bool
  Existing file + no prior Read on the session = error message
  pointing at the four ways to satisfy the check (Read first,
  mode='create', must_not_exist, or the explicit unsafe bypass).
  Stale detection: if file's current SHA-256 doesn't match the
  one recorded at Read time, refuse with 'changed since this
  session Read it'.
- internal/tools/core/edit.go — EditResult gains HashBefore,
  HashAfter, DiffUnified. unifiedDiff() emits a 'diff -u'-style
  patch (--- a/path / +++ b/path / @@ hunk / line-by-line walk),
  capped at 200 lines so multi-line rewrites don't bloat the
  response. lcsLen kept as a stub for the future LCS-driven
  hunk algorithm.
- internal/tools/core/session_state_test.go — 11 tests:
  hashBytes determinism, HashFile round-trip, Sessions
  record/lookup with isolation across keys + paths, anonymous
  fallback, prefixLineNumbers formatter, guard rejecting
  no-prior-Read, allowing after recorded Read, rejecting on
  stale hash, create-mode rejecting existing file, create-mode
  passing for new path, unsafe override bypassing guard.
- wiki/decisions/021-core-tools-polish.md (accepted) — full
  design + the eight items, two-phase rollout plan, hash strategy,
  MCP session id contract, open questions.

Phase B (next commit): Glob .gitignore default-on, Grep context
lines + multi-pattern, Bash background mode, WebFetch SSRF
guard, WebSearch filters. (ec2dd44)
- Dockerize clawtool — 15MB distroless static image + Compose stack (0713937)
- Feat(v0.18): clawtool sandbox surface + (bwrap/sandbox-exec/docker) lands. Synthesised from parallel BIAM async dispatches: Codex
(task 4468aa25) recommended `mcp`-style noun + native-flag composition
+ BIAM cancel fix; Gemini (task 87343e0f) recommended `vault` (rejected
— HashiCorp Vault collides) + Engine interface shape. Both reviewers
converged on bwrap (Linux/WSL2) / sandbox-exec (macOS) / docker
(fallback) + external-wrap-over-native-delegate.

This commit ships the SURFACE: profile parser, engine probes,
read-only verbs (list / show / doctor), MCP tool catalog. The
dispatch-time wrapping (clawtool send --sandbox <profile> actually
constraining the upstream agent) lands incrementally per:
v0.18.1 bwrap adapter, v0.18.2 sandbox-exec, v0.18.3 docker, v0.19
Windows. Same incremental pattern v0.16.4 used for `mcp` before
v0.17 filled in the generator.

Live smoke against built binary verified the full surface:
  clawtool sandbox list   → two configured profiles + bwrap engine
  clawtool sandbox show   → renders paths/network/limits correctly
  clawtool sandbox doctor → bwrap + docker both detected on this
                            WSL2 host, noop fallback always
                            available, bwrap selected as primary

- internal/config/config.go: SandboxConfig + SandboxPath +
  SandboxNetwork + SandboxLimits + SandboxEnv added next to
  PortalConfig. Schema covers paths (ro/rw/none), network
  policy (none/loopback/allowlist/open), allow list, env
  allow + deny, timeout / memory / CPU shares / process count.
- internal/sandbox/sandbox.go: Engine interface (Name/Available/
  Wrap), Profile type, ParseProfile (validates modes + network
  policy + duration + byte sizes), parseBytes ("1GB", "512M",
  raw), SelectEngine (priority order, falls through to noop),
  AvailableEngines (for doctor).
- internal/sandbox/bwrap_linux.go: bubblewrap engine probe.
  Available() looks for bwrap on PATH. Wrap() returns a
  deferred-feature error pointing at v0.18.1 (matching the
  pattern v0.16.1 used for portal ask).
- internal/sandbox/sandbox_exec_darwin.go: macOS sandbox-exec
  probe + deferred Wrap (v0.18.2).
- internal/sandbox/docker_anywhere.go: cross-platform fallback.
  Available() runs `docker info` to check the daemon, not just
  the client binary. Deferred Wrap (v0.18.3).
- internal/sandbox/sandbox_test.go: 7 tests (full-shape parse,
  bad mode, bad network policy, allow-without-allowlist,
  parseBytes table, SelectEngine non-nil, AvailableEngines
  includes noop).
- internal/cli/sandbox.go: list / show / doctor / run dispatcher.
  list iterates configured profiles + reports the selected engine.
  show parses one profile through ParseProfile + renders all
  fields. doctor walks every registered engine + Available.
  run is the escape hatch (deferred error today).
- internal/tools/core/sandbox_tool.go: SandboxList / SandboxShow /
  SandboxDoctor MCP tools. SandboxRun deliberately omitted —
  letting a model spawn sandboxed commands has the wrong default.
- ToolSearch indexes the three new MCP tools.
- topUsage block in cli.go updated.
- docs/sandbox.md walks engines / profile schema / per-agent
  default / native composition / failure modes.
- wiki/decisions/020-sandbox-feature.md (accepted) — full design
  including the `[sandboxes.X.native]` sub-stanza Codex
  contributed and the BIAM cancel fix Codex flagged at
  internal/agents/biam/runner.go:61. (8c81e37)
- Clawtool uninstall — full footprint cleanup (ce9bed7)
- Feat(v0.17): clawtool mcp generator — Go / Python / TypeScript scaffolds generator lands. `clawtool mcp new <name>` walks the operator
through a huh.Form wizard (or `--yes` for defaults) and writes a real,
compilable MCP server. Per each language adapter wraps the
canonical SDK in its ecosystem.

Live smoke against built binary verified the full chain:
  clawtool mcp new my-thing --yes  → 9 files including Go server.
  go mod tidy && go build ...      → 6.7MB binary.
  echo '<initialize JSON-RPC>' | ./bin/my-thing
                                   → correct serverInfo response.
                                   The server actually speaks MCP.
  clawtool mcp install . --as smoke-test
                                   → [sources.smoke-test] in config.toml.
  clawtool mcp list --root <dir>   → discovers the scaffold.

- internal/mcpgen/: package for the generator.
  - mcpgen.go — Spec / ToolSpec / File / Adapter interface +
    Generate orchestrator + name validators + writeFile guard.
  - common.go — language-agnostic files: .clawtool/mcp.toml marker,
    README, .gitignore, .claude-plugin/plugin.json (opt-in).
  - go_adapter.go — mark3labs/mcp-go v0.49.0. cmd/<name>/main.go +
    internal/tools/example.go + Makefile + go.mod + (opt-in)
    Dockerfile.
  - python_adapter.go — fastmcp ≥0.4. src/<pkg>/ layout +
    pyproject.toml + Makefile + tests/.
  - typescript_adapter.go — @modelcontextprotocol/sdk ≥1.0.
    src/server.ts + tools/ + package.json + tsconfig + test/.
  - mcpgen_test.go — 12 tests: per-language plan, docker opt-in,
    plugin opt-out, refuses existing dir, name + tool name + language
    validators.

- internal/cli/mcp_wizard.go: huh.Form sequence (description,
  language, transport, packaging, plugin manifest, first tool).
  --yes path uses minimal defaults (Go / stdio / native / one
  echo_back tool). mcpgenDeps interface lets tests drive without
  TTY.

- internal/cli/mcp_install.go: reads .clawtool/mcp.toml, derives
  the launch command from language + packaging, writes
  [sources.<instance>] into config.toml. Same registry the
  catalog (clawtool source add) populates — no new code path in
  internal/sources/manager.go.

- internal/cli/mcp.go: rewired from v0.16.4 stub to real impls.
  mcp list now does filepath.Walk skipping noise dirs. mcp run /
  mcp build shim through the project's Makefile (per:
  don't reinvent build orchestration).

- internal/tools/core/mcp_tool.go: McpNew + McpList wired to the
  real generator + walker. McpRun / McpBuild / McpInstall surface
  a hint to invoke the CLI shortcut (those touch the operator's
  filesystem + language toolchain so the model giving advice
  is the natural pattern, not driving the build via MCP).

- internal/cli/mcp_test.go: wizard --yes happy path + bad-name
  rejection + existing-dir refusal + walker discovery.

Total surface: 5 CLI verbs, 5 MCP tools, 12+ unit tests, real
end-to-end smoke. README + docs/mcp-authoring.md updated to
"v0.17 shipped". Wiki log entry captures the design + smoke
results. (b6a3359)
- Feat(v0.16.4): clawtool mcp authoring noun + surface lands. `mcp` is the new authoring noun for MCP server source
code, sister to `skill` (Agent Skills). Co-designed with Codex (task
55a5a480) and Gemini (task 13d4ea86) in parallel BIAM async
dispatches; synthesis preserves Codex's naming + repo-relative
output, both reviewers' .claude-plugin/ day-one + operator-managed
marketplace.

This commit is the SURFACE STUB — generator (`mcp new / run / build /
install`) lands in v0.17. Same deferred-feature pattern v0.16.1
used for `portal ask` before v0.16.2 wired the CDP driver: surface
booked today so agents discover the namespace early; rewriting it
post-adoption isn't free.

- internal/cli/mcp.go: CLI subcommand dispatcher.
  - `mcp list` ships read-only (walker stub; upgrades when generator
    writes .clawtool/mcp.toml markers).
  - `mcp new / run / build / install` return McpNotImplementedError
    sentinel pointing at.
- internal/tools/core/mcp_tool.go: McpList / McpNew / McpRun /
  McpBuild / McpInstall MCP tools. RegisterMcpTools wired alongside
  RegisterPortalTools in server.go.
- internal/tools/core/toolsearch.go: 5 new entries so ToolSearch
  surfaces the surface.
- internal/cli/cli.go topUsage block: `clawtool mcp ...` near
  `clawtool skill ...`, with one-liner clarification (mcp = MCP
  server source code; skill = Agent Skill folder).
- README.md hero block: MCP authoring bullet alongside Browser
  tools / Portals.
- docs/mcp-authoring.md: full preview — wizard prompts, per-language
  artifact, install flow, today's interim hand-roll path.
- wiki/decisions/019-mcp-authoring-scaffolder.md (accepted), with
  cross-refs to / 007 / 008 / 010 / 014 / 018.
- wiki/log.md: design synthesis captured (Codex `mcp` + Gemini
  `forge` reviewers) plus the chromedp lesson from v0.16.3. (8301353)
- **v0.16.3:** Portal add interactive wizard (chromedp + Chrome) (3532ffa)
- **v0.16.2:** Portal CDP driver — Ask flow + per-portal MCP aliases (8067955)
- **v0.16.1:** Portal feature — saved web-UI targets (0171284)
- Feat(v0.16): BrowserFetch + BrowserScrape — Obscura-backed JS render stays untouched: browser is a Tool surface, not a Transport.
clawtool wraps github.com/h4ckf0r0day/obscura (Apache-2.0, V8 + Chrome
DevTools Protocol, 30 MB memory vs Chromium's 200+) per so
agents can render SPA / hydrated pages without us hand-rolling a
headless engine.

- BrowserFetch (internal/tools/core/browser_fetch.go): stateless
  single-URL render via `obscura fetch --dump html | --eval ...`. Result
  shape mirrors WebFetch (title / byline / sitename / content) plus
  optional eval_result so agents can swap the two without rewriting
  parsing. Optional CSS-selector wait, --stealth pass-through.
- BrowserScrape (internal/tools/core/browser_scrape.go): bulk parallel
  via `obscura scrape ... --concurrency N --eval ... --format json`,
  hard cap 500 URLs / 50 workers. Tolerates both NDJSON and JSON-array
  output; per-URL errors fold into the row so the batch keeps going.
- engines.go now caches `obscura` alongside `rg` / `pdftotext`. Missing
  binary surfaces a one-shot install hint (Linux/macOS one-liners) at
  call time — no boot-time refusal.
- Tests cover the missing-binary, bad-URL, HTML readability, eval
  pass-through, non-zero exit paths plus the NDJSON/array parser and
  the URL splitter helper. Race-clean.
- Both registered in server.go (always-on) and indexed in
  CoreToolDocs so ToolSearch surfaces them.
- docs/browser-tools.md walks through install, the two tool schemas,
  worked Next.js + bulk-scrape examples, failure modes, and the
  reasoning for picking Obscura over Headless Chrome. README links it
  from the v0.15 hero block. The cookie-driven interactive surface
  (BrowserAction, CDP-over-WebSocket) lands as a follow-up commit
  because cookie injection requires the obscura serve transport, not
  the fetch CLI. (6cbec23)
- **v0.15:** F5 telemetry + F6 hooks CLI + F7 process-group reaping + README (9096d7b)
- **v0.15:** F3 hooks subsystem + F4 clawtool onboard wizard (71334d8)
- **v0.15:** Per-instance rate limiter (F1) + clawtool upgrade subcommand (F2) (9b74041)
- **biam:** Ship Phase 1 (async dispatch + signed envelopes + SQLite store) + 3 polish fixes (42b4889)
- **v0.14:** T3 mem0 + T5 git-worktree isolation + T6 SemanticSearch (148f001)
- **v0.14:** T1 OTel + T2 auto-lint + T4 Verify MCP tool (22994f7)
- **serve:** POST /v1/recipe/apply + GET /v1/recipes + --mcp-http transport, plus claude/gemini transport fixes from live smoke (4b843ba)
- **supervisor:** Ship Phase 4 of — dispatch policies (round-robin, failover, tag-routed) (d806663)
- **relay:** Ship Phase 3 of — Docker image + clawtool-relay recipe (94130c2)
- **serve:** Ship Phase 2 of — clawtool serve --listen HTTP gateway (be91f9f)
- **agents:** Ship Phase 1 of — Transport, Supervisor, send/bridge CLI, MCP tools (c875a54)
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

Grep upgrades ( continuation):

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

- **portal:** Swap hand-rolled CDP for chromedp (e6af0f2)
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
- Feat(setup): foundation for clawtool init — recipes, runner, repo-config codified: clawtool init is an injector that wraps upstream
tools, never reimplements them. This commit lands the framework
recipes plug into:

  internal/setup/category.go     — 9 frozen categories (governance,
                                   commits, release, ci, quality,
                                   supply-chain, knowledge, agents,
                                   runtime). Set is the v1.0 API
                                   contract; adding a category is a
                                   major bump.
  internal/setup/recipe.go       — Recipe interface + Registry. Meta
                                   requires Upstream as a non-empty
                                   field, so the wrap-don't-reinvent
                                   rule is compile-time enforced —
                                   a from-scratch reimplementation
                                   literally won't register.
  internal/setup/runner.go       — stitches Detect→Prereqs→Apply→
                                   Verify into one Apply call with
                                   Prompter (TTY/MCP/auto) and
                                   CommandRunner abstractions.
  internal/setup/repoconfig.go   — .clawtool.toml load/save/upsert
                                   (atomic temp+rename, sorted
                                   recipe list for clean diffs).
  internal/setup/fs.go           — WriteAtomic + marker helpers
                                   shared across recipe packages.

First recipe under the new framework: conventional-commits-ci
(category: commits) wraps amannn/action-semantic-pull-request.
Drops a marker-stamped workflow, refuses to overwrite anything
the user wrote themselves.

29 unit tests, race-clean. No CLI/MCP wiring yet — that lands in
follow-up commits per the v0.9 milestone.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com> (1afde74)
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
