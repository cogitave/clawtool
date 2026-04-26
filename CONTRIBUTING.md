# Contributing to clawtool

Thanks for considering a contribution. clawtool is a small focused tool — keeping it that way is a feature, not an oversight. Read [ADR-009](wiki/decisions/009-versioning-policy-and-tooling.md) for the versioning policy and [ADR-007](wiki/decisions/007-leverage-best-in-class-not-reinvent.md) for the engineering discipline before opening a non-trivial PR.

## Quickstart

```bash
git clone https://github.com/bahadirarda/clawtool
cd clawtool
make build      # build to ./bin/clawtool
make test       # 'go test -race ./...'
make e2e        # spawns the binary, runs MCP integration assertions
make install    # copy to ~/.local/bin (atomic temp+rename)
```

You'll need:
- **Go ≥ 1.25.5** (matches `go.mod`).
- **ripgrep** (`rg`) and **pandoc** + **poppler-utils** for full Read coverage. Linux: `apt install ripgrep pandoc poppler-utils`. macOS: `brew install ripgrep pandoc poppler`.

## Conventional Commits — required

Every commit subject must match the [Conventional Commits 1.0](https://www.conventionalcommits.org/) format:

```
<type>(<scope>)!?: <subject>

<body>
```

| Type | When |
|---|---|
| `feat` | New feature visible to users (a new tool, a new flag, a new format). |
| `fix` | Bug fix. |
| `perf` | Performance improvement with no behavioral change. |
| `refactor` | Internal restructure with no behavioral change. |
| `docs` | Docs (README, wiki, ADRs, comments) only. |
| `test` | Test code only. |
| `build` | Build / release / Makefile / GoReleaser / CI scripts. |
| `ci` | GitHub Actions workflow only. |
| `chore` | Routine maintenance (dependency bumps, file moves, formatting). |
| `style` | Formatting / whitespace; no logic change. |
| `revert` | Reverts an earlier commit (subject keeps the original under "Reverts:"). |

Use `!` after the scope to mark a breaking change (e.g. `feat(tools)!: rename cwd to working_dir`). Breaking changes are minor-version bumps (per ADR-009) until v1.0.

The `commit-format` job in `.github/workflows/ci.yml` enforces this on every PR title.

## Versioning — patches by default

Per ADR-009, until clawtool reaches v1.0:

- **Patch (`x.y.Z`)** for non-breaking adds (new tool, new format, new source backend, fix). Default.
- **Minor (`x.Y.0`)** only for breaking changes to existing tool surface.
- **Major (`X.0.0`)** reserved for v1.0 production-readiness.

If your change introduces a breaking surface change, bump the minor version and document the migration in the commit body. Otherwise the maintainer cuts a patch tag from your merged commit.

## Testing discipline

- **Every new tool, format, backend, or CLI subcommand ships with unit tests.**
- **Every new MCP-visible behavior ships with at least one e2e assertion.**
- `make test && make e2e` must pass before opening a PR.
- The CI matrix runs on Linux + macOS. If a test relies on a binary the runner doesn't have, install it in the workflow rather than skipping the test.

## Adding a new core tool

1. Identify the upstream engine (ADR-007: wrap, don't reinvent).
2. Add the row to [Canonical Tool Implementations Survey](wiki/sources/canonical-tool-implementations-survey-2026-04-26.md) with status "Adopted vX.Y.Z".
3. Implement under `internal/tools/core/<tool>.go` using the shared polish layer (`engines.go`, `atomic.go`).
4. Add `RegisterFoo(s)` and wire it in `internal/server/server.go` behind `cfg.IsEnabled("Foo")`.
5. Add the tool to `KnownCoreTools` in `internal/config/config.go` and append a descriptor to `CoreToolDocs()` in `internal/tools/core/toolsearch.go`.
6. Tests: `internal/tools/core/<tool>_test.go` + e2e assertions in `test/e2e/run.sh`.
7. Bump version per ADR-009; commit message starts `feat(tools): add Foo …`.

## Adding a new source to the catalog

1. Add the entry to `internal/catalog/builtin.toml`. Required fields: `description`, `runtime`, `package`, `required_env` (if any), `auth_hint`, `homepage`, `maintained`.
2. Add a regression test asserting the entry parses and produces the right argv.
3. `feat(catalog): add <name>` commit subject.

## Reporting bugs / requesting features

- Bug → file an issue with the `bug` template. Include `clawtool version`, OS, the exact MCP request that misbehaved, and the response body.
- Feature → `enhancement` template. State which ADR governs the area before proposing.
- Source request → `source-request` template. Catalog additions are usually trivial; we'll fast-track.

## Security

Never file a vulnerability via a public issue. See [SECURITY.md](SECURITY.md).

## License

By contributing, you agree your work is released under the [MIT License](LICENSE) — same terms as the rest of clawtool.
