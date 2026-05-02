// Package sources — canonical default-source list.
//
// Both Ideator edges (`internal/cli/ideate.go` for the CLI verb +
// `internal/tools/core/ideator_tool.go` for the MCP `IdeateRun` tool)
// historically maintained their own `defaultIdeatorSources()`
// function. Each one was a literal slice of `New*()` constructor
// calls. When a new source landed, the operator had to update both
// — and forgetting to update one was a silent drift bug: the CLI
// would surface 12 source signals while MCP showed 8. Operator hit
// this at v0.22.150 (PR #12 triage cycle).
//
// DefaultSources is the single canonical list. Both edges call it.
// Adding a new source is one new entry here, not two.
//
// `manifest_drift` still depends on the package-global manifest
// provider that edges wire via `SetDefaultManifestProvider` in
// their init(). The constructor returns an empty struct; the
// provider lookup happens at Scan time. Edges remain responsible
// for wiring the provider before any IdeateRun call lands.
package sources

import "github.com/cogitave/clawtool/internal/ideator"

// DefaultSources returns every Ideator signal source enabled by
// default, in priority-conscious order. Edges (`internal/cli` for
// the `clawtool ideate` verb, `internal/tools/core` for the MCP
// `IdeateRun` tool) call this so they're always in sync. Tests can
// override the list by passing their own `[]ideator.IdeaSource`.
//
// Source order is NOT load-bearing — the orchestrator dedupes by
// `Idea.DedupeKey` and re-sorts by priority before returning. The
// order here is documentary: roughly highest-signal-first so the
// operator reading the list understands "what does Ideator notice
// most reliably?"
func DefaultSources() []ideator.IdeaSource {
	return []ideator.IdeaSource{
		// Signal-driven (priority 7+): externally-observable
		// failures the operator should act on.
		NewCIFailures(),
		NewVulnAdvisories(),
		// Backlog-driven (priority 4-6): work the operator has
		// either filed (ADR questions) or that accumulates from
		// the project's own motion (deps, deadcode, PR queue).
		NewADRQuestions(),
		NewADRDrafting(),
		NewDepsOutdated(),
		NewDeadcodeHits(),
		NewPRReviewPending(),
		// Code-shape-driven (priority 3-5): drift that doesn't
		// fail CI but rots toolspace.
		NewManifestDrift(),
		NewBenchRegression(),
		NewTODOs(),
		// Heuristic fallbacks (priority 1-2): keep the loop
		// productive when no signal-driven source has anything.
		NewStaleFiles(),
		NewStaleBranches(),
	}
}
