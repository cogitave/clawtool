// Package cli — structured summary of an `init` run.
//
// InitSummary is the machine-parseable counterpart to the human-
// readable lines `clawtool init` prints. The chat-driven onboard
// flow (a parallel branch) consumes either the `--summary-json`
// stdout payload or the in-process struct directly to decide what
// to surface to the operator and what to do next.
//
// The shape is intentionally narrow and append-only: chat-onboard
// can take a hard dependency on these field names. Adding new
// fields is fine; renaming or removing them is a breaking change.
package cli

import (
	"encoding/json"
	"io"
)

// RecipeApplyStatus enumerates the terminal states of a single
// recipe in an init run. Stable string values — chat-onboard reads
// them verbatim.
type RecipeApplyStatus string

const (
	// RecipeStatusApplied: recipe wrote artifacts this run.
	RecipeStatusApplied RecipeApplyStatus = "applied"
	// RecipeStatusAlreadyPresent: Detect reported non-Absent
	// before Apply ran. Idempotent skip — nothing was written.
	RecipeStatusAlreadyPresent RecipeApplyStatus = "already-present"
	// RecipeStatusSkipped: recipe was deselected, deferred (e.g.
	// needs required options), or the prompter voted skip.
	RecipeStatusSkipped RecipeApplyStatus = "skipped"
	// RecipeStatusFailed: Apply returned a non-skip error.
	RecipeStatusFailed RecipeApplyStatus = "failed"
)

// RecipeApply is one row of the apply loop. Status is mandatory;
// Error is set only when Status == RecipeStatusFailed.
type RecipeApply struct {
	Name     string            `json:"name"`
	Category string            `json:"category"`
	Status   RecipeApplyStatus `json:"status"`
	Error    string            `json:"error,omitempty"`
}

// RecipeSkip captures why a recipe never reached Apply. Reasons are
// short kebab-case tokens so chat-onboard can branch on them:
//
//   - "operator-deselected"     — user unchecked the row in the wizard
//   - "missing-required-option" — recipe needs holder/owners/etc.
//   - "not-core"                — `--all` filter excluded the row
//   - "stability-not-stable"    — non-interactive defaults filter
type RecipeSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// InitSummary is the structured tail of `clawtool init`. Designed
// for two audiences:
//
//   - Operators on a TTY get the human-readable rendering (the
//     pre-existing wizard output, unchanged by default).
//   - LLM / chat-onboard sessions get this struct serialised as
//     compact JSON via `--summary-json`, or in-process via
//     ChatRender() when re-used from the parallel chat branch.
//
// Field semantics:
//
//   - AppliedRecipes: every recipe the apply loop touched, including
//     idempotent skips (status=already-present) and failures
//     (status=failed). One entry per recipe considered.
//   - SkippedRecipes: recipes that never reached Apply because the
//     pre-filter dropped them (deselected, needs options, not Core).
//   - PendingActions: human-curated next-step suggestions built
//     from the registry's known follow-ups (e.g. "install gh CLI").
//   - Generated: files written this run, keyed by absolute path,
//     value is the managed-by marker ("clawtool" / "external").
//   - NextSteps: ordered, prompt-friendly bullets for an LLM to
//     read aloud. Distinct from PendingActions: NextSteps is the
//     curated short-list (≤5), PendingActions is the full registry.
type InitSummary struct {
	AppliedRecipes []RecipeApply     `json:"applied_recipes"`
	SkippedRecipes []RecipeSkip      `json:"skipped_recipes"`
	PendingActions []string          `json:"pending_actions"`
	Generated      map[string]string `json:"generated"`
	NextSteps      []string          `json:"next_steps"`
}

// AppliedCount returns the number of rows whose terminal status is
// RecipeStatusApplied. Used by ChatRender and by the JSON consumer
// to decide whether anything actually changed.
func (s InitSummary) AppliedCount() int {
	n := 0
	for _, r := range s.AppliedRecipes {
		if r.Status == RecipeStatusApplied {
			n++
		}
	}
	return n
}

// AlreadyPresentCount returns the count of idempotent skips.
func (s InitSummary) AlreadyPresentCount() int {
	n := 0
	for _, r := range s.AppliedRecipes {
		if r.Status == RecipeStatusAlreadyPresent {
			n++
		}
	}
	return n
}

// FailedCount returns the count of recipes whose Apply errored.
func (s InitSummary) FailedCount() int {
	n := 0
	for _, r := range s.AppliedRecipes {
		if r.Status == RecipeStatusFailed {
			n++
		}
	}
	return n
}

// AppliedNames returns recipe names in apply order, filtered to
// status=applied. Used by ChatRender for the "✓ N recipes applied:
// foo, bar" line.
func (s InitSummary) AppliedNames() []string {
	out := make([]string, 0, len(s.AppliedRecipes))
	for _, r := range s.AppliedRecipes {
		if r.Status == RecipeStatusApplied {
			out = append(out, r.Name)
		}
	}
	return out
}

// WriteJSON emits the summary as compact JSON terminated by a
// newline. Used by the `--summary-json` flag in runInitAll. Errors
// are returned to the caller so the dispatcher can downgrade the
// exit code if the encoder fails (it shouldn't — InitSummary has no
// non-marshallable types).
func (s InitSummary) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(s)
}
