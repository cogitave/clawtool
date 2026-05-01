package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestToolsListIsNameSorted asserts SortToolsByName produces a
// strictly ascending sequence — every consecutive pair of names
// is in `<= ` order. The MCP spec post-SEP-2133 calls out
// deterministic order for prompt-cache hits and client-side
// caching; clawtool's AfterListTools hook pins this invariant
// regardless of upstream sort drift / session-tool merges /
// filter ordering quirks.
func TestToolsListIsNameSorted(t *testing.T) {
	// Deliberately scrambled input — Zebra → Alpha → Mango →
	// Bash → AAA — exercises the sort path on every adjacent
	// pair, including same-letter prefix (AAA vs Alpha) that
	// would trip naive prefix-only comparisons.
	result := &mcp.ListToolsResult{
		Tools: []mcp.Tool{
			mcp.NewTool("Zebra"),
			mcp.NewTool("Alpha"),
			mcp.NewTool("Mango"),
			mcp.NewTool("Bash"),
			mcp.NewTool("AAA"),
		},
	}
	SortToolsByName(result)

	for i := 1; i < len(result.Tools); i++ {
		prev := result.Tools[i-1].Name
		curr := result.Tools[i].Name
		if strings.Compare(prev, curr) > 0 {
			t.Errorf("not sorted: index %d (%q) > index %d (%q); full = %v",
				i-1, prev, i, curr, toolNames(result.Tools))
		}
	}

	// Nil input must be a no-op (defensive contract).
	SortToolsByName(nil)
}

// TestSortToolsByName_PreservesMetaAndSchema asserts the sort
// only re-orders the slice — per-tool fields (Description,
// InputSchema, _meta envelope) survive untouched. Catches a
// future regression where someone implements sort by rebuilding
// each Tool from a partial copy.
func TestSortToolsByName_PreservesMetaAndSchema(t *testing.T) {
	tagged := mcp.NewTool("Zebra", mcp.WithDescription("z-desc"))
	tagged.Meta = &mcp.Meta{AdditionalFields: map[string]any{"key": "value"}}

	result := &mcp.ListToolsResult{
		Tools: []mcp.Tool{tagged, mcp.NewTool("Alpha")},
	}
	SortToolsByName(result)

	// After sort, Zebra moves to index 1; Description + Meta
	// must still be intact.
	if got := result.Tools[1].Name; got != "Zebra" {
		t.Fatalf("unexpected post-sort order: %v", toolNames(result.Tools))
	}
	if got := result.Tools[1].Description; got != "z-desc" {
		t.Errorf("Description dropped during sort: %q", got)
	}
	if result.Tools[1].Meta == nil || result.Tools[1].Meta.AdditionalFields["key"] != "value" {
		t.Errorf("Meta dropped during sort: %+v", result.Tools[1].Meta)
	}
}

// TestAnnotateAlwaysLoad_InjectsMetaFlag asserts the function
// sets `_meta["anthropic/alwaysLoad"] = true` on every tool in
// the always-load set, and leaves every other tool's _meta
// untouched.
func TestAnnotateAlwaysLoad_InjectsMetaFlag(t *testing.T) {
	hot := mcp.NewTool("Bash")
	cold := mcp.NewTool("RecipeList")

	result := &mcp.ListToolsResult{Tools: []mcp.Tool{hot, cold}}
	set := map[string]struct{}{"Bash": {}}
	AnnotateAlwaysLoad(result, set)

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Bash: _meta["anthropic/alwaysLoad"] must equal true.
	bashMeta, ok := decoded.Tools[0]["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("Bash _meta missing: %v", decoded.Tools[0])
	}
	if got, _ := bashMeta["anthropic/alwaysLoad"].(bool); !got {
		t.Errorf("Bash anthropic/alwaysLoad = %v, want true", bashMeta["anthropic/alwaysLoad"])
	}

	// RecipeList: no entry in always-load set → no _meta.
	if _, present := decoded.Tools[1]["_meta"]; present {
		t.Errorf("RecipeList should not carry _meta when not in alwaysLoad set; got %v", decoded.Tools[1]["_meta"])
	}

	// Nil / empty inputs are no-ops.
	AnnotateAlwaysLoad(nil, set)
	AnnotateAlwaysLoad(result, nil)
	AnnotateAlwaysLoad(result, map[string]struct{}{})
}

// TestAnnotateAlwaysLoad_PreservesExistingMeta asserts the
// always-load flag adds onto a tool that already carries an
// upstream `_meta` envelope (e.g. clawtool/usage_hint from the
// usage-hint hook) rather than clobbering it.
func TestAnnotateAlwaysLoad_PreservesExistingMeta(t *testing.T) {
	tool := mcp.NewTool("Bash")
	tool.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		"clawtool": map[string]any{"usage_hint": "use this when foo"},
	}}
	result := &mcp.ListToolsResult{Tools: []mcp.Tool{tool}}
	AnnotateAlwaysLoad(result, map[string]struct{}{"Bash": {}})

	got := result.Tools[0].Meta.AdditionalFields
	if v, _ := got["anthropic/alwaysLoad"].(bool); !v {
		t.Errorf("alwaysLoad flag missing: %v", got)
	}
	ns, _ := got["clawtool"].(map[string]any)
	if ns == nil || ns["usage_hint"] != "use this when foo" {
		t.Errorf("pre-existing clawtool namespace clobbered: %v", got)
	}
}

func toolNames(tools []mcp.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}
