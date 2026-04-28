# Contributing to clawtool

Thanks for considering a contribution. clawtool is a small focused tool — keeping it that way is a feature, not an oversight. Two non-negotiables before opening a non-trivial PR: (1) we are pre-1.0; patch bumps are the default and breaking changes go in minor bumps with a documented migration, and (2) we **wrap, don't reinvent** — every new core tool must adopt an existing best-in-class engine (ripgrep / pandoc / pdftotext / bleve / …) rather than ship a from-scratch implementation.

## Quickstart

```bash
git clone https://github.com/cogitave/clawtool
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
| `docs` | Docs (README, comments) only. |
| `test` | Test code only. |
| `build` | Build / release / Makefile / GoReleaser / CI scripts. |
| `ci` | GitHub Actions workflow only. |
| `chore` | Routine maintenance (dependency bumps, file moves, formatting). |
| `style` | Formatting / whitespace; no logic change. |
| `revert` | Reverts an earlier commit (subject keeps the original under "Reverts:"). |

Use `!` after the scope to mark a breaking change (e.g. `feat(tools)!: rename cwd to working_dir`). Breaking changes are minor-version bumps until v1.0.

The `commit-format` job in `.github/workflows/ci.yml` enforces this on every PR title.

## Versioning — patches by default

Until clawtool reaches v1.0:

- **Patch (`x.y.Z`)** for non-breaking adds (new tool, new format, new source backend, fix). Default.
- **Minor (`x.Y.0`)** only for breaking changes to existing tool surface.
- **Major (`X.0.0`)** reserved for v1.0 production-readiness.

If your change introduces a breaking surface change, bump the minor version and document the migration in the commit body. Otherwise the maintainer cuts a patch tag from your merged commit.

## Testing discipline

Three layers, each with its own scope and speed budget:

| Layer        | Command           | Scope                                                                                                                                              | When to run                                                              |
|--------------|-------------------|----------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------|
| Unit         | `make test`       | `go test -race ./...` against every package                                                                                                        | Every change                                                             |
| E2E (stub)   | `make e2e`        | Spawns the built binary, drives MCP over stdio against the in-tree Go stub server (`test/e2e/stub-server`)                                         | Every change                                                             |
| Integration  | `make integration`| Multi-instance soak against real upstream MCP servers (`memory`, `sequentialthinking`, `filesystem`) — full catalog UX + proxy spawn + aggregation | Touching `internal/sources/`, `internal/catalog/`, or release infra      |

**Rules:**

- **Every new tool, format, backend, or CLI subcommand ships with unit tests.**
- **Every new MCP-visible behavior ships with at least one e2e assertion** under `test/e2e/run.sh`.
- `make test && make e2e` must pass before opening a PR. Both are fast (<10 s combined) and have zero network dependencies.
- `make integration` is the slow path — it pulls npm packages on first run and depends on the npm registry. Not required for most PRs; CI runs it nightly. Run locally only when your change touches source aggregation.
- Want CI to run integration on your PR? Apply the **`integration`** label.

The CI matrix runs unit + e2e on Linux + macOS. If a test relies on a binary the runner doesn't have (ripgrep, pandoc, …), install it in the workflow rather than skipping the test.

## Adding a new core tool

1. Identify the upstream engine — wrap an existing best-in-class implementation rather than reinventing.
2. Implement under `internal/tools/core/<tool>.go` using the shared polish layer (`engines.go`, `atomic.go`).
3. Add `RegisterFoo(s)` and wire it in `internal/server/server.go` behind `cfg.IsEnabled("Foo")`.
4. Add the tool to `KnownCoreTools` in `internal/config/config.go` and append a descriptor to `CoreToolDocs()` in `internal/tools/core/toolsearch.go`.
5. Tests: `internal/tools/core/<tool>_test.go` + e2e assertions in `test/e2e/run.sh`.
6. Bump version (patch by default); commit message starts `feat(tools): add Foo …`.

## Adding a new source to the catalog

1. Add the entry to `internal/catalog/builtin.toml`. Required fields: `description`, `runtime`, `package`, `required_env` (if any), `auth_hint`, `homepage`, `maintained`.
2. Add a regression test asserting the entry parses and produces the right argv.
3. `feat(catalog): add <name>` commit subject.

## Reporting bugs / requesting features

- Bug → file an issue with the `bug` template. Include `clawtool version`, OS, the exact MCP request that misbehaved, and the response body.
- Feature → `enhancement` template.
- Source request → `source-request` template. Catalog additions are usually trivial; we'll fast-track.

## Security

Never file a vulnerability via a public issue. See [SECURITY.md](SECURITY.md).

## License

By contributing, you agree your work is released under the [MIT License](LICENSE) — same terms as the rest of clawtool.
