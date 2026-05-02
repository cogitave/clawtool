// Command version-sync regenerates the two .claude-plugin manifests
// from the canonical Go const at internal/version.Version, so the
// release-please-tracked release tag is the single source of truth
// for every consumer.
//
// Why this exists:
//
//	Pre-2026-05-01 the repo carried the same release tag in three
//	places — internal/version/version.go (the Go const release-please
//	updates), .claude-plugin/plugin.json, and .claude-plugin/marketplace.json
//	(the manifest the Claude Code marketplace reads, which carries
//	the version twice: metadata.version + plugins[0].version).
//	TestReleasePipeline_VersionStringsInSync could detect drift
//	after the fact, but never fix it. release-please regularly
//	bumped the const and forgot the manifests, leaving the
//	marketplace pinned to a stale build (operator caught the
//	v0.21.7 manifest mismatch on 2026-05-02).
//
// What this binary does:
//
//  1. Reads the Version variable from internal/version (compiled in
//     via the import — no string parsing of source files).
//  2. Rewrites .claude-plugin/plugin.json's top-level "version"
//     and .claude-plugin/marketplace.json's metadata.version +
//     plugins[0].version to that exact string.
//  3. Preserves every other byte of the manifests: field order,
//     formatting, trailing newline. The diff after a sync is
//     strictly the version line(s).
//
// Wiring:
//
//   - go:generate directive at internal/version/sync.go runs this
//     binary, so `go generate ./internal/version/...` regenerates
//     both manifests.
//   - `make sync-versions` is the operator-facing alias.
//   - TestReleasePipeline_VersionsAreCodegenSynced runs the helpers
//     against the live files and asserts the rewrite is a no-op —
//     codifies "if you bumped the const, run sync-versions before
//     commit" as a CI gate.
//
// Output is idempotent: running twice with no const change is a
// no-op. Exit codes:
//
//	0 — manifests written (or already in sync); --check passed.
//	1 — I/O / parse failure, OR --check found drift.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cogitave/clawtool/internal/version"
)

func main() {
	var (
		repoRoot = flag.String("root", "", "repo root (defaults to walking up from the binary's source path until a go.mod is found)")
		check    = flag.Bool("check", false, "exit non-zero if a write would change either manifest (CI gate)")
	)
	flag.Parse()

	root := *repoRoot
	if root == "" {
		var err error
		root, err = inferRepoRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "version-sync: %v\n", err)
			os.Exit(1)
		}
	}

	wantVer := version.Version
	if wantVer == "" {
		fmt.Fprintln(os.Stderr, "version-sync: internal/version.Version is empty")
		os.Exit(1)
	}

	pluginPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	marketplacePath := filepath.Join(root, ".claude-plugin", "marketplace.json")

	pluginBefore, err := os.ReadFile(pluginPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "version-sync: read plugin.json: %v\n", err)
		os.Exit(1)
	}
	marketplaceBefore, err := os.ReadFile(marketplacePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "version-sync: read marketplace.json: %v\n", err)
		os.Exit(1)
	}

	pluginAfter, err := version.SyncPluginJSON(pluginBefore, wantVer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "version-sync: %v\n", err)
		os.Exit(1)
	}
	marketplaceAfter, err := version.SyncMarketplaceJSON(marketplaceBefore, wantVer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "version-sync: %v\n", err)
		os.Exit(1)
	}

	pluginDirty := string(pluginBefore) != string(pluginAfter)
	marketplaceDirty := string(marketplaceBefore) != string(marketplaceAfter)

	if *check {
		if pluginDirty || marketplaceDirty {
			fmt.Fprintf(os.Stderr,
				"version-sync: manifests are out of sync with internal/version.Version=%s\n"+
					"  run `make sync-versions` (or `go generate ./internal/version/...`) and commit.\n",
				wantVer)
			os.Exit(1)
		}
		fmt.Printf("version-sync: manifests in sync at %s\n", wantVer)
		return
	}

	if pluginDirty {
		if err := os.WriteFile(pluginPath, pluginAfter, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "version-sync: write plugin.json: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("version-sync: rewrote %s -> %s\n", pluginPath, wantVer)
	}
	if marketplaceDirty {
		if err := os.WriteFile(marketplacePath, marketplaceAfter, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "version-sync: write marketplace.json: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("version-sync: rewrote %s -> %s\n", marketplacePath, wantVer)
	}
	if !pluginDirty && !marketplaceDirty {
		fmt.Printf("version-sync: manifests already in sync at %s\n", wantVer)
	}
}

// inferRepoRoot walks up from the directory containing main.go
// (encoded into the binary by runtime.Caller) until it finds a
// go.mod. Mirrors the repoRoot helper in
// internal/version/release_pipeline_test.go so the binary works
// whether invoked via `go run`, `go generate`, or a built binary
// placed somewhere on PATH (the latter can pass --root explicitly).
func inferRepoRoot() (string, error) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("walked to filesystem root without finding go.mod (cwd=%q); pass --root", dir)
		}
		dir = parent
	}
}
