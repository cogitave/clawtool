// Package version — release pipeline regression tests.
//
// These tests guard the three regressions that broke the v0.9.2 →
// v0.20.x release stretch:
//
//  1. GoReleaser archive name_template emitted clawtool_<v>_linux_x86_64
//     while creativeprojects/go-selfupdate (used by `clawtool upgrade`)
//     looks for the native GOARCH (amd64). Result: every `upgrade`
//     call silently 404'd through DetectLatest and printed the
//     "no release found, fall back to install.sh" hint.
//
//  2. install.sh's ARCH detection mapped x86_64|amd64 → x86_64,
//     mirroring the broken GoReleaser convention. The two had to
//     agree, but they had to agree on amd64.
//
//  3. BODY.md (git-cliff scratch file consumed by GoReleaser via
//     --release-notes) wasn't in .gitignore. GoReleaser's "git is
//     in a dirty state" pre-flight aborted the release.
//
// Plus a trip-wire for the Release Please workflow being
// re-introduced without justification — it failed every run since
// v0.9.2 (GitHub GraphQL pagination bug on linear history) and we
// removed it deliberately. If a future commit re-adds the
// release-please.yml or its manifest, this test fires so the
// reintroducer knows what they're walking back into.
package version

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot walks up from this file to the directory containing go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked to filesystem root without finding go.mod")
		}
		dir = parent
	}
}

// TestReleasePipeline_GoReleaserNamingIsSelfUpdateCompatible asserts
// the .goreleaser.yaml archive name_template uses {{ .Arch }} verbatim
// (so amd64 stays amd64, matching go-selfupdate's matcher) and does
// NOT remap amd64 → x86_64 the way it used to.
func TestReleasePipeline_GoReleaserNamingIsSelfUpdateCompatible(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	src := string(body)

	if !strings.Contains(src, "{{- .Arch }}") && !strings.Contains(src, "{{ .Arch }}") {
		t.Error(".goreleaser.yaml: archive name_template should use {{ .Arch }} verbatim — it's how go-selfupdate matches the asset")
	}
	// The old broken template wrapped `if eq .Arch "amd64" }}x86_64`
	// to alias the architecture. That's the bug. Refuse to ship it
	// again.
	if strings.Contains(src, `}}x86_64`) {
		t.Error(".goreleaser.yaml still rewrites amd64 → x86_64 — clawtool upgrade will 404 on DetectLatest")
	}
}

// TestReleasePipeline_InstallShArchAgreesWithGoReleaser asserts
// install.sh's ARCH detection maps x86_64 → amd64 (matching the
// .goreleaser.yaml archive names) and not the inverse.
func TestReleasePipeline_InstallShArchAgreesWithGoReleaser(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	src := string(body)

	// The right line: x86_64|amd64) ARCH=amd64 ;;
	// The wrong line: x86_64|amd64) ARCH=x86_64 ;;
	if !strings.Contains(src, "x86_64|amd64) ARCH=amd64") {
		t.Error("install.sh: ARCH=amd64 expected for x86_64|amd64 case (must match .goreleaser.yaml asset names)")
	}
	if strings.Contains(src, "x86_64|amd64) ARCH=x86_64") {
		t.Error("install.sh: still maps to ARCH=x86_64 — no GoReleaser asset matches that any more")
	}
}

// TestReleasePipeline_BodyMdIsGitignored asserts BODY.md is in
// .gitignore. release.yml's git-cliff step writes BODY.md as a
// scratch file consumed by GoReleaser's --release-notes flag; if
// the file isn't gitignored, GoReleaser's "git clean" pre-flight
// fails on the untracked file and the release aborts.
func TestReleasePipeline_BodyMdIsGitignored(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	src := string(body)
	patterns := []string{"BODY.md", "/BODY.md"}
	hit := false
	for _, p := range patterns {
		if strings.Contains(src, p) {
			hit = true
			break
		}
	}
	if !hit {
		t.Error(".gitignore is missing BODY.md — GoReleaser's git-clean pre-flight will trip on git-cliff's scratch output")
	}
}

