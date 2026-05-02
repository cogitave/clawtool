// Package sources — ci_failures source.
//
// Calls `gh run list --branch main --status failure --limit 10
// --json databaseId,name,headSha,conclusion,event,createdAt` and
// emits one Idea per failed run. Falls back to a quiet no-op when
// the gh CLI isn't installed or unauthenticated — the spec calls
// this a cheap-on-fail signal, not a hard requirement.
//
// Superseded-run filter: a failed run is dropped from the result set
// when ANY of (a) its head sha is no longer reachable from HEAD (the
// commit was force-pushed or rewritten away — re-running won't help),
// (b) its head sha is more than MaxCommitsBehind commits behind the
// current branch tip, or (c) the same workflow has had a later
// successful run on the same branch. All probes use `gh`/`git` so
// they share the same auth + cheap-on-fail story as the main query.
package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/ideator"
)

// CIFailures implements IdeaSource. Construct via NewCIFailures.
type CIFailures struct {
	// Branch is the branch to inspect (default "main").
	Branch string
	// Limit caps how many failed runs gh returns (default 10).
	Limit int
	// GHBinary lets tests inject a stub binary; defaults to "gh".
	GHBinary string
	// Timeout caps the gh subprocess (default 10s).
	Timeout time.Duration
	// MaxCommitsBehind drops failures whose head sha is more than
	// this many commits behind the current branch tip (default 20).
	// Set to 0 to disable the distance probe.
	MaxCommitsBehind int
	// GitBinary lets tests inject a stub `git`; defaults to "git".
	// Used by the distance probe (`git rev-list --count A..HEAD`).
	GitBinary string
	// SkipSupersededByGreen toggles the per-workflow "later green
	// run" probe. Default true. Tests that don't want to stub gh's
	// success query can disable it.
	SkipSupersededByGreen bool
}

// NewCIFailures returns a ready-to-use ci_failures miner.
func NewCIFailures() *CIFailures {
	return &CIFailures{
		Branch:                "main",
		Limit:                 10,
		GHBinary:              "gh",
		GitBinary:             "git",
		Timeout:               10 * time.Second,
		MaxCommitsBehind:      20,
		SkipSupersededByGreen: true,
	}
}

// Name returns the canonical source name.
func (CIFailures) Name() string { return "ci_failures" }

