// Package checkpoint — autosquash resolve.
//
// Resolve collapses every `wip!:` checkpoint commit between <base>
// and HEAD into the most recent non-`wip!:` commit on the branch,
// preserving its subject + body.
//
// Earlier revisions of this file shelled out to `git rebase -i
// --autosquash` with a custom GIT_SEQUENCE_EDITOR shell script
// that rewrote `pick <sha> wip!: …` lines to `fixup <sha>`. That
// approach was fragile across CI environments — the sequence
// editor was a /bin/sh + sed wrapper, and on at least one CI
// runner config the rebase silently no-op'd (exit 0, but the todo
// rewrite never landed; HEAD unchanged). Reproducing under
// macOS-latest required jumping through too many hoops, and the
// failure mode (silent success) is exactly the wrong shape for
// a primitive that mutates history.
//
// The new mechanism is pure Go + plumbing-level git commands —
// no shell script, no rebase, no interactive editor. Algorithm:
//
//  1. List <base>..HEAD as (sha, subject) pairs, oldest first.
//  2. Walk the list and group commits into "fold groups": one
//     non-wip commit followed by zero or more contiguous wip!:
//     commits. The non-wip commit's subject + body is preserved;
//     the wip commits' diffs are folded in via the LAST commit's
//     tree (so all checkpoint progress is retained).
//  3. Replay groups onto <base> using `git commit-tree`:
//     - parent = current synthesised HEAD (starts at <base>)
//     - tree   = the LAST commit in the group's tree (so the
//     final wip diff state wins, exactly like fixup)
//     - msg    = the FIRST (non-wip) commit's full message
//     (subject + body, preserving Co-Authored-By
//     trailers if present on the real commit)
//     - author / committer info preserved from the non-wip
//     commit so `git log` blame stays meaningful.
//  4. `git update-ref HEAD <last_synth_sha>` — atomically swing
//     the branch tip to the new history.
//  5. `git reset --hard HEAD` to bring the working tree + index
//     into sync with the squashed tip.
//
// Edge cases handled:
//   - Branch contains ONLY wip!: commits since base → return an
//     error; there's no real commit to fold into. The operator
//     should manually rename the first wip!: subject to a real
//     Conventional Commits form, then re-run.
//   - Branch contains NO wip!: commits → no-op; return nil
//     immediately without touching git state.
//   - Multiple non-wip commits with wips between them → each
//     real commit anchors its own fold group; the structure is
//     preserved (only wips collapse, real commits stay distinct).
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
	"strings"
)

// Resolve runs the autosquash mechanism on the current working
// directory. See ResolveAt for the full semantics.
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
//
// Returns:
//   - nil on success (or no-op when no wip!: commits exist)
//   - error wrapping the underlying git failure
//   - error when every commit since base is `wip!:` (no real
//     subject to fold into)
func ResolveAt(ctx context.Context, cwd, base string) error {
	if !IsGitRepo(cwd) {
		return fmt.Errorf("checkpoint: resolve: not a git repository: %s", cwd)
	}
	if strings.TrimSpace(base) == "" {
		return errors.New("checkpoint: resolve: base ref is required")
	}

	commits, err := commitsSince(ctx, cwd, base)
	if err != nil {
		return fmt.Errorf("checkpoint: resolve: list commits: %w", err)
	}
	if len(commits) == 0 {
		// Nothing between base and HEAD → no-op.
		return nil
	}

	wipCount, nonWipCount := 0, 0
	for _, c := range commits {
		if strings.HasPrefix(c.subject, WipPrefix) {
			wipCount++
		} else {
			nonWipCount++
		}
	}
	if wipCount == 0 {
		// No wip!: commits since base → no-op. Resolve is
		// idempotent.
		return nil
	}
	if nonWipCount == 0 {
		return fmt.Errorf(
			"checkpoint: resolve: every commit since %s is wip!: — "+
				"rename the first checkpoint to a real Conventional "+
				"Commits subject before resolving (e.g. `git commit "+
				"--amend` on the oldest wip)", base)
	}

	groups := groupForFold(commits)

	// Resolve <base> to a concrete sha so the parent chain
	// starts from a stable reference, not a symbolic name that
	// might be re-resolved mid-walk.
	baseSha, err := runGitCtxStr(ctx, cwd, "rev-parse", base)
	if err != nil {
		return fmt.Errorf("checkpoint: resolve: rev-parse base: %w", err)
	}

	parent := baseSha
	for _, g := range groups {
		newSha, err := replayGroup(ctx, cwd, parent, g)
		if err != nil {
			return fmt.Errorf("checkpoint: resolve: replay group %s: %w", g.anchor.sha[:7], err)
		}
		parent = newSha
	}

	// Atomically swing HEAD to the new tip.
	if _, err := runGitCtx(ctx, cwd, "update-ref", "HEAD", parent); err != nil {
		return fmt.Errorf("checkpoint: resolve: update-ref HEAD: %w", err)
	}
	// Sync the working tree + index to the new HEAD. The
	// previous wip commits already had the same tree as the
	// final group anchor, so this should be a no-op for the
	// working tree in the common path — but it ensures the
	// index doesn't drift.
	if _, err := runGitCtx(ctx, cwd, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("checkpoint: resolve: reset --hard: %w", err)
	}
	return nil
}

// commitMeta is one row from `git log --format=%H%x00%s` — sha
// plus subject. The full message + author info is fetched lazily
// per-group inside replayGroup; we only need the subject here for
// the wip!: prefix check.
type commitMeta struct {
	sha     string
	subject string
}

