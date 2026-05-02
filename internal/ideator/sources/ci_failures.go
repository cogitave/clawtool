// Package sources — ci_failures source.
//
// Calls `gh run list --branch main --status failure --limit 10
// --json databaseId,name,headSha,conclusion,event,createdAt` and
// emits one Idea per failed run. Falls back to a quiet no-op when
// the gh CLI isn't installed or unauthenticated — the spec calls
// this a cheap-on-fail signal, not a hard requirement.
package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
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
}

// NewCIFailures returns a ready-to-use ci_failures miner.
func NewCIFailures() *CIFailures {
	return &CIFailures{
		Branch:   "main",
		Limit:    10,
		GHBinary: "gh",
		Timeout:  10 * time.Second,
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

	ideas := make([]ideator.Idea, 0, len(runs))
	for _, r := range runs {
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

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
