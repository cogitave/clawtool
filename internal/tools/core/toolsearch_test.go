package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cogitave/clawtool/internal/search"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestToolSearch_DetailLevels exercises the three detail-level
// projections — `name`, `name_desc` (default), and `full` — and
// asserts each one's wire shape matches the contract documented
// in the tool description and the v0.22.108 research report:
//
//   - name      → [{"name": "...", "score": ...}]
//   - name_desc → [{"name": "...", "score": ..., "description": "...",
//     "type": "...", "instance": "..."}] (current default)
//   - full      → name_desc + "input_schema": {...}
//
// Using a real registered ToolSearch handler against a probe
// server so the full Handler closure (including the
// request.GetString fallback for the missing arg) is on the
// hook of regression tests.
func TestToolSearch_DetailLevels(t *testing.T) {
	// Build a tiny index + register ToolSearch + an extra
	// "TargetTool" so `full` has a real schema to attach.
	idx, err := search.Build([]search.Doc{
		{Name: "TargetTool", Description: "test target", Type: "core", Keywords: []string{"target"}},
	})
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	s := server.NewMCPServer("toolsearch-test", "0")
	// Register a "TargetTool" so GetTool finds a schema for it
	// when detail_level=full.
	target := mcp.NewTool(
		"TargetTool",
		mcp.WithDescription("a target with schema"),
		mcp.WithString("foo", mcp.Required(), mcp.Description("required string")),
	)
	s.AddTool(target, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	RegisterToolSearch(s, idx)

	cases := []struct {
		name        string
		level       string
		wantHasDesc bool
		wantHasType bool
		wantSchema  bool
	}{
		{"name only", detailLevelName, false, false, false},
		{"name_desc default", detailLevelNameDesc, true, true, false},
		{"full schema", detailLevelFull, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name: "ToolSearch",
					Arguments: map[string]any{
						"query":        "target",
						"detail_level": tc.level,
					},
				},
			}
			tool := s.GetTool("ToolSearch")
			if tool == nil {
				t.Fatal("ToolSearch not registered")
			}
			result, err := tool.Handler(context.Background(), req)
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if result.IsError {
				t.Fatalf("handler returned IsError=true: %+v", result.Content)
			}

			// resultOf calls mcp.NewToolResultStructured(r, render);
			// the structured payload is on .StructuredContent. We
			// round-trip it through JSON so the test checks the
			// EXACT wire shape clients see, not the in-memory struct.
			if result.StructuredContent == nil {
				t.Fatalf("StructuredContent missing: %+v", result)
			}
			raw, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatalf("marshal structured: %v", err)
			}
			var decoded struct {
				Query       string           `json:"query"`
				Results     []map[string]any `json:"results"`
				DetailLevel string           `json:"detail_level"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal structured: %v\nraw: %s", err, raw)
			}
			if decoded.DetailLevel != tc.level {
				t.Errorf("detail_level echoed = %q, want %q", decoded.DetailLevel, tc.level)
			}
			if len(decoded.Results) == 0 {
				t.Fatalf("no results for query 'target'; raw=%s", raw)
			}
			hit := decoded.Results[0]

			if _, ok := hit["name"]; !ok {
				t.Errorf("name missing from hit: %v", hit)
			}
			if _, ok := hit["score"]; !ok {
				t.Errorf("score missing from hit: %v", hit)
			}
			if _, hasDesc := hit["description"]; hasDesc != tc.wantHasDesc {
				t.Errorf("description present=%v, want %v (level=%s)", hasDesc, tc.wantHasDesc, tc.level)
			}
			if _, hasType := hit["type"]; hasType != tc.wantHasType {
				t.Errorf("type present=%v, want %v (level=%s)", hasType, tc.wantHasType, tc.level)
			}
			if _, hasSchema := hit["input_schema"]; hasSchema != tc.wantSchema {
				t.Errorf("input_schema present=%v, want %v (level=%s)", hasSchema, tc.wantSchema, tc.level)
			}
		})
	}
}

// TestToolSearch_DefaultDetailLevel asserts callers that omit
// the `detail_level` arg get the legacy `name_desc` shape (no
// breaking change for existing harnesses). Mirrors the
// "default preserves current behavior" promise in the spec.
func TestToolSearch_DefaultDetailLevel(t *testing.T) {
	idx, err := search.Build([]search.Doc{
		{Name: "TargetTool", Description: "test target", Type: "core"},
	})
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	s := server.NewMCPServer("toolsearch-default-test", "0")
	RegisterToolSearch(s, idx)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "ToolSearch",
			Arguments: map[string]any{"query": "target"},
		},
	}
	tool := s.GetTool("ToolSearch")
	result, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if result.StructuredContent == nil {
		t.Fatalf("StructuredContent missing: %+v", result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	var decoded struct {
		DetailLevel string           `json:"detail_level"`
		Results     []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if decoded.DetailLevel != detailLevelNameDesc {
		t.Errorf("default detail_level = %q, want %q", decoded.DetailLevel, detailLevelNameDesc)
	}
	if len(decoded.Results) > 0 {
		hit := decoded.Results[0]
		// name_desc shape: name + score + description + type
		// MUST be present; input_schema MUST NOT.
		for _, key := range []string{"name", "score", "description", "type"} {
			if _, ok := hit[key]; !ok {
				t.Errorf("default shape missing %q: %v", key, hit)
			}
		}
		if _, ok := hit["input_schema"]; ok {
			t.Errorf("default shape unexpectedly carries input_schema: %v", hit)
		}
	}
}

// TestNormalizeDetailLevel covers the small enum-coercion
// helper directly so a regression in the canonicalisation
// (e.g. someone removes the unknown→default fallback) is caught
// without booting a full server.
func TestNormalizeDetailLevel(t *testing.T) {
	cases := map[string]string{
		"":              detailLevelNameDesc,
		"name":          detailLevelName,
		"name_desc":     detailLevelNameDesc,
		"full":          detailLevelFull,
		"unknown":       detailLevelNameDesc,
		"NAME":          detailLevelNameDesc, // case-sensitive on purpose; explicit enum
		"verbose":       detailLevelNameDesc,
		"name_and_desc": detailLevelNameDesc,
	}
	for in, want := range cases {
		if got := normalizeDetailLevel(in); got != want {
			t.Errorf("normalizeDetailLevel(%q) = %q, want %q", in, got, want)
		}
	}
}
