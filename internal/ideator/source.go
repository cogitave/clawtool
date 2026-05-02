// Package ideator — top of clawtool's three-layer self-direction
// stack:
//
//	Ideator  ──── what to work on (mines repo signals → Idea list)
//	Autopilot ─── when to work on it (proposed → pending → claimed)
//	Autonomous ── how to work on it (one goal → bounded iteration loop)
//
// The Ideator surveys cheap, repo-local signals that the operator
// would normally surface by hand — open ADR questions, TODO/FIXME
// comments, recent CI failures, doc/manifest drift, benchmark
// regressions — and emits Idea structs ranked by signal strength.
//
// Output is NEVER auto-claimed. RunAndQueue posts every selected
// Idea into the autopilot backlog at status=proposed; only an
// operator running `clawtool autopilot accept <id>` (or the MCP
// AutopilotAccept tool) can flip the row to pending. That gate is
// the safety boundary — it pins the agent to "I can suggest, the
// human approves, then I work" rather than the unbounded loop the
// 2025-11 autonomy literature warns against.
//
// Sources are pluggable via the IdeaSource interface. New signals
// (e.g. "dependabot alerts", "log analytics anomaly") drop in as a
// new file under internal/ideator/sources/ + a registration in the
// CLI wire-up (DefaultSources is assembled at the edge to avoid an
// import cycle between this package and its sources subpackage).
//
// Each source MUST be cheap-on-fail: missing tool, missing dir,
// network error → empty result + warning log, never a hard error
// that takes down the whole orchestrator.
package ideator

import "context"

// Idea is one feature candidate the Ideator emits.
type Idea struct {
	// Title is a short label for `ideate` print output and the
	// queue list. Empty → orchestrator derives one from the first
	// line of SuggestedPrompt.
	Title string
	// Summary is a 1–3 sentence rationale; the operator reads this
	// when deciding whether to Accept. Stored as Item.Note in the
	// queue.
	Summary string
	// SourceName names the IdeaSource that produced this Idea.
	// Optional in the source-side struct; orchestrator backfills
	// from IdeaSource.Name() when empty.
	SourceName string
	// Evidence carries the file:line / test name / ADR section
	// reference / bench-baseline diff — anything the operator can
	// grep to verify the suggestion isn't fabricated.
	Evidence string
	// SuggestedPriority is the base score (typically 0..10);
	// orchestrator may apply a small evidence-recency bump before
	// final ranking.
	SuggestedPriority int
	// SuggestedPrompt is the verbatim text the agent will receive
	// when it eventually claims the item. Written in the second
	// person ("Investigate the TODO at ..."). Empty → idea is
	// silently dropped by the orchestrator.
	SuggestedPrompt string
	// DedupeKey is a stable hash combining source + evidence;
	// re-running ideate over the same repo state must not multiply
	// the queue. Source authors hash once at construction; the
	// orchestrator AND queue.Propose both deduplicate by this key.
	DedupeKey string
}

// IdeaSource is the pluggable signal-mining contract. Implementations
// live under internal/ideator/sources/ and are wired into the
// orchestrator at the CLI / MCP edge (see internal/cli/ideate.go's
// defaultSources()). Two ground rules:
//
//  1. Scan MUST be cheap-on-fail. Missing dir / missing CLI tool /
//     timed-out network → empty slice + nil error. The orchestrator
//     surfaces this via the per-source warning log; one broken
//     source must never poison the rest.
//
//  2. Scan MUST respect ctx.Done(). The orchestrator runs all
//     sources in parallel under a single deadline; ignoring the ctx
//     blocks `clawtool ideate` past the operator's patience.
type IdeaSource interface {
	Name() string
	Scan(ctx context.Context, repoRoot string) ([]Idea, error)
}
