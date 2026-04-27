// Package checkpoint — git commit + safety net for clawtool.
//
// Per ADR-022 (drafting): the operator's "checkpoint" umbrella
// covers Commit (this file), autocommit, doc-sync rules, snapshot/
// restore, and dirty-tree guard. v1 ships only the Commit primitive
// — Conventional Commits validation, hard Co-Authored-By block,
// and a pre-commit rules.Verdict gate. The richer pieces
// (autocommit, snapshot, guard) layer on top in subsequent commits.
//
// Lives in internal/checkpoint, NOT internal/agents/biam — Codex's
// architectural review (BIAM task a3ef5af9) was explicit: "Do not
// reuse BIAM for checkpoint state. The overlap is 'SQLite exists,'
// not semantics." Checkpoint state is per-repo + per-session, not
// per-agent-task.
package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CommitOptions captures every input the Commit primitive accepts.
// The MCP tool layer (internal/tools/core/commit_tool.go) maps
// JSON args onto this struct so Validate / Run / Push stay pure
// and testable in isolation.
type CommitOptions struct {
	// Message is the proposed commit message body. Validated
	// against Conventional Commits unless RequireConventional
	// is false.
	Message string
	// Cwd is the repo root. Defaults to current directory.
	Cwd string
	// Files lists paths to stage before committing. When empty,
	// the existing index is used (operator stages manually or
	// via AutoStageAll=true).
	Files []string
	// AutoStageAll runs `git add -A` before commit. Default
	// false to avoid accidentally committing the world.
	AutoStageAll bool
	// AllowEmpty maps onto `git commit --allow-empty`. Default
	// false — empty commits are usually a bug.
	AllowEmpty bool
	// AllowDirty bypasses the working-tree dirtiness guard.
	// Default false — dirty trees during a commit usually mean
	// "you forgot to stage something or autocommit raced you".
	AllowDirty bool
	// RequireConventional enforces the Conventional Commits
	// shape. Default true (operator's policy); flip to false
	// for prototype repos that don't bother.
	RequireConventional bool
	// ForbidCoauthor hard-blocks any `Co-Authored-By` trailer.
	// Default true (operator memory feedback — never attribute
	// to AI). The flag exists so other operators using
	// clawtool can opt out; Bahadır's profile keeps it on.
	ForbidCoauthor bool
	// Push runs `git push` after the commit. Default false —
	// auto-push is loud and should be opt-in per call.
	Push bool
	// Sign maps onto `git commit -S`. When true, fails fast
	// if `git config commit.gpgsign` isn't already configured —
	// no silent fall-through to unsigned commits.
	Sign bool
}

// CommitResult is the structured return shape.
type CommitResult struct {
	Sha         string    `json:"sha"`
	ShortSha    string    `json:"short_sha"`
	Branch      string    `json:"branch,omitempty"`
	Subject     string    `json:"subject"`
	Files       []string  `json:"files,omitempty"`
	Pushed      bool      `json:"pushed"`
	CommittedAt time.Time `json:"committed_at"`
}

// ───── validators ────────────────────────────────────────────────

// conventionalCommitRe matches the Conventional Commits 1.0.0
// spec — see https://www.conventionalcommits.org/en/v1.0.0/.
//
// Form: type(scope)?(!)?: subject
// Allowed types: feat, fix, docs, style, refactor, perf, test,
// build, ci, chore, revert. Scope is an optional bracketed string.
// Bang (`!`) marks a breaking change (BREAKING CHANGE: footer
// also accepted but not enforced here).
var conventionalCommitRe = regexp.MustCompile(
	`^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9_\-./]+\))?(!)?: .+`,
)

// coauthorTrailerRe matches the "Co-Authored-By:" trailer Git
// recognises. Case-insensitive on the key per Git's own parser
// (see git-interpret-trailers(1)).
var coauthorTrailerRe = regexp.MustCompile(`(?im)^co-authored-by:`)

