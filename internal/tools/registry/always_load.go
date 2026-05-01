// Package registry — always-load annotation + deterministic sort
// for tools/list responses.
//
// Two MCP-spec-compliance hooks live here:
//
//  1. SortToolsByName — emits each tool in name-sorted order at
//     the MCP boundary. Post-SEP-2133 the spec calls out
//     deterministic order so prompt-cache hits and client-side
//     caching stay stable across runs. mcp-go's handleListTools
//     already sorts in the base case, but session-tool merges,
//     filters, and other AfterListTools hooks can disturb the
//     order; pinning the sort here as the LAST hook guarantees
//     the wire output stays deterministic regardless.
//
//  2. AnnotateAlwaysLoad — injects
//     `_meta["anthropic/alwaysLoad"]: true` on the eight hot
//     tools (Bash / Read / Edit / Glob / Grep / WebFetch /
//     WebSearch / ToolSearch). Anthropic's "Code execution with
//     MCP" engineering recipe lets Claude Code respect this hint
//     to keep specific tools eager while deferring the long
//     tail. Tools without AlwaysLoad=true are left untouched —
//     the resulting JSON simply lacks the annotation, preserving
//     backward compatibility for clients that don't read the
//     namespace.
//
// Both functions mutate the result in place; they're wired into
// the AfterListTools hook chain in internal/server/server.go.
package registry

import (
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
)

// alwaysLoadMetaKey is the canonical key under each tool's
// `_meta`. Anthropic's recipe namespaces the field under the
// `anthropic/` prefix; clawtool follows the same convention so a
// host that already reads Anthropic's flag picks ours up
// transparently.
const alwaysLoadMetaKey = "anthropic/alwaysLoad"

// SortToolsByName re-orders `result.Tools` ascending by Name.
// Safe to call with a nil result. Stable sort so ties (which
// shouldn't occur — names are unique within a manifest) preserve
// input order.
func SortToolsByName(result *mcp.ListToolsResult) {
	if result == nil {
		return
	}
	sort.SliceStable(result.Tools, func(i, j int) bool {
		return result.Tools[i].Name < result.Tools[j].Name
	})
}

// AnnotateAlwaysLoad walks `result.Tools` and, for any tool
// whose name is in `alwaysLoad`, attaches
// `_meta["anthropic/alwaysLoad"] = true`. Tools not in the set
// are left untouched.
//
// Pre-existing `_meta` content is preserved: if a tool was
// already shipped with `_meta.foo = bar` (or
// `_meta.clawtool.usage_hint = "..."` from the usage-hint hook),
// this function adds `_meta["anthropic/alwaysLoad"]` alongside
// without clobbering.
//
// Safe to call with nil/empty inputs: a nil set or nil result is
// a no-op.
func AnnotateAlwaysLoad(result *mcp.ListToolsResult, alwaysLoad map[string]struct{}) {
	if result == nil || len(alwaysLoad) == 0 {
		return
	}
	for i := range result.Tools {
		tool := &result.Tools[i]
		if _, hot := alwaysLoad[tool.Name]; !hot {
			continue
		}
		if tool.Meta == nil {
			tool.Meta = &mcp.Meta{}
		}
		if tool.Meta.AdditionalFields == nil {
			tool.Meta.AdditionalFields = map[string]any{}
		}
		tool.Meta.AdditionalFields[alwaysLoadMetaKey] = true
	}
}
