// Package registry — usage-hint enrichment for tools/list responses.
//
// MCP's tools/list result carries each tool's Description, but
// Description answers WHAT the tool does. Calling agents (Claude
// Code, Codex, OpenCode) frequently need a HOW pointer too:
// "when do I pick this over the similar one", "what's a common
// mistake", "one concrete example". We curate that as a separate
// `UsageHint` field on the ToolSpec and surface it via the
// MCP-spec-defined `_meta` extension envelope:
//
//	{
//	  "name": "Glob",
//	  "description": "List files matching a glob pattern...",
//	  "_meta": {
//	    "clawtool": { "usage_hint": "Use when you need a file LIST..." }
//	  }
//	}
//
// `_meta` is the canonical extension surface in the MCP spec — strict
// clients ignore unknown sub-keys gracefully, so adding the
// `clawtool` namespace under it is non-breaking. (We considered
// hanging the hint off `annotations.clawtool` per ADR-008's
// extension-namespace pattern, but mcp-go's typed `ToolAnnotation`
// struct has no extension map and re-shaping its MarshalJSON
// would force a vendored fork. `_meta.AdditionalFields` is the
// supported path.)
//
// Wiring: server.go installs an `AddAfterListTools` hook that
// calls `EnrichListToolsResult` with the manifest's UsageHints
// map. The hook mutates the result in place before mcp-go
// serializes it.
package registry

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// usageHintMetaKey is the top-level namespace under `_meta`.
// Keep this in sync with any consumer that reads the hint
// (docs/feature-shipping-contract.md, future CLI surface).
const usageHintMetaKey = "clawtool"

// usageHintField is the leaf key under `_meta.clawtool`. We keep
// it lowercase + snake-case to match MCP's wire conventions for
// tool fields (descriptionHint, readOnlyHint, ...).
const usageHintField = "usage_hint"

// EnrichListToolsResult walks `result.Tools` and, for any tool
// whose name appears in `hints`, attaches the hint string at
// `_meta.clawtool.usage_hint`. Tools without a hint are left
// untouched — the resulting JSON simply lacks the annotation,
// preserving backward compatibility for callers that don't read
// the namespace.
//
// Pre-existing `_meta` content is preserved: if a tool was
// already shipped with `_meta.foo = bar`, this function adds
// `_meta.clawtool.usage_hint` alongside without clobbering.
//
// Safe to call with nil/empty inputs: a nil hints map or nil
// result is a no-op.
func EnrichListToolsResult(result *mcp.ListToolsResult, hints map[string]string) {
	if result == nil || len(hints) == 0 {
		return
	}
	for i := range result.Tools {
		tool := &result.Tools[i]
		hint, ok := hints[tool.Name]
		if !ok || hint == "" {
			continue
		}
		// Bring up the Meta envelope lazily so tools without a
		// hint marshal exactly as they did before this hook.
		if tool.Meta == nil {
			tool.Meta = &mcp.Meta{}
		}
		if tool.Meta.AdditionalFields == nil {
			tool.Meta.AdditionalFields = map[string]any{}
		}
		// Existing clawtool sub-namespace (set by some other
		// path) is preserved + extended; we only overwrite the
		// usage_hint leaf.
		ns, _ := tool.Meta.AdditionalFields[usageHintMetaKey].(map[string]any)
		if ns == nil {
			ns = map[string]any{}
		}
		ns[usageHintField] = hint
		tool.Meta.AdditionalFields[usageHintMetaKey] = ns
	}
}
