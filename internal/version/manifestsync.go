// internal/version/manifestsync.go — pure helpers that rewrite the
// .claude-plugin/plugin.json + .claude-plugin/marketplace.json
// manifests from a supplied semver string.
//
// Lives in package version (not cmd/version-sync) so the codegen
// invariant test (release_pipeline_test.go) can call the same
// helpers the binary uses without spawning a subprocess. The
// `cmd/version-sync` main is a thin wrapper that wires
// version.Version into these helpers and writes the result to disk.
//
// Implementation note: we deliberately use a regex-style
// substitution rather than json.Marshal-with-an-ordered-map. The
// Claude Code marketplace.json + plugin.json carry a specific
// field order that reviewers expect to see preserved (mcpServers
// last, etc.), and re-marshalling through Go's encoding/json
// would alphabetize map keys and lose the trailing newline /
// 2-space indent the existing files use. The regex approach
// guarantees byte-identical output except for the version line(s)
// — which is exactly what the `git diff --exit-code` CI gate
// expects.
package version

import (
	"fmt"
	"regexp"
)

// versionLineRE matches a `"version": "<semver>"` JSON line,
// agnostic to nesting depth. Tied to the JSON style our manifests
// use (double-quoted keys, single-space-after-colon) — anything
// outside that shape is left alone.
var versionLineRE = regexp.MustCompile(`"version":\s*"[^"]*"`)

// SyncPluginJSON rewrites plugin.json's single top-level "version"
// field to the supplied semver. plugin.json carries one version
// occurrence; we replace all matches but assert exactly one was
// found so a future schema change that adds a second "version"
// key fails loudly instead of silently rewriting too much.
func SyncPluginJSON(in []byte, ver string) ([]byte, error) {
	count := len(versionLineRE.FindAll(in, -1))
	if count != 1 {
		return nil, fmt.Errorf(`plugin.json: expected exactly 1 "version" field, found %d`, count)
	}
	return versionLineRE.ReplaceAll(in, []byte(`"version": "`+ver+`"`)), nil
}

// SyncMarketplaceJSON rewrites marketplace.json's two "version"
// fields (metadata.version + plugins[0].version) to the supplied
// semver. Asserts exactly two matches so a schema change that
// adds or removes a "version" occurrence trips this fence instead
// of silently mis-syncing.
func SyncMarketplaceJSON(in []byte, ver string) ([]byte, error) {
	count := len(versionLineRE.FindAll(in, -1))
	if count != 2 {
		return nil, fmt.Errorf(`marketplace.json: expected exactly 2 "version" fields, found %d`, count)
	}
	return versionLineRE.ReplaceAll(in, []byte(`"version": "`+ver+`"`)), nil
}
