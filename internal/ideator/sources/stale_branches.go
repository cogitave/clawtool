// Package sources — stale_branches source.
//
// Surfaces remote feature branches whose tip is already merged into
// the default branch — i.e. PRs that landed but the branch was never
// deleted. Each one is a one-line `git push origin --delete` away
// from being cleaned up; in aggregate they pollute `git branch -a`,
// confuse `gh pr list`, and slow `git fetch`.
//
// Signal-driven, not heuristic: a merged branch is *definitely*
// removable. Priority 3 — same tier as deadcode, below ci_failures
// but above stale_files.
//
// Cheap-on-fail: missing git / non-repo cwd → no ideas, no error.
package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/ideator"
)

// DefaultStaleBranchPrefix scopes the source to autodev branches by
// default — those are the ones the autonomous loop creates and the
// loop should clean up after itself. Operators with different naming
// conventions (`feature/`, `wip/`) override the field.
const DefaultStaleBranchPrefix = "origin/autodev/"

// DefaultStaleBranchMaxIdeas caps the result set; merged-but-undeleted
// branches accumulate fast on a busy autonomous loop, and 5 per ideate
// run is enough to drain the backlog over a few cycles without drowning
// the autopilot queue in single-line cleanup items.
const DefaultStaleBranchMaxIdeas = 5

// StaleBranches implements IdeaSource for "remote branch tip already
// reachable from default branch HEAD".
type StaleBranches struct {
	// GitBinary lets tests inject a stub; defaults to "git".
	GitBinary string
	// DefaultBranch is the merge target tested against (default
	// "origin/main"). Branches whose tip is already in this ref's
	// history are considered merged.
	DefaultBranch string
	// Prefix is the ref-name prefix that scopes which branches are
	// candidates. Default `origin/autodev/`. Set to "origin/" to
	// scope to the whole remote.
	Prefix string
	// MaxIdeas caps the result set; default DefaultStaleBranchMaxIdeas.
	MaxIdeas int
	// SkipBranches lists ref names to never surface (default
	// includes `origin/main`, `origin/HEAD`).
	SkipBranches []string
}

// NewStaleBranches returns a ready-to-use stale-branch miner.
func NewStaleBranches() *StaleBranches {
	return &StaleBranches{
		GitBinary:     "git",
		DefaultBranch: "origin/main",
		Prefix:        DefaultStaleBranchPrefix,
		MaxIdeas:      DefaultStaleBranchMaxIdeas,
		SkipBranches: []string{
			"origin/main",
			"origin/HEAD",
		},
	}
}

// Name returns the canonical source name.
func (StaleBranches) Name() string { return "stale_branches" }

// Scan calls `git branch -r --merged <default>` and emits one Idea
// per branch matching Prefix. Returns empty + nil error on missing
// git / non-repo cwd / unparseable output.
func (s StaleBranches) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	gitBin := s.GitBinary
	if gitBin == "" {
		gitBin = "git"
	}
	if _, err := exec.LookPath(gitBin); err != nil {
		return nil, nil
	}
	defaultBranch := s.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "origin/main"
	}
	prefix := s.Prefix
	if prefix == "" {
		prefix = DefaultStaleBranchPrefix
	}
	maxIdeas := s.MaxIdeas
	if maxIdeas <= 0 {
		maxIdeas = DefaultStaleBranchMaxIdeas
	}
	skip := make(map[string]struct{}, len(s.SkipBranches))
	for _, b := range s.SkipBranches {
		skip[b] = struct{}{}
	}

	cmd := exec.CommandContext(ctx, gitBin, "branch", "-r", "--merged", defaultBranch)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Drop any "<branch> -> <target>" symbolic-ref pointers.
		if idx := strings.Index(line, " -> "); idx >= 0 {
			line = line[:idx]
		}
		if _, drop := skip[line]; drop {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		branches = append(branches, line)
	}
	sort.Strings(branches)
	if len(branches) > maxIdeas {
		branches = branches[:maxIdeas]
	}
	ideas := make([]ideator.Idea, 0, len(branches))
	for _, full := range branches {
		short := strings.TrimPrefix(full, "origin/")
		hash := sha1.Sum([]byte(full))
		ideas = append(ideas, ideator.Idea{
			Title:             fmt.Sprintf("Delete merged branch %s", short),
			Summary:           fmt.Sprintf("Remote branch %s is already reachable from %s; PR landed and the branch never got cleaned up.", full, defaultBranch),
			Evidence:          fmt.Sprintf("git branch -r --merged %s | grep %s", defaultBranch, short),
			SuggestedPriority: 3,
			SuggestedPrompt: fmt.Sprintf(
				"Delete the merged remote branch `%s`.\n\n"+
					"  - branch: %s\n"+
					"  - merged into: %s\n\n"+
					"Verify the branch is actually merged: `git log %s..%s` should be empty. "+
					"Then run `git push origin --delete %s` to delete it. "+
					"This is a hygiene-only operation: every autodev branch the loop ships accumulates here until cleaned up; left alone they pollute `git branch -a` and slow `git fetch`.",
				full, full, defaultBranch, full, defaultBranch, short,
			),
			DedupeKey: "stale_branches:" + hex.EncodeToString(hash[:]),
		})
	}
	return ideas, nil
}
