// usage_hint_coverage_test.go — load-bearing invariant: every
// registered ToolSpec carries a curated UsageHint string.
//
// Why a separate test file: the existing usage_hint_test.go
// covers the serializer round-trip (hint → _meta.clawtool.usage_hint).
// THIS test guards the data side — if a new tool is added to the
// manifest without a hint, the operator's "every tool the AI uses
// carries curated guidance" rule is silently broken. By driving
// the assertion off the live manifest (BuildManifest), nothing
// short of populating the field gets the test green.
//
// Why it lives in registry/ and imports core: registry/ already
// owns the ToolSpec type + the Manifest harness; core/ owns
// the canonical BuildManifest. Importing core from registry would
// create a cycle, so the test is registry_test package and pulls
// in core via the test-only graph.
package registry_test

import (
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/tools/core"
)

// TestEveryRegisteredToolHasUsageHint walks the canonical manifest
// emitted by core.BuildManifest and asserts every spec's UsageHint
// is non-empty AND meaningful (≥ 20 chars after trim — guards
// against placeholder values like " " or "TODO").
//
// The assertion drives off m.Specs() rather than a hand-curated
// list so a NEW tool added in a future commit must populate
// UsageHint to land — exactly what the operator asked for.
func TestEveryRegisteredToolHasUsageHint(t *testing.T) {
	m := core.BuildManifest()
	if m == nil {
		t.Fatal("BuildManifest returned nil")
	}
	specs := m.Specs()
	if len(specs) == 0 {
		t.Fatal("BuildManifest returned empty manifest")
	}

	const minHintLen = 20

	var missing []string
	var stub []string
	for _, s := range specs {
		hint := strings.TrimSpace(s.UsageHint)
		if hint == "" {
			missing = append(missing, s.Name)
			continue
		}
		if len(hint) < minHintLen {
			stub = append(stub, s.Name+" ("+hint+")")
		}
	}

	if len(missing) > 0 {
		t.Errorf("the following %d ToolSpec(s) have no UsageHint:\n  %s\n\nThe operator's invariant is that every tool the AI uses carries curated guidance. Add a UsageHint string to each spec in internal/tools/core/manifest.go.", len(missing), strings.Join(missing, "\n  "))
	}
	if len(stub) > 0 {
		t.Errorf("the following %d ToolSpec(s) have a stub UsageHint (< %d chars):\n  %s", len(stub), minHintLen, strings.Join(stub, "\n  "))
	}
}
