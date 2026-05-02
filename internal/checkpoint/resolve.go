// Package checkpoint — autosquash resolve.
//
// Resolve collapses every `wip!:` checkpoint commit between <base>
// and HEAD into the most recent non-`wip!:` commit on the branch,
// preserving its subject + body. The mechanism is `git rebase -i
// --autosquash`, the same machinery that recognises `fixup!` /
// `squash!` prefixes — but git doesn't natively understand `wip!`,
// so we run the rebase with a custom GIT_SEQUENCE_EDITOR that
// rewrites the todo list before git applies it.
//
// Algorithm:
//
//  1. List commits between <base>..HEAD in chronological order.
//  2. Walk the rebase todo list: any `pick <sha> wip!: …` line is
//     rewritten to `fixup <sha>` so git folds the diff into the
//     preceding pick without keeping the wip subject.
//  3. The first non-wip pick stays untouched and absorbs the
//     squashed diffs that follow it.
//
// Edge cases handled:
//   - Branch contains ONLY wip!: commits since base → return an
//     error; there's no real commit to fold into. The operator
//     should manually rename the first wip!: subject to a real
//     Conventional Commits form, then re-run.
//   - Branch contains NO wip!: commits → no-op; return nil
//     immediately without invoking git rebase (cheaper, and
//     avoids spurious "noop" rebase output).
//   - GIT_SEQUENCE_EDITOR script must be self-contained and not
//     depend on the operator's environment — we write a tiny
//     shell wrapper to a tempfile and pass its absolute path.
//
// Anti-pattern guard: Resolve does NOT push. The pre_push rule
// (wip-on-protected-branch) is the safety net for forgetting to
// resolve; Resolve itself is purely local-history surgery.
package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve runs `git rebase -i --autosquash <base>` with the
// sequence editor rewritten to fold every `wip!:` commit into the
// preceding non-wip commit.
//
// `base` is the upstream ref to rebase onto — typically
// `origin/main` or the branch's merge-base. The caller is
// responsible for choosing the right base; Resolve doesn't infer
// it (inferring would mean shelling out to merge-base, which
// makes the function impure and harder to test).
//
// Returns:
//   - nil on success (or no-op when no wip!: commits exist)
//   - error wrapping the underlying git failure
//   - error when every commit since base is `wip!:` (no real
//     subject to fold into)
func Resolve(ctx context.Context, base string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("checkpoint: resolve getwd: %w", err)
	}
	return ResolveAt(ctx, cwd, base)
}

// ResolveAt is Resolve with an explicit cwd — useful for tests
// that operate on a temporary repo and don't want to chdir the
// whole process.
func ResolveAt(ctx context.Context, cwd, base string) error {
	if !IsGitRepo(cwd) {
		return fmt.Errorf("checkpoint: resolve: not a git repository: %s", cwd)
	}
	if strings.TrimSpace(base) == "" {
		return errors.New("checkpoint: resolve: base ref is required")
	}

	subjects, err := commitSubjectsSince(cwd, base)
	if err != nil {
		return fmt.Errorf("checkpoint: resolve: list commits: %w", err)
	}
	wipCount, nonWipCount := 0, 0
	for _, s := range subjects {
		if strings.HasPrefix(s, WipPrefix) {
			wipCount++
		} else {
			nonWipCount++
		}
	}
	if wipCount == 0 {
		// No wip!: commits since base → no-op. Return nil so
		// callers can treat Resolve as idempotent.
		return nil
	}
	if nonWipCount == 0 {
		return fmt.Errorf(
			"checkpoint: resolve: every commit since %s is wip!: — "+
				"rename the first checkpoint to a real Conventional "+
				"Commits subject before resolving (e.g. `git commit "+
				"--amend` on the oldest wip)", base)
	}

	editor, cleanup, err := writeWipSequenceEditor()
	if err != nil {
		return fmt.Errorf("checkpoint: resolve: build sequence editor: %w", err)
	}
	defer cleanup()

	// Inherit env, then layer GIT_SEQUENCE_EDITOR + a non-
	// interactive GIT_EDITOR (the rebase shouldn't open the
	// commit message editor for the squashed result — fixup
	// keeps the preceding subject as-is).
	env := append(os.Environ(),
		"GIT_SEQUENCE_EDITOR="+editor,
		"GIT_EDITOR=true", // `true` is a no-op editor on POSIX
	)
	if _, err := runGitCtxEnv(ctx, cwd, env, "rebase", "-i", "--autosquash", base); err != nil {
		return fmt.Errorf("checkpoint: resolve: git rebase: %w", err)
	}
	return nil
}

// commitSubjectsSince returns the subject line of each commit in
// <base>..HEAD, oldest first. Empty slice when base equals HEAD.
func commitSubjectsSince(cwd, base string) ([]string, error) {
	out, err := runGit(cwd, "log", "--reverse", "--format=%s", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(string(out))
	if body == "" {
		return nil, nil
	}
	return strings.Split(body, "\n"), nil
}

// writeWipSequenceEditor materialises a tiny script that rewrites
// the rebase todo list: every `pick <sha> wip!: …` line becomes
// `fixup <sha>`. Returns the absolute path to the script and a
// cleanup func the caller defers.
//
// On POSIX we emit a /bin/sh script using sed; on Windows we emit
// a batch file using a PowerShell one-liner. The clawtool repo's
// CI runs on Linux + macOS so the POSIX path is the hot one; the
// Windows branch is a courtesy for operators on native Windows
// (most use WSL anyway).
func writeWipSequenceEditor() (string, func(), error) {
	dir, err := os.MkdirTemp("", "clawtool-checkpoint-resolve-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "rewrite.cmd")
		// PowerShell handles the regex more cleanly than cmd's
		// findstr/for. The replacement keeps the SHA but drops
		// the subject so git's todo parser falls back to the
		// commit's stored message (which is the wip!: line — but
		// fixup throws it away, so it doesn't matter).
		script := `@echo off
powershell -NoProfile -Command "(Get-Content -LiteralPath '%~1') ^| ForEach-Object { if ($_ -match '^pick ([0-9a-f]+) wip!:') { 'fixup ' + $matches[1] } else { $_ } } ^| Set-Content -LiteralPath '%~1'"
`
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			cleanup()
			return "", func() {}, err
		}
		return path, cleanup, nil
	}

	path := filepath.Join(dir, "rewrite.sh")
	// sed in-place: match `pick <hex> wip!:` (case-sensitive, the
	// prefix literal we control) and rewrite to `fixup <hex>`.
	// The .bak suffix is for BSD sed compatibility (macOS); we
	// remove it after the substitution lands.
	script := `#!/bin/sh
set -eu
todo="$1"
sed -e 's/^pick \([0-9a-f][0-9a-f]*\) wip!:.*/fixup \1/' "$todo" > "$todo.new"
mv "$todo.new" "$todo"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// runGitCtxEnv is runGitCtx + a custom env slice. Used only by
// Resolve so the GIT_SEQUENCE_EDITOR override stays scoped to the
// rebase call.
func runGitCtxEnv(ctx context.Context, cwd string, env []string, args ...string) ([]byte, error) {
	cmd := gitCommandWithEnv(ctx, cwd, env, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
