package registry

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestEnrichListToolsResult_SurfacesUsageHintInMeta is the
// invariant the operator asked for: a ToolSpec with UsageHint set
// surfaces it in the serialized `_meta.clawtool.usage_hint` field
// of the corresponding tools/list entry, while a tool without a
// hint stays untouched (no empty _meta envelope).
//
// We round-trip through the same JSON serializer mcp-go uses for
// tools/list, so this also catches accidental key-shape drift —
// e.g. `Description` getting redirected through Meta or a typo
// in usageHintMetaKey / usageHintField slipping past unit-coverage.
func TestEnrichListToolsResult_SurfacesUsageHintInMeta(t *testing.T) {
	withHint := mcp.NewTool("Glob")
	withoutHint := mcp.NewTool("Bash")
	result := &mcp.ListToolsResult{Tools: []mcp.Tool{withHint, withoutHint}}

	hints := map[string]string{
		"Glob": "Pick Glob when you need a file LIST; pick Grep for CONTENTS.",
	}
	EnrichListToolsResult(result, hints)

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Tool 0 (Glob): _meta.clawtool.usage_hint must equal the hint.
	meta, ok := decoded.Tools[0]["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("Glob _meta missing or wrong type: %v", decoded.Tools[0]["_meta"])
	}
	ns, ok := meta["clawtool"].(map[string]any)
	if !ok {
		t.Fatalf("Glob _meta.clawtool missing: %v", meta["clawtool"])
	}
	if got, _ := ns["usage_hint"].(string); got != hints["Glob"] {
		t.Errorf("Glob usage_hint = %q, want %q", got, hints["Glob"])
	}

	// Tool 1 (Bash): no hint registered → no _meta key at all.
	if _, present := decoded.Tools[1]["_meta"]; present {
		t.Errorf("Bash should not carry _meta when no hint set; got %v", decoded.Tools[1]["_meta"])
	}

	// Manifest.UsageHints contract: only specs with non-empty
	// UsageHint feed the enrichment path. A spec without a hint
	// MUST be absent so the enricher never writes an empty
	// usage_hint string.
	m := New()
	m.Append(ToolSpec{Name: "WithHint", Category: CategoryShell, UsageHint: "use this when foo"})
	m.Append(ToolSpec{Name: "NoHint", Category: CategoryShell})
	gotMap := m.UsageHints()
	if len(gotMap) != 1 || gotMap["WithHint"] != "use this when foo" {
		t.Errorf("UsageHints map drift: %v", gotMap)
	}
	if _, present := gotMap["NoHint"]; present {
		t.Error("NoHint should be absent from UsageHints map")
	}
}
