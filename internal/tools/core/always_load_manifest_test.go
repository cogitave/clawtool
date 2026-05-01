package core

import (
	"sort"
	"strings"
	"testing"
)

// TestHotToolsAlwaysLoad asserts the manifest carries
// `AlwaysLoad: true` on EXACTLY the canonical eight hot tools
// (Bash / Read / Edit / Glob / Grep / WebFetch / WebSearch /
// ToolSearch) — and on NO others. The "no others" half is
// load-bearing: the deferral optimisation only works when the
// flag stays opt-in tight; a runaway flip on every spec would
// defeat the whole point per Anthropic's "Code execution with
// MCP" engineering recipe.
func TestHotToolsAlwaysLoad(t *testing.T) {
	want := map[string]struct{}{
		"Bash":       {},
		"Read":       {},
		"Edit":       {},
		"Glob":       {},
		"Grep":       {},
		"WebFetch":   {},
		"WebSearch":  {},
		"ToolSearch": {},
	}

	got := BuildManifest().AlwaysLoadSet()

	// Each canonical hot tool MUST appear in the set.
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("hot tool %q missing AlwaysLoad=true on its ToolSpec", name)
		}
	}

	// No OTHER tool may carry the flag — opt-in tight.
	var stray []string
	for name := range got {
		if _, expected := want[name]; !expected {
			stray = append(stray, name)
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		t.Errorf("AlwaysLoad=true leaked onto non-hot tool(s): %s", strings.Join(stray, ", "))
	}

	// Sanity: count matches.
	if len(got) != len(want) {
		t.Errorf("AlwaysLoad set size = %d, want %d (hot=%v, got=%v)",
			len(got), len(want), keysOf(want), keysOf(got))
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
