package core

import (
	"strings"
	"testing"
)

// TestSendMessage_ToolsSubset_ValidatesNames covers the validation
// half of ADR-014 §Resolved (2026-05-02) Phase 4: an unknown tool
// name surfaces a directive error BEFORE dispatch reaches the
// supervisor, so the operator sees the typo here instead of a
// silent missing-tool on the upstream peer.
func TestSendMessage_ToolsSubset_ValidatesNames(t *testing.T) {
	// Pick one name we know is in the manifest as a sanity anchor
	// (Bash is gated-on always; if it disappears, the test is
	// already wrong about more than this feature).
	got, err := parseToolsSubsetArg([]any{"Bash"})
	if err != nil {
		t.Fatalf("known tool rejected: %v", err)
	}
	if len(got) != 1 || got[0] != "Bash" {
		t.Errorf("known tool round-trip: got %v want [Bash]", got)
	}

	// Unknown tool — must error with the bad name in the message
	// so the operator can see what they typo'd.
	_, err = parseToolsSubsetArg([]any{"Bash", "DefinitelyNotARealTool"})
	if err == nil {
		t.Fatal("unknown tool name should error")
	}
	if !strings.Contains(err.Error(), "DefinitelyNotARealTool") {
		t.Errorf("error %q must name the offending tool", err)
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error %q should indicate unknown / not-found", err)
	}

	// Empty string entries are rejected — silently dropping them
	// would let a malformed `[\"\"]` slip through as \"no subset\"
	// and the operator's intent would vanish without a signal.
	if _, err := parseToolsSubsetArg([]any{""}); err == nil {
		t.Error("empty-string entry should error")
	}

	// Wrong shape (not an array of strings) is a hard error.
	if _, err := parseToolsSubsetArg("Bash"); err == nil {
		t.Error("non-array `tools` should error")
	}
	if _, err := parseToolsSubsetArg([]any{42}); err == nil {
		t.Error("non-string element should error")
	}
}

// TestSendMessage_ToolsSubset_EmptyDefaultsToAllTools is the
// back-compat lock for ADR-014 Phase 4: when the caller supplies no
// `tools` arg (or an empty array), parseToolsSubsetArg returns
// (nil, nil) and the runSendMessage opts map is left without a
// `tools_subset` key — the supervisor / runner downstream see the
// pre-Phase-4 shape exactly.
func TestSendMessage_ToolsSubset_EmptyDefaultsToAllTools(t *testing.T) {
	// nil (key absent) → no subset, no error.
	got, err := parseToolsSubsetArg(nil)
	if err != nil {
		t.Errorf("nil tools should not error: %v", err)
	}
	if got != nil {
		t.Errorf("nil tools should yield nil subset, got %v", got)
	}

	// Empty array → no subset, no error.
	got, err = parseToolsSubsetArg([]any{})
	if err != nil {
		t.Errorf("empty tools array should not error: %v", err)
	}
	if got != nil {
		t.Errorf("empty tools array should yield nil subset, got %v", got)
	}

	// De-dup: same name twice collapses to one entry (operators
	// scripting the field shouldn't be punished for redundancy).
	got, err = parseToolsSubsetArg([]any{"Bash", "Bash"})
	if err != nil {
		t.Fatalf("dedup case errored: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("dedup: got %v want [Bash]", got)
	}
}