// TestReleasePipeline_NoReleasePleaseLeftovers asserts the Release
// Please artefacts stay deleted. They were noisy (failed every run
// on linear-history GraphQL pagination bug) and we removed them
// deliberately in v0.20.2. Re-adding them without first fixing
// the underlying cause would re-noisy the workflow tab.
//
// If you genuinely want Release Please back, delete this test
// in the same commit and explain in the message what changed —
// either GitHub fixed the bug or you switched to a merge-commit
// workflow that doesn't trigger it.
func TestReleasePipeline_NoReleasePleaseLeftovers(t *testing.T) {
	root := repoRoot(t)
	leftovers := []string{
		".github/workflows/release-please.yml",
		".release-please-manifest.json",
		"release-please-config.json",
	}
	var found []string
	for _, p := range leftovers {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			found = append(found, p)
		}
	}
	if len(found) > 0 {
		t.Errorf(
			"Release Please artefacts re-appeared: %v\n"+
				"They were removed in v0.20.2 because the action failed every "+
				"run on a GitHub GraphQL pagination bug (no merge commits on "+
				"linear-history main). If you're re-introducing them, drop this "+
				"test in the same commit and document what changed.",
			found)
	}
}

// TestReleasePipeline_VersionStringsInSync asserts the four files
// that carry a clawtool version string all agree. A drift here
// means the marketplace version, plugin manifest, binary version,
// and any auto-emitted CHANGELOG won't match — confusing for users
// who run `clawtool version` after a marketplace install.
//
// Files checked:
//   - internal/version/version.go (Version const)
//   - .claude-plugin/plugin.json (top-level "version")
//   - .claude-plugin/marketplace.json (metadata.version + plugins[0].version)
func TestReleasePipeline_VersionStringsInSync(t *testing.T) {
	root := repoRoot(t)

	binVer := Version
	if binVer == "" {
		t.Fatal("internal/version/version.go: Version is empty")
	}

	plugin, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	if !strings.Contains(string(plugin), `"version": "`+binVer+`"`) {
		t.Errorf(".claude-plugin/plugin.json: top-level version doesn't match binary version %q", binVer)
	}

	marketplace, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	body := string(marketplace)
	// Both metadata.version and plugins[0].version must contain binVer.
	count := strings.Count(body, `"version": "`+binVer+`"`)
	if count < 2 {
		t.Errorf(".claude-plugin/marketplace.json: expected 2 occurrences of %q (metadata + plugins[0]), got %d",
			binVer, count)
	}
}

// TestReleasePipeline_VersionsAreCodegenSynced is the architectural
// follow-up to VersionStringsInSync: instead of just *checking* the
// three files agree, it runs the same SyncPluginJSON /
// SyncMarketplaceJSON helpers cmd/version-sync uses against the
// canonical Version variable and asserts the output is byte-identical
// to the live manifests.
//
// What this catches that VersionStringsInSync doesn't:
//
//   - A manifest version field that *contains* the right substring
//     but has trailing garbage (e.g. accidentally adding "+dev").
//   - A manifest with the right version pinned but other fields
//     mangled by a half-applied search-and-replace.
//   - A manifest that drifted in formatting (extra whitespace, lost
//     trailing newline) — Claude Code's marketplace JSON parser
//     tolerates it but reviewers shouldn't have to.
//
// The fix when this test fails is always: run `make sync-versions`
// (or `go generate ./internal/version/...`) and commit the diff.
// The diff is guaranteed to be the version line(s) only — the
// helpers preserve every other byte verbatim.
func TestReleasePipeline_VersionsAreCodegenSynced(t *testing.T) {
	root := repoRoot(t)

	for _, tc := range []struct {
		name string
		path string
		fn   func([]byte, string) ([]byte, error)
	}{
		{".claude-plugin/plugin.json", filepath.Join(root, ".claude-plugin", "plugin.json"), SyncPluginJSON},
		{".claude-plugin/marketplace.json", filepath.Join(root, ".claude-plugin", "marketplace.json"), SyncMarketplaceJSON},
	} {
		live, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.name, err)
		}
		want, err := tc.fn(live, Version)
		if err != nil {
			t.Fatalf("sync %s: %v (the helper rejected the live manifest — likely a structural drift, see manifestsync.go)", tc.name, err)
		}
		if string(live) != string(want) {
			t.Errorf("%s is out of sync with internal/version.Version=%s\n"+
				"  run `make sync-versions` (or `go generate ./internal/version/...`) and commit.",
				tc.name, Version)
		}
	}
}
