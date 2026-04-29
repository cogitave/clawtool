// Package version exposes the clawtool build version.
//
// Three layers, picked in order:
//
//  1. ldflags override — `go build -ldflags='-X
//     github.com/cogitave/clawtool/internal/version.Version=v…'`.
//     goreleaser sets this on every release tarball, so installed
//     binaries always carry the exact tag.
//
//  2. runtime/debug.ReadBuildInfo — module-cached `go install`
//     binaries surface the tag here. Local `go build` from a
//     working tree returns "(devel)".
//
//  3. The release-please-tracked constant below — fallback for
//     dev workflows where neither (1) nor (2) yields a real
//     version.
//
// `Resolved()` is the single function every caller (overview,
// upgrade, claude-bootstrap, telemetry) must use to read the
// effective version. Reading `Version` directly (the constant)
// will diverge from what the binary actually is when goreleaser
// stamped a different value via ldflags.
package version

import (
	"runtime/debug"
	"sync"
)

// x-release-please-start-version
const Name = "clawtool"

// Version is the build-stamped semver string. Declared as `var`
// (not `const`) so goreleaser can override it via
// `-ldflags='-X github.com/cogitave/clawtool/internal/version.Version=…'`
// at link time. `-X` cannot patch constants; that's why this is a
// var even though it's effectively immutable at runtime.
var Version = "0.21.7" // x-release-please-version

// x-release-please-end

var (
	resolvedOnce sync.Once
	resolvedVal  string
)

// Resolved returns the authoritative installed-binary version.
// First-call computation is cached for the process lifetime — the
// binary's version doesn't change while it's running.
//
// Output strips any leading "v" so callers can pass it straight
// into Compare() without normalising at every call site.
//
// **Every external surface MUST use this** — telemetry events,
// hook payloads, /v1/health JSON, A2A card, doctor banner,
// orchestrator probe, MCP serverInfo. The literal `Version` var
// holds the pre-build fallback ("0.21.7") and reads of it
// outside this package are an anti-pattern: a goreleaser-baked
// binary at v0.22.34 emitting the const looks like v0.21.7 to
// every consumer (operator's PostHog filter, A2A peer, /v1/health
// probe — all silently wrong). The bug repeated across 9 sites
// before the operator caught it on 2026-04-29 ("12 hours, no
// telemetry events"). Don't repeat it — call Resolved().
func Resolved() string {
	resolvedOnce.Do(func() {
		resolvedVal = resolveVersion()
	})
	return resolvedVal
}

func resolveVersion() string {
	// Prefer ldflags-baked Version when it's a real version (not
	// the dev-fallback "0.21.7"). goreleaser always sets this,
	// so production binaries report the exact release tag.
	if Version != "" && Version != "0.21.7" {
		return strip(Version)
	}
	// Module-cached `go install` binaries put the tag in
	// debug.Main.Version. `go build` from a working tree returns
	// "(devel)" — we want to skip that and fall through to the
	// constant.
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" {
			return strip(v)
		}
	}
	return strip(Version)
}

func strip(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v[1:]
	}
	return v
}

// String is the formatted "clawtool X.Y.Z" banner the CLI prints.
func String() string {
	return Name + " " + Resolved()
}