// ghRun is the JSON shape gh returns for `--json
// databaseId,name,headSha,conclusion,event,createdAt`.
type ghRun struct {
	DatabaseID int64     `json:"databaseId"`
	Name       string    `json:"name"`
	HeadSha    string    `json:"headSha"`
	Conclusion string    `json:"conclusion"`
	Event      string    `json:"event"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Scan calls gh, parses JSON, returns one Idea per failed run.
// Missing gh / network failure / auth failure → empty + nil error.
//
// Superseded runs (head sha far behind tip, OR a newer green run on
// the same workflow) are dropped before any Idea is emitted.
func (c CIFailures) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	bin := c.GHBinary
	if bin == "" {
		bin = "gh"
	}
	if _, err := exec.LookPath(bin); err != nil {
		// gh isn't installed on this host — quietly no-op.
		return nil, nil
	}
	branch := c.Branch
	if branch == "" {
		branch = "main"
	}
	limit := c.Limit
	if limit <= 0 {
		limit = 10
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(subCtx, bin,
		"run", "list",
		"--branch", branch,
		"--status", "failure",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "databaseId,name,headSha,conclusion,event,createdAt",
	)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// Auth / network / "not a gh repo" — silent no-op.
		return nil, nil
	}
	var runs []ghRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, nil
	}

	// Per-workflow cache so we only ask gh for the latest green
	// once per distinct workflow name even if the failure list has
	// several runs of the same workflow.
	latestGreen := make(map[string]time.Time)

	ideas := make([]ideator.Idea, 0, len(runs))
	for _, r := range runs {
		if c.isSuperseded(subCtx, repoRoot, bin, branch, r, latestGreen) {
			continue
		}
		evidence := fmt.Sprintf("gh-run %d (%s @ %s)", r.DatabaseID, r.Name, shortSHA(r.HeadSha))
		hash := sha1.Sum([]byte(fmt.Sprintf("%d:%s", r.DatabaseID, r.HeadSha)))
		ideas = append(ideas, ideator.Idea{
			Title:             "CI failure: " + r.Name,
			Summary:           fmt.Sprintf("GitHub Actions run %d (%s) on %s failed at %s. Investigate the log, land a fix, and confirm a green re-run.", r.DatabaseID, r.Name, branch, r.CreatedAt.Format(time.RFC3339)),
			Evidence:          evidence,
			SuggestedPriority: 7,
			SuggestedPrompt: fmt.Sprintf(
				"Investigate the failed CI run %d (%s) on branch %s.\n\n"+
					"  - run id: %d\n  - workflow: %s\n  - head sha: %s\n  - failed at: %s\n\n"+
					"Pull the log via `gh run view %d --log-failed`, identify the root"+
					" cause, land the smallest fix that makes it green, and verify by"+
					" waiting for the next run on the same workflow to pass.",
				r.DatabaseID, r.Name, branch,
				r.DatabaseID, r.Name, r.HeadSha, r.CreatedAt.Format(time.RFC3339),
				r.DatabaseID),
			DedupeKey: "ci_failures:" + hex.EncodeToString(hash[:]),
		})
	}
	return ideas, nil
}

// isSuperseded returns true when the failed run should be dropped:
// either the head sha is far behind the current tip, or a later
// green run of the same workflow exists. Probes are cheap-on-fail —
// any error from git/gh leaves the run in the result set so we
// fail open rather than swallowing real failures.
func (c CIFailures) isSuperseded(ctx context.Context, repoRoot, ghBin, branch string, r ghRun, cache map[string]time.Time) bool {
	if r.HeadSha != "" {
		gitBin := c.gitBinary()
		if shaExistsLocal(ctx, gitBin, repoRoot, r.HeadSha) {
			if !shaReachableFromHead(ctx, gitBin, repoRoot, r.HeadSha) {
				return true
			}
		} else if shaProbeSucceeded(ctx, gitBin, repoRoot) {
			return true
		}
		if c.MaxCommitsBehind > 0 {
			if behind, ok := commitsBehind(ctx, gitBin, repoRoot, r.HeadSha); ok && behind > c.MaxCommitsBehind {
				return true
			}
		}
	}
	if !c.SkipSupersededByGreen || r.Name == "" {
		return false
	}
	greenAt, ok := cache[r.Name]
	if !ok {
		greenAt, ok = latestGreenForWorkflow(ctx, ghBin, repoRoot, branch, r.Name)
		// Cache zero-time too so we don't re-query a workflow with
		// no green history.
		cache[r.Name] = greenAt
		if !ok {
			return false
		}
	}
	if greenAt.IsZero() {
		return false
	}
	return greenAt.After(r.CreatedAt)
}

// gitBinary resolves the git binary to use, defaulting to "git".
func (c CIFailures) gitBinary() string {
	if c.GitBinary == "" {
		return "git"
	}
	return c.GitBinary
}

// commitsBehind returns how many commits the given sha is behind
// HEAD via `git rev-list --count <sha>..HEAD`. The boolean is false
// when git is missing / the sha is unknown / any error — callers
// fail open in that case.
func commitsBehind(ctx context.Context, gitBin, repoRoot, sha string) (int, bool) {
	if _, err := exec.LookPath(gitBin); err != nil {
		return 0, false
	}
	cmd := exec.CommandContext(ctx, gitBin, "rev-list", "--count", sha+"..HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// shaExistsLocal checks whether the named sha is known to git at all
// (`git cat-file -e <sha>` exits 0 when the object exists). Returns
// false when git is missing or the object isn't present locally.
func shaExistsLocal(ctx context.Context, gitBin, repoRoot, sha string) bool {
	if _, err := exec.LookPath(gitBin); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, gitBin, "cat-file", "-e", sha)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

// shaReachableFromHead returns true when the named sha is reachable
// from HEAD via `git merge-base --is-ancestor <sha> HEAD`. The command
// exits 0 (true), 1 (false), or 128 (error). Returns false on error so
// callers can pair this with shaExistsLocal to distinguish "not in our
// graph" from "git failed to answer".
func shaReachableFromHead(ctx context.Context, gitBin, repoRoot, sha string) bool {
	if _, err := exec.LookPath(gitBin); err != nil {
		return true // fail open — keep the failure visible
	}
	cmd := exec.CommandContext(ctx, gitBin, "merge-base", "--is-ancestor", sha, "HEAD")
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

// shaProbeSucceeded checks that the local repo is healthy enough for
// the unreachable-sha probe to be trusted. Without this, a host where
// `git` is missing or `repoRoot` isn't a git tree would mark every
// failure as superseded and silently swallow them all.
func shaProbeSucceeded(ctx context.Context, gitBin, repoRoot string) bool {
	if _, err := exec.LookPath(gitBin); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, gitBin, "rev-parse", "--verify", "HEAD")
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

// latestGreenForWorkflow asks gh for the most recent successful run
// of the named workflow on the named branch. Returns the createdAt
// timestamp + true on success, zero-time + false on any error.
func latestGreenForWorkflow(ctx context.Context, ghBin, repoRoot, branch, workflow string) (time.Time, bool) {
	cmd := exec.CommandContext(ctx, ghBin,
		"run", "list",
		"--branch", branch,
		"--workflow", workflow,
		"--status", "success",
		"--limit", "1",
		"--json", "createdAt,headSha",
	)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	var runs []struct {
		CreatedAt time.Time `json:"createdAt"`
		HeadSha   string    `json:"headSha"`
	}
	if err := json.Unmarshal(out, &runs); err != nil {
		return time.Time{}, false
	}
	if len(runs) == 0 {
		return time.Time{}, true // probed successfully, just no green
	}
	return runs[0].CreatedAt, true
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