// ValidateMessage runs every message-level check the operator
// configured. Returns nil when the message passes; otherwise an
// error naming the failed check first so a caller's error display
// reads cleanly.
func ValidateMessage(msg string, opts CommitOptions) error {
	if strings.TrimSpace(msg) == "" {
		return errors.New("commit message is empty")
	}
	first := firstLine(msg)
	if opts.RequireConventional && !conventionalCommitRe.MatchString(first) {
		return fmt.Errorf(
			"commit message does not match Conventional Commits 1.0.0 — "+
				"expected `<type>(<scope>)?(!)?: <subject>`, got %q. "+
				"Allowed types: feat, fix, docs, style, refactor, perf, test, "+
				"build, ci, chore, revert.", first)
	}
	if opts.ForbidCoauthor && coauthorTrailerRe.MatchString(msg) {
		return errors.New(
			"commit message contains a Co-Authored-By trailer — operator " +
				"policy hard-blocks AI attribution in commits. Strip the trailer " +
				"before retrying.")
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ───── git plumbing ──────────────────────────────────────────────

// IsGitRepo reports whether cwd is inside a Git working tree.
// We shell out to `git rev-parse --is-inside-work-tree` rather
// than walking up looking for `.git` because submodules and
// worktrees both make the directory layout non-trivial; let
// Git answer the question.
func IsGitRepo(cwd string) bool {
	out, err := runGit(cwd, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// IsClean reports whether the working tree has no unstaged or
// untracked changes (git status --porcelain returns empty). When
// AllowDirty is false, the Commit caller refuses to proceed if
// this returns false AFTER staging.
func IsClean(cwd string) (bool, error) {
	out, err := runGit(cwd, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// CurrentBranch returns the symbolic branch name (or empty when
// detached). Used in CommitResult for the operator's render.
func CurrentBranch(cwd string) string {
	out, err := runGit(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "HEAD" {
		// Detached HEAD — surface as empty so the renderer
		// shows nothing rather than the literal "HEAD".
		return ""
	}
	return name
}

// Stage runs `git add` for each path. When paths is empty the
// caller may have set AutoStageAll, which is handled here too.
func Stage(cwd string, paths []string, autoAll bool) error {
	if autoAll {
		if _, err := runGit(cwd, "add", "-A"); err != nil {
			return fmt.Errorf("git add -A: %w", err)
		}
		return nil
	}
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	if _, err := runGit(cwd, args...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	return nil
}

// Run executes the actual `git commit -m <msg>` and returns the
// new SHA + branch + subject. ValidateMessage MUST have run
// before this point.
func Run(ctx context.Context, opts CommitOptions) (CommitResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}
	if !IsGitRepo(cwd) {
		return CommitResult{}, fmt.Errorf("not a git repository: %s", cwd)
	}

	if err := Stage(cwd, opts.Files, opts.AutoStageAll); err != nil {
		return CommitResult{}, err
	}

	args := []string{"commit", "-m", opts.Message}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	if opts.Sign {
		args = append(args, "-S")
	}
	if _, err := runGitCtx(ctx, cwd, args...); err != nil {
		return CommitResult{}, fmt.Errorf("git commit: %w", err)
	}

	sha, err := runGit(cwd, "rev-parse", "HEAD")
	if err != nil {
		return CommitResult{}, fmt.Errorf("read HEAD sha: %w", err)
	}
	full := strings.TrimSpace(string(sha))
	short := full
	if len(full) > 7 {
		short = full[:7]
	}

	res := CommitResult{
		Sha:         full,
		ShortSha:    short,
		Branch:      CurrentBranch(cwd),
		Subject:     firstLine(opts.Message),
		Files:       opts.Files,
		CommittedAt: time.Now(),
	}

	if opts.Push {
		if _, err := runGitCtx(ctx, cwd, "push"); err != nil {
			return res, fmt.Errorf("git push: %w", err)
		}
		res.Pushed = true
	}
	return res, nil
}

// ───── helpers ───────────────────────────────────────────────────

func runGit(cwd string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func runGitCtx(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
