---
type: decision
title: "009 Versioning policy and tooling"
aliases:
  - "009 Versioning policy and tooling"
  - "ADR-009"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - process
  - release
status: developing
related:
  - "[[004 clawtool initial architecture direction]]"
  - "[[007 Leverage best-in-class not reinvent]]"
sources: []
---

# 009 — Versioning Policy and Tooling

> **Status: developing.** Process ADR. Locks how we version clawtool, how
> commits are written, and how the changelog is produced — so the path to
> v1.0 is paced and credible rather than arbitrary.

## Context

Between 2026-04-26 morning and afternoon, clawtool went from v0.1 (a
"hello, MCP" stub) to v0.7 (full proxy + 7-tool canonical surface) in
roughly four hours of session work. Every increment was a "minor" bump.

The user pushed back:

> *"v0.8'den sonra kardeşim v0.8001'den başlayacaksın çok hızlı v1.0'a
> gidiyoruz henüz ürün potansiyeli yok."*
>
> After v0.8 you'll start at v0.8001 — we're going to v1.0 too fast,
> there's no product potential yet.

The instinct is correct. Semantic versioning's contract is that **v1.0
means the public API is stable and the project is fit for production
use**. clawtool isn't there: no plugin marketplace, no signed releases,
no backwards-compatibility commitments, no observability story, no
multi-platform CI matrix. Inflating the version doesn't change those
facts; it just lies about them.

The same conversation also asked us to wrap (per ADR-007 discipline)
mature release tooling rather than improvise:

> *"Projeyi yönetme noktasında kullanabileceğimiz paketler var ise
> versiyonlamaya yönelik doğru şekilde onları da kullanabiliriz."*

So this ADR locks two things at once: **what** version numbers mean, and
**which tools** generate / police them.

## Decision

### 1. Semver discipline, slow lane

Until clawtool can credibly call itself v1.0, we stay in the v0.x.y range
with these rules:

| Bump | Trigger | Examples |
|---|---|---|
| Patch (`x.y.Z`) | New tool or new format that does not break existing tool surface; bug fix; doc; refactor; deps; tests | "Add WebSearch Tavily backend"; "fix Edit CRLF on Windows"; "bump bleve" |
| Minor (`x.Y.0`) | Breaking change to a tool surface; new ADR that obsoletes a previous decision; major dep swap (e.g. ripgrep→ag) | "rename `cwd` → `working_dir` in all tools"; "switch to Cobra CLI"; "drop Glob `pattern` arg" |
| Major (`X.0.0`) | clawtool reaches v1.0 = production-ready. **No major bumps before then.** | (future) |

In practice this means: **most increments are patch-level**. v0.8.0
ships the canonical core list (Edit + Write completing it); v0.8.1, v0.8.2 …
fill in source-catalog entries, polish, additional WebSearch backends,
gitignore-aware Glob, OCR for Read, etc. v0.9 only happens when something
breaks the existing tool surface.

### 2. v1.0 gating criteria

We commit not to bump v1.0 until **all** of these are true:

- A real-world user has run clawtool against a production agent setup
  for at least one week without filing a usability bug.
- The full canonical core list is shipped (✅ as of v0.8.0) and each
  core tool has been smoke-tested on at least Linux + macOS (Windows
  best-effort).
- A signed binary release pipeline exists (GoReleaser + GitHub Actions).
- A versioned API stability promise is documented: which fields in tool
  outputs are guaranteed not to change without a major bump.
- Multi-instance source spawning is verified end-to-end against at least
  three real upstream MCP servers (not just the stub).
- Plugin packaging exists for at least Claude Code (`claude plugin
  install clawtool@…`) so the install path matches ADR-008's "Layer 2".

This list lives in the `v1.0` GitHub milestone (created when the
remote repo lands) and items move out as they're checked off. v1.0
ships the day the milestone reaches zero.

### 3. Conventional Commits

Every commit subject from this ADR forward starts with a Conventional
Commits prefix (https://www.conventionalcommits.org/):

```
feat(scope): add Tavily backend to WebSearch
fix(read): preserve BOM during xlsx export
docs(adr-009): clarify v1.0 gating criteria
chore(deps): bump bleve to v2.x
build(makefile): add release target
refactor(sources): extract spawn lifecycle to Instance.start
test(edit): cover empty-string old_string edge case
```

Why: this format gives `git-cliff` deterministic input to produce a
human-readable changelog without any annotation step. Bonus: any future
contributor (human or agent) sees from the message alone what the
intent of the change was.

The first commit on `main` written in this style is the one that adds
this ADR.

### 4. git-cliff for changelog

Per ADR-007 "wrap, don't reinvent":

- **`git-cliff`** (Rust binary, MIT, ~10 MB, no runtime deps) generates
  `CHANGELOG.md` from commit history.
- Config lives at `cliff.toml` in repo root.
- `make changelog` (already wired in this commit) regenerates the file.
- `make release` (future, v0.9+) will tie tag → CHANGELOG entry → binary
  artifact production via `goreleaser`.

We deliberately skip `semantic-release` (Node-heavy, opinionated about
the publish step) and `release-please` (GitHub-only) because clawtool
should be self-hostable — every step of the release flow runs from a
single binary on a workstation.

### 5. Tags

We tag every shipped version with `vX.Y.Z` (annotated, signed when
keys exist). Tag list is the source of truth for "what was released
when"; the changelog is generated from those tags.

The first tag was `v0.8.0` (canonical core complete). `v0.8.1` is this
ADR + tooling commit.

## Alternatives Rejected

- **Keep bumping minors aggressively.** Inflates the version and breaks
  semver's contract with users.
- **Use 4-digit patch (`v0.8.0001`) as the user suggested literally.**
  Considered. Rejected because semver's parser ecosystem (Go modules,
  Cargo, npm, GitHub releases) all expect three components. We honor
  the *intent* — patch-level granular bumps — without breaking tooling.
- **Drop semver entirely; use date-based versioning (CalVer).** Tempting
  for a tool with continuous delivery, but semver's "breaking change =
  major bump" promise is more useful for downstream agents than CalVer's
  date stamp.
- **`semantic-release`** for fully-automated bumps from commit messages.
  Heavyweight (Node + plugins), opinionated about publish target. We can
  add it later if commit-driven auto-bumps prove valuable; for now
  manual `git tag` is fine.
- **Hand-write CHANGELOG.md.** Drift-prone and slow. git-cliff fixes
  both for free.

## Consequences

- **Patch-rate, not minor-rate.** Expect v0.8.1, v0.8.2, … many of them
  before v0.9. v0.9 only when something breaks an existing surface.
- **No v1.0 until the gating criteria are checked.** It's fine to ship
  features for years inside v0.x.y.
- **Commit hygiene matters.** A commit not in Conventional Commits
  format gets rejected by `git-cliff` and ends up in the "Other"
  section. CI may eventually enforce the format via a commitlint hook.
- **`CHANGELOG.md` is generated, not authored.** Editing it by hand is
  a bug. Real changes go in commit messages.
- **`make changelog`** regenerates the file from full history. Run it
  before tagging. v0.9+ will add `make release` that ties everything
  together.
- **The historical commits before this ADR don't follow Conventional
  Commits.** They land in the changelog's "Other" / "Genesis" /
  "Decisions" buckets per the `cliff.toml` fallback rules. We accept
  the cosmetic noise; rewriting history would lose more than it gains.

## Status

Developing. The first patch bump under this policy is v0.8.1 (the
commit that adds this ADR + cliff.toml + Makefile target). Going
forward, every commit subject must start with a Conventional Commits
prefix.