// foldGroup is "one anchor (non-wip) commit + N trailing wips".
// The anchor's commit message is preserved; the LAST commit's
// tree is used so every wip diff lands.
type foldGroup struct {
	anchor commitMeta   // the non-wip commit whose message survives
	tail   []commitMeta // contiguous wip!: commits to fold in (may be empty)
}

// last returns the commit whose tree should be used for the
// synthesised commit. When tail is non-empty, that's the final
// wip; otherwise it's the anchor itself (no folding needed).
func (g foldGroup) last() commitMeta {
	if len(g.tail) > 0 {
		return g.tail[len(g.tail)-1]
	}
	return g.anchor
}

// commitsSince lists <base>..HEAD oldest-first as (sha, subject)
// pairs. Uses NUL as the field separator so subjects containing
// arbitrary punctuation (including newlines via %s — which git
// already collapses to one line) parse cleanly.
func commitsSince(ctx context.Context, cwd, base string) ([]commitMeta, error) {
	out, err := runGitCtx(ctx, cwd, "log", "--reverse", "--format=%H%x00%s", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	body := strings.TrimRight(string(out), "\n")
	if body == "" {
		return nil, nil
	}
	lines := strings.Split(body, "\n")
	commits := make([]commitMeta, 0, len(lines))
	for _, line := range lines {
		i := strings.IndexByte(line, 0)
		if i < 0 {
			// Defensive: skip malformed rows rather than
			// fabricate a commit. In practice git always
			// emits the NUL when --format includes %x00.
			continue
		}
		commits = append(commits, commitMeta{
			sha:     line[:i],
			subject: line[i+1:],
		})
	}
	return commits, nil
}

// groupForFold walks the oldest-first commit list and partitions
// it into fold groups. Wip!: commits at the front of the list
// (before any anchor) are impossible by construction — the caller
// already errored out when nonWipCount==0 — but we still guard
// defensively: a leading wip without an anchor is silently
// dropped from the replay (its diff lands in the next anchor's
// tree anyway, since that anchor was committed after the wip).
//
// In practice the structure is always:
//
//	[anchor1] [wip] [wip] … [anchor2] [wip] …
//
// and groupForFold splits at each anchor.
func groupForFold(commits []commitMeta) []foldGroup {
	var groups []foldGroup
	var cur *foldGroup
	for _, c := range commits {
		if !strings.HasPrefix(c.subject, WipPrefix) {
			// New anchor → close the previous group (if any)
			// and open a fresh one.
			if cur != nil {
				groups = append(groups, *cur)
			}
			cur = &foldGroup{anchor: c}
			continue
		}
		// wip!: commit → append to current group's tail.
		// If there's no current group (leading wips with no
		// preceding anchor), drop them — the caller has
		// already validated that at least one non-wip exists.
		if cur != nil {
			cur.tail = append(cur.tail, c)
		}
	}
	if cur != nil {
		groups = append(groups, *cur)
	}
	return groups
}

// replayGroup synthesises a new commit on top of `parent` whose
// tree matches the last commit in the group and whose message +
// author come from the anchor (the non-wip commit). Returns the
// sha of the new commit.
//
// Author and committer environment vars are pulled from the
// anchor commit so `git log` shows the original authorship; only
// the parent pointer changes (the rebase moves the commit, but
// keeps "who wrote it").
func replayGroup(ctx context.Context, cwd, parent string, g foldGroup) (string, error) {
	tree, err := runGitCtxStr(ctx, cwd, "rev-parse", g.last().sha+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("rev-parse tree: %w", err)
	}
	// %B = full raw body (subject + blank + body + trailers).
	msg, err := runGitCtxStr(ctx, cwd, "log", "-1", "--format=%B", g.anchor.sha)
	if err != nil {
		return "", fmt.Errorf("log anchor message: %w", err)
	}
	// %an / %ae / %ad — author identity + date as recorded on
	// the anchor commit. We propagate via env so `git
	// commit-tree` records the original author.
	authorName, err := runGitCtxStr(ctx, cwd, "log", "-1", "--format=%an", g.anchor.sha)
	if err != nil {
		return "", fmt.Errorf("log anchor author name: %w", err)
	}
	authorEmail, err := runGitCtxStr(ctx, cwd, "log", "-1", "--format=%ae", g.anchor.sha)
	if err != nil {
		return "", fmt.Errorf("log anchor author email: %w", err)
	}
	authorDate, err := runGitCtxStr(ctx, cwd, "log", "-1", "--format=%aI", g.anchor.sha)
	if err != nil {
		return "", fmt.Errorf("log anchor author date: %w", err)
	}

	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+authorName,
		"GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_AUTHOR_DATE="+authorDate,
		// Committer info intentionally falls through to the
		// caller's git identity — `git log` distinguishes
		// author (original writer) from committer (who
		// performed the rebase / squash). This matches what
		// `git rebase --autosquash` would have produced.
	)

	cmd := gitCommandWithEnv(ctx, cwd, env,
		"commit-tree",
		tree,
		"-p", parent,
		"-m", strings.TrimRight(msg, "\n"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runGitCtxStr is runGitCtx with the output already trimmed and
// converted to string — saves the same three lines in every
// caller.
func runGitCtxStr(ctx context.Context, cwd string, args ...string) (string, error) {
	out, err := runGitCtx(ctx, cwd, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
