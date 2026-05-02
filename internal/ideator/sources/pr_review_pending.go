// Package sources — pr_review_pending source.
//
// Calls `gh pr list --search "review:none" --json
// number,title,createdAt,author,reviewDecision` and emits one Idea
// per PR awaiting review for more than MinAge (default 24h). Every
// open PR that hasn't been reviewed yet is real signal — review
// queues silently grow when nobody triages them.
//
// Cheap-on-fail: missing gh CLI / unauthenticated / no PRs → no
// ideas, no error.
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

// DefaultPRMinAge is how old an unreviewed PR must be before it
// surfaces. 24h is operator-friendly: a PR opened in the morning
// and reviewed by EOD doesn't ring the bell.
const DefaultPRMinAge = 24 * time.Hour

// PRReviewPending implements IdeaSource for review-queue triage.
type PRReviewPending struct {
	// GHBinary lets tests inject a stub `gh`; defaults to "gh".
	GHBinary string
	// Limit caps how many PRs gh returns; default 10.
	Limit int
	// MinAge is how long a PR must sit unreviewed; default 24h.
	MinAge time.Duration
	// Timeout caps the gh subprocess; default 10s.
	Timeout time.Duration
	// Now is overridable for tests; defaults to time.Now.
	Now func() time.Time
}

// NewPRReviewPending returns a ready-to-use review-queue miner.
func NewPRReviewPending() *PRReviewPending {
	return &PRReviewPending{
		GHBinary: "gh",
		Limit:    10,
		MinAge:   DefaultPRMinAge,
		Timeout:  10 * time.Second,
	}
}

// Name returns the canonical source name.
func (PRReviewPending) Name() string { return "pr_review_pending" }

// ghPR mirrors the `gh pr list --json` shape we read.
type ghPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	Author    struct {
		Login string `json:"login"`
		IsBot bool   `json:"is_bot"`
	} `json:"author"`
	ReviewDecision string `json:"reviewDecision"`
}

// Scan calls gh, returns one Idea per PR sitting unreviewed past
// MinAge. Empty + nil error on missing gh / network failure /
// auth failure (cheap-on-fail).
func (p PRReviewPending) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	bin := p.GHBinary
	if bin == "" {
		bin = "gh"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, nil
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 10
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	minAge := p.MinAge
	if minAge <= 0 {
		minAge = DefaultPRMinAge
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(subCtx, bin,
		"pr", "list",
		"--state", "open",
		"--search", "review:none",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,createdAt,author,reviewDecision",
	)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, nil
	}
	cutoff := now().Add(-minAge)
	ideas := make([]ideator.Idea, 0, len(prs))
	for _, pr := range prs {
		if pr.ReviewDecision != "" && pr.ReviewDecision != "REVIEW_REQUIRED" {
			continue
		}
		if pr.CreatedAt.After(cutoff) {
			continue
		}
		days := int(now().Sub(pr.CreatedAt).Hours() / 24)
		hash := sha1.Sum([]byte(fmt.Sprintf("%d", pr.Number)))
		evidence := fmt.Sprintf("gh-pr #%d by @%s opened %d days ago", pr.Number, pr.Author.Login, days)
		ideas = append(ideas, ideator.Idea{
			Title:             fmt.Sprintf("Review PR #%d: %s", pr.Number, pr.Title),
			Summary:           fmt.Sprintf("PR #%d (%s) opened by @%s on %s has been waiting for review for %d days.", pr.Number, pr.Title, pr.Author.Login, pr.CreatedAt.Format("2006-01-02"), days),
			Evidence:          evidence,
			SuggestedPriority: priorityForPR(days),
			SuggestedPrompt: fmt.Sprintf(
				"Review pending PR #%d.\n\n"+
					"  - title: %s\n"+
					"  - author: @%s\n"+
					"  - opened: %s (%d days ago)\n\n"+
					"Pull the diff via `gh pr view %d --json files,additions,deletions` + `gh pr diff %d`. "+
					"Decide: approve / request changes / leave a substantive comment. "+
					"Use `gh pr review %d --approve` / `--request-changes` / `--comment` to act. "+
					"Don't merge without an explicit operator instruction — this source is triage, not gate.",
				pr.Number, pr.Title, pr.Author.Login,
				pr.CreatedAt.Format("2006-01-02"), days,
				pr.Number, pr.Number, pr.Number,
			),
			DedupeKey: "pr_review_pending:" + hex.EncodeToString(hash[:]),
		})
	}
	return ideas, nil
}

// priorityForPR scales by age. New (1-2 days) gets 4 — same tier
// as deps_outdated. Stale (>7 days) gets 6 — review backlogs that
// linger drift toward bit-rot fast.
func priorityForPR(days int) int {
	switch {
	case days >= 7:
		return 6
	case days >= 3:
		return 5
	default:
		return 4
	}
}
