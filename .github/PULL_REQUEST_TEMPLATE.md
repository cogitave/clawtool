<!--
Thanks for the contribution! Before submitting, please:
- Run `make test && make e2e` locally.
- Make sure the PR title follows Conventional Commits (e.g. 'feat(tools): add Foo'). The CI commit-format job enforces this.
- Reference any ADRs your change touches.
-->

## What

<!-- One paragraph: what does this change do? -->

## Why

<!-- One paragraph: what problem does it solve, or which ADR / issue does it implement? -->

## How verified

- [ ] `make test` passes
- [ ] `make e2e` passes
- [ ] New unit tests cover the changed code paths
- [ ] (For new tools) e2e assertion added under `test/e2e/run.sh`
- [ ] (For new ADR-impacting work) wiki updated under `wiki/decisions/` and `wiki/sources/`

## ADRs touched

<!-- Cross-link any ADR you touched, e.g. "ADR-005 (positioning), ADR-007 (engineering discipline)". Empty = no ADR-level change. -->

## Versioning

<!-- Per ADR-009: tick the bump kind. Maintainer cuts the tag after merge. -->
- [ ] Patch (non-breaking; default)
- [ ] Minor (breaks an existing tool / surface — explain below)
- [ ] No version bump needed (docs / chore / ci)

## Notes for the reviewer

<!-- Anything tricky, deferred, or worth flagging. -->
