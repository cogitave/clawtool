// Package checkpoint — `wip!:` autocommit primitive.
//
// Autocommit is the operator's "save my work" escape hatch: stage
// the named files (or whatever's already staged) and commit with a
// `wip!:` Conventional Commits prefix that the autosquash flow in
// resolve.go later collapses into a real subject.
//
// Why `wip!:` and not plain `wip:`:
//   - The bang (!) marks a breaking-change-style attention flag in
//     Conventional Commits, which is repurposed here as "this is
//     not a real subject — squash me." `git rebase -i --autosquash`
//     doesn't recognise `wip!:` natively (it knows fixup!/squash!),
//     so resolve.go does the recognition itself by rewriting the
//     todo list before handing it to git.
//   - The trailing-bang form keeps the prefix structurally close to
//     the existing `feat!:` / `fix!:` shapes the operator's eye is
//     trained on, so `git log --oneline` reading stays uniform.
//
// Anti-pattern guard: Autocommit is intentionally NOT exposed as a
// background daemon or hook — it's an explicit operator/agent call.
// Auto-saving every Edit would race the rules engine's pre_commit
// gate and bury intentional commits under noise. The CLI surface
// (e.g. `clawtool checkpoint save`) layers on top in a follow-up;
// this commit ships the helper only, per the autodev scope decision.
package checkpoint

import (
	"context"
	"errors"
	"strings"
)

// WipPrefix is the literal subject prefix Autocommit prepends.
// Exported so resolve.go and the pre_push rule can reference one
// canonical string instead of duplicating the literal across the
// package.
const WipPrefix = "wip!:"

// Autocommit stages files (or uses the existing index when files is
// empty) and commits with a `wip!:` prefix prepended to msg. When
// msg already starts with `wip!:` (or `wip!: `), Autocommit does NOT
// double-prefix — the operator's intent is preserved verbatim.
//
// Validation: msg must be non-empty after trimming. The Conventional
// Commits validator is bypassed (the prefix would fail it anyway —
// `wip` is not in the allowed type set), and the Co-Authored-By
// hard-block stays on so AI-attribution still can't sneak in via a
// checkpoint commit.
//
// Files semantics:
//   - empty slice → don't stage anything new; commit the current
//     index. This is the "I already ran git add" path.
//   - non-empty   → run `git add -- <files>` first.
//
// Returns nil on success; the underlying `git commit` error
// otherwise (including "nothing to commit" when the index is clean
// and AutoStageAll wasn't used).
func Autocommit(ctx context.Context, files []string, msg string) error {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return errors.New("checkpoint: autocommit message is empty")
	}
	subject := PrependWipPrefix(trimmed)

	opts := CommitOptions{
		Message: subject,
		Files:   files,
		// `wip!:` is not Conventional — the validator would reject
		// it. Disabling RequireConventional is correct here; the
		// pre_push rule catches `wip!:` before it leaves the
		// branch, so the relaxation only affects local history.
		RequireConventional: false,
		// Coauthor hard-block stays on. Operator's policy doesn't
		// distinguish "real" commits from checkpoints — AI
		// attribution is forbidden in either.
		ForbidCoauthor: true,
		// AllowDirty true: the post-stage dirtiness guard is for
		// "real" commits where the operator wants to know they
		// missed staging something. A wip checkpoint is the
		// opposite of careful — it's a save-and-keep-going.
		AllowDirty: true,
	}
	// Run() trusts the caller to have validated; do that here so
	// the Co-Authored-By hard-block fires on wip!: commits too
	// (operator policy doesn't exempt checkpoints from the
	// no-AI-attribution rule).
	if err := ValidateMessage(opts.Message, opts); err != nil {
		return err
	}
	if _, err := Run(ctx, opts); err != nil {
		return err
	}
	// Reset the Guard counter — the operator just landed a
	// checkpoint, so the "uncheckpointed edits" budget resets.
	// Safe to call when Guard is disabled (no-op).
	Guard().OnCheckpoint()
	return nil
}

// PrependWipPrefix returns msg with `wip!: ` prepended unless msg
// already begins with the prefix (case-sensitive — Conventional
// Commits is case-sensitive on the type, and our pre_push rule
// matches the literal string, so we don't normalise case here).
//
// Exposed (capitalised) so internal/tools/core can reuse the same
// shaping logic when an MCP tool surface lands.
func PrependWipPrefix(msg string) string {
	if HasWipPrefix(msg) {
		return msg
	}
	return WipPrefix + " " + msg
}

// HasWipPrefix reports whether the first line of msg begins with
// the `wip!:` literal (with or without the trailing space). Used
// by PrependWipPrefix to avoid double-prefixing and by the
// pre_push rule predicate path to detect a checkpoint subject.
func HasWipPrefix(msg string) bool {
	first := firstLine(msg)
	return strings.HasPrefix(first, WipPrefix)
}
