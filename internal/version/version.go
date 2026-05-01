// Package version exposes the clawtool build version.
//
// `Info()` and `InfoJSON()` extend the surface for shell-pipeline
// consumers that want a structured snapshot (`clawtool version
// --json`) instead of the human banner. Single source of truth so
// telemetry / health-probe / version-gate scripts all read the
// same shape.
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
	"encoding/json"
	"regexp"
	"runtime"
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

// pseudoVersionRE matches Go module pseudo-versions of the form
// `v?0.0.0-YYYYMMDDHHMMSS-<12 hex>` that `runtime/debug.ReadBuildInfo`
// returns for `go install`-ed binaries built from a non-tagged
// commit (operator's 2026-04-30 PostHog audit: ~95% of inbound
// events carried `properties.version=0.0.0-20260501001315-a5ac21717194`,
// the unhelpful Go-cache pseudo-version). It is NOT a real semver
// for downstream consumers (telemetry filter, A2A card, /v1/health
// probe), so resolveVersion treats matches as if the build info
// had returned "(devel)" — fall through to the release-please
// constant. Operators who want the real tag should `go install`
// at a tagged ref or build via goreleaser (which sets the
// ldflags-baked Version).
var pseudoVersionRE = regexp.MustCompile(`^v?0\.0\.0-\d{14}-[a-f0-9]{12}$`)

// isPseudoVersion reports whether v looks like a Go module
// pseudo-version. Exposed (lowercase) for the resolveVersion
// fallthrough; tests assert the regex against synthesized
// inputs.
func isPseudoVersion(v string) bool { return pseudoVersionRE.MatchString(v) }

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
	// constant. Same treatment for Go pseudo-versions
	// (`v0.0.0-<ts>-<sha>`): they're not real semver and pollute
	// downstream consumers (telemetry, A2A card, /v1/health).
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" && !isPseudoVersion(v) {
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

// BuildInfo is the structured snapshot emitted by `clawtool version
// --json`. Single source of truth for any external probe (telemetry,
// /v1/health, monitoring scripts) that wants to reason about the
// running binary's identity. snake_case JSON tags follow the
// project's convention (mirrors the agents.Status / agentListEntry
// shape from earlier ticks).
type BuildInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"` // GOOS/GOARCH
	// Commit is the git revision baked in by `go build` via
	// debug.ReadBuildInfo. Empty when the binary was built without
	// VCS info (e.g. via the goreleaser pipeline that strips it).
	Commit string `json:"commit,omitempty"`
	// Modified reports whether the working tree was dirty when the
	// binary was built. Best-effort — only populated when the build
	// captured VCS info.
	Modified bool `json:"modified,omitempty"`
}

// Info returns a structured snapshot of the running binary.
// Wraps Resolved() so the version string respects the same
// ldflags / debug.BuildInfo / fallback hierarchy.
func Info() BuildInfo {
	bi := BuildInfo{
		Name:      Name,
		Version:   Resolved(),
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				bi.Commit = s.Value
			case "vcs.modified":
				bi.Modified = s.Value == "true"
			}
		}
	}
	return bi
}

// InfoJSON renders Info() as indented JSON, ready to print. The
// indented form is the same shape Marshal would emit; we use
// MarshalIndent because operators inspecting the output by eye
// expect a pretty document, and shell pipelines (`jq`) handle
// either form transparently.
func InfoJSON() (string, error) {
	body, err := json.MarshalIndent(Info(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}
