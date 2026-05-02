// internal/version/sync.go — go:generate hook for the
// version-sync codegen.
//
// Running `go generate ./internal/version/...` (or the
// `make sync-versions` Makefile alias) rewrites the two
// .claude-plugin manifests from the canonical Version variable in
// version.go. Operators bumping the const should run the
// generator + commit the regenerated manifests in the same
// changeset; the TestReleasePipeline_VersionsAreCodegenSynced
// test fails CI if they drift.
//
// The generator is implemented as a separate main package
// (cmd/version-sync) rather than a //go:build-tagged tool inside
// this package because (1) it has its own dependency on os/exec
// and filepath traversal that would bloat the version package's
// import surface, and (2) `go run` against a sibling cmd/ keeps
// the generator runnable from a tarball without `go install`-ing
// anything globally.
package version

//go:generate go run github.com/cogitave/clawtool/cmd/version-sync
