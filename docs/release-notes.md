# clawtool release notes

The per-tag GitHub release body is produced by a self-hosted
script (`scripts/release-notes-rich.sh`) rather than by
goreleaser's built-in changelog or git-cliff's flat preamble.
This page documents the format and the goreleaser bypass step
the release workflow runs after publish.

## Why self-hosted

The previous output of `orhun/git-cliff-action` shipped a flat
`## clawtool vX.Y.Z` header plus a static install snippet — no
grouped commits, no scope callouts, no breaking-change block.
The rich script (v0.22.77) replaces that with:

- Header + 1-line summary derived from the highest-impact commit
  in the range (`breaking > feat > fix`).
- **Features** grouped by scope (catalog / agents / cli /
  recipes / rules / tools / portal / playbooks / setup) with
  per-scope emoji sub-headers.
- **Fixes** grouped by scope.
- Collapsed `<details>` block for docs / tests / chore.
- Loud Breaking Changes section for `type!:` or
  `BREAKING CHANGE:` body trailers.
- Static install snippet preserved verbatim.
- Stats footer: N commits, M contributors, +X/-Y lines,
  compare-link to the prior tag.

## Surface

```text
scripts/release-notes-rich.sh --from <tag> --to <tag>     # explicit range
scripts/release-notes-rich.sh --to <tag>                  # auto previous tag
scripts/release-notes-rich.sh                             # last tag → HEAD
```

`--from` defaults to the closest tag strictly before `--to`
(via `git describe --tags --abbrev=0 <to>^`); when no prior tag
exists (first release) the range starts at the root commit.
`--to` defaults to `$GITHUB_REF_NAME` when set (the CI path) or
`HEAD` otherwise.

Output: markdown to stdout. Consumed by goreleaser via
`--release-notes=BODY.md`.

## Scope → emoji map

| Scope | Emoji |
| --- | --- |
| `catalog` | 📦 |
| `agents` | 🤖 |
| `cli` | 🖥️ |
| `recipes` | 🍳 |
| `rules` | 🛡️ |
| `tools` | 🛠️ |
| `portal` | 🌉 |
| `playbooks` | 📓 |
| `setup` | ⚙️ |
| _other_ | 🔹 |

A new scope without a dedicated emoji renders with the generic
`🔹` — not a hard error, just a visual nudge to add a row to
`scope_emoji()` if the scope becomes a regular.

## Goreleaser bypass post-step

Goreleaser v2.15.4 silently drops `--release-notes` content on
publish — verified across v0.22.77 / .78 / .79 / .80 — leaving
the GitHub release body empty. The release workflow therefore
runs a belt-and-braces step right after the goreleaser action
to force-set the body via the `gh` CLI:

```yaml
- name: Force release body from BODY.md (goreleaser bypass)
  run: |
    set -euo pipefail
    if [ ! -s BODY.md ]; then
      echo "::warning::BODY.md missing or empty; skipping body edit"
      exit 0
    fi
    gh release edit "${GITHUB_REF_NAME}" --notes-file BODY.md
    echo "release body force-set from BODY.md"
  env:
    GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

The script's `BODY.md` is the canonical source; this step
guarantees GitHub displays it regardless of goreleaser's
silent-drop bug.

## Why goreleaser's own changelog is disabled

`.goreleaser.yaml`:

```yaml
changelog:
  disable: true
```

`release.header` / `release.footer` / `release.mode` were also
removed (2026-05-01). Reasons:

- The script produces the COMPLETE body — header, grouped
  commits, install snippet, stats. Templates duplicated content.
- The previous `release.header` template leaked an internal ADR
  reference into user-facing UX, violating the project's "no
  internal doc IDs in UX" rule.
- `release.mode=append` suppressed `--release-notes=BODY.md`
  (v0.22.79 fix).

Single source of truth lives in
`scripts/release-notes-rich.sh`.

## CHANGELOG.md (full history)

The per-release body is one artifact; the in-repo
`CHANGELOG.md` is the other. The release workflow regenerates
the full-history `CHANGELOG.md` via
`orhun/git-cliff-action@v4` (no `--latest` flag) and commits
it back to `main` with a `[skip ci]` trailer to avoid
double-firing the CI pipeline.

The two artifacts are deliberately separate:

| Artifact | Producer | Consumer |
| --- | --- | --- |
| GitHub release body | `scripts/release-notes-rich.sh` | release page on github.com |
| `CHANGELOG.md` | `orhun/git-cliff-action` | in-repo readers, archive view |

## Cross-references

- `.github/workflows/release.yml` — full CI definition.
- `.goreleaser.yaml` — release config; `changelog.disable: true`
  + the removed-templates comment block.
- `scripts/release-notes-rich.test.sh` — unit tests for the
  script's renderer.
- `scripts/release-health.sh` — a sibling helper that audits
  recent published releases for empty bodies / missing assets.
