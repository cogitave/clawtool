// Package checkpoint — docsync rule type. Surfaces the operator
// invariant: "when a Go source file lands a real change, its
// sibling Markdown doc (same basename, .md extension) MUST land
// in the same commit." Per ADR-022 §Resolved (2026-05-02), this
// rule type ships as a 3-mode severity gradient (off / warn /
// block) wired through the existing `rules.Severity` enum — no
// parallel enum, the verbatim ladder declared at
// internal/rules/types.go:31-43.
//
// Why a separate file (not inside commit.go): docsync is a
// PREDICATE, not a commit primitive. commit.go owns "stage,
// validate message, run git commit"; docsync.go owns "given a
// changed-paths list, find the Go files whose sibling .md
// doc was NOT also touched." Keeping the two surfaces split
// means a future caller (e.g. the post_edit dispatch site, a
// pre_send `clawtool send` rule) can call CheckDocsync without
// dragging in the whole commit pipeline.
//
// The check is intentionally PURE relative to its inputs + the
// filesystem state at call time:
//
//   - Inputs: cwd (repo root), changedPaths (forward-slash,
//     relative to cwd — same shape rules.Context.ChangedPaths
//     uses, so callers can pass it through unchanged).
//   - Output: a slice of violation paths (the .go files that
//     changed AND have a sibling .md on disk AND that .md is
//     not in changedPaths).
//   - Side effects: stat() calls under cwd to test sibling
//     existence; nothing else.
//
// The rules engine (internal/rules/eval.go) MUST NOT do this
// stat work itself — eval is documented as pure. Callers
// (runCommit, runRulesCheck) precompute violations with
// CheckDocsync and feed them to rules.Context.DocsyncViolations
// before calling rules.Evaluate. The new `docsync_violation()`
// predicate then reads that field. This keeps the eval / I/O
// boundary the same shape it was before.

package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
)

// CheckDocsync returns the subset of changedPaths that are Go
// source files whose sibling Markdown doc (same basename, `.md`
// extension, same directory) exists on disk under cwd but is
// NOT itself in changedPaths.
//
// Rules of the predicate:
//   - Only `*.go` files are inspected. `_test.go` files are
//     EXCLUDED — tests don't have a documentation contract, and
//     including them would fire on every test edit.
//   - Generated files (`*.pb.go`, `*_gen.go`, `zz_generated_*.go`,
//     anything matching the well-known codegen suffixes) are
//     EXCLUDED — they're regenerated, not hand-edited, and
//     have no sibling doc.
//   - The sibling doc is the file at <dir>/<base>.md where
//     <base> is the Go file's name minus the `.go` suffix.
//     We don't fall back to README.md or package-level docs —
//     the rule's tight-pairing semantics (one Go file ↔ one
//     .md) is what makes the violation actionable. Operator
//     can opt out via severity=off if a project doesn't follow
//     the convention.
//   - Sibling existence is checked with os.Stat(cwd + "/" +
//     siblingPath). When cwd is empty we use ".".
//   - Forward-slash paths are normalised to filepath separators
//     before stat-ing so the function works the same on Linux
//     and Windows; the returned violation paths preserve the
//     input's forward-slash shape (callers compare against
//     rules.Context.ChangedPaths, which is forward-slash).
//
// Returns nil (not an empty slice) when no violations are found —
// callers can use len(result) == 0 OR result == nil interchangeably.
func CheckDocsync(cwd string, changedPaths []string) []string {
	if len(changedPaths) == 0 {
		return nil
	}
	if cwd == "" {
		cwd = "."
	}

	// Build a set of changed paths for O(1) sibling-touched
	// lookup. Forward-slash form is the canonical key — that's
	// the shape rules.Context.ChangedPaths uses (see staging in
	// runCommit, where `git diff --name-only --cached` already
	// emits forward-slash on every platform).
	changed := make(map[string]struct{}, len(changedPaths))
	for _, p := range changedPaths {
		changed[p] = struct{}{}
	}

	var violations []string
	for _, p := range changedPaths {
		if !isCandidateGoFile(p) {
			continue
		}
		sibling := siblingDocPath(p)
		if sibling == "" {
			continue
		}
		// Skip when the sibling .md is in the same diff —
		// that's the success path (Go + doc co-landed).
		if _, ok := changed[sibling]; ok {
			continue
		}
		// Sibling must actually exist on disk for the rule to
		// fire. A missing sibling means the doc convention
		// hasn't been established for this file yet — silent
		// (operator opts in by creating the .md the first
		// time, then docsync enforces co-landing thereafter).
		abs := filepath.Join(cwd, filepath.FromSlash(sibling))
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		violations = append(violations, p)
	}
	return violations
}

// isCandidateGoFile reports whether path is a Go source file
// whose doc-sync invariant we enforce. Filters out tests and
// well-known codegen patterns.
func isCandidateGoFile(path string) bool {
	if !strings.HasSuffix(path, ".go") {
		return false
	}
	if strings.HasSuffix(path, "_test.go") {
		return false
	}
	base := filepath.Base(path)
	// Codegen suffixes — buf/protobuf, sqlc, kubebuilder, mockgen,
	// stringer. Conservative list; missing patterns can be added.
	if strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_gen.go") ||
		strings.HasSuffix(base, "_generated.go") ||
		strings.HasPrefix(base, "zz_generated_") ||
		strings.HasPrefix(base, "zz_generated.") {
		return false
	}
	return true
}

// siblingDocPath returns the forward-slash path of the sibling
// Markdown doc for a Go file (`internal/foo/bar.go` → `internal/
// foo/bar.md`). Returns "" when the input doesn't end in `.go`
// (defence in depth — the caller already filtered).
func siblingDocPath(goPath string) string {
	if !strings.HasSuffix(goPath, ".go") {
		return ""
	}
	return strings.TrimSuffix(goPath, ".go") + ".md"
}
