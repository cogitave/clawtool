package setuptools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// mkInitApplyReq fabricates an MCP CallToolRequest. nil core_only
// = use default (true); explicit false ⇒ Stable+Core. dryRun
// boolean is always set explicitly.
func mkInitApplyReq(coreOnly *bool, dryRun bool, repo string) mcp.CallToolRequest {
	args := map[string]any{
		"dry_run": dryRun,
	}
	if coreOnly != nil {
		args["core_only"] = *coreOnly
	}
	if repo != "" {
		args["repo"] = repo
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "InitApply",
			Arguments: args,
		},
	}
}

// TestInitApply_DryRunFreshRepo — dry_run on a fresh repo
// surfaces every Core recipe as "would apply" without writing
// anything. Core_only=true (the default) is in scope.
func TestInitApply_DryRunFreshRepo(t *testing.T) {
	repo := t.TempDir()

	res, err := runInitApply(context.Background(), mkInitApplyReq(nil, true, repo))
	if err != nil {
		t.Fatalf("runInitApply: %v", err)
	}
	got, ok := res.StructuredContent.(initApplyResult)
	if !ok {
		t.Fatalf("StructuredContent = %T, want initApplyResult", res.StructuredContent)
	}
	if got.Repo != repo {
		t.Errorf("Repo = %q, want %q", got.Repo, repo)
	}
	if !got.DryRun {
		t.Error("DryRun = false; want true")
	}
	if !got.CoreOnly {
		t.Error("CoreOnly = false; want true (default)")
	}
	// Dry-run should pre-populate Applied with Core recipes that
	// would land on this repo. The set isn't constant — recipes
	// ship + retire — but it MUST be non-empty because Core
	// recipes exist (claude-md / agent-claim / commit-format-ci /
	// promptfoo-redteam / rtk-token-filter / mattpocock-skills as
	// of this writing).
	if len(got.Applied) == 0 {
		t.Error("Applied is empty on dry-run against fresh repo; want at least one Core recipe")
	}
	// On dry-run, no files should be written. Verify by reading
	// the repo dir — it should still be empty.
	repoFile := repo + "/CLAUDE.md"
	if _, err := stat(repoFile); err == nil {
		t.Errorf("CLAUDE.md was written under dry_run=true (path %s)", repoFile)
	}
	// next_steps prompts the agent to call again with dry_run=false.
	hasFollowup := false
	for _, s := range got.NextSteps {
		if contains(s, "dry_run=false") {
			hasFollowup = true
			break
		}
	}
	if !hasFollowup {
		t.Errorf("NextSteps should suggest a dry_run=false follow-up, got %v", got.NextSteps)
	}
}

// TestInitApply_NormalAppliesAndIdempotent — running with
// dry_run=false applies the recipes; a second call sees them as
// already-applied (the Skipped slice carries the names).
func TestInitApply_NormalAppliesAndIdempotent(t *testing.T) {
	repo := t.TempDir()

	// First call — apply.
	res1, err := runInitApply(context.Background(), mkInitApplyReq(nil, false, repo))
	if err != nil {
		t.Fatalf("first runInitApply: %v", err)
	}
	got1 := res1.StructuredContent.(initApplyResult)
	if len(got1.Applied) == 0 {
		t.Errorf("first call: Applied is empty; want at least one recipe applied (Failed=%v)", got1.Failed)
	}

	// Second call — every previously-applied recipe should now
	// land in Skipped with the "already applied" reason. Some
	// recipes might Detect as Partial (file exists but isn't
	// clawtool-managed yet) — that's allowed too; we just
	// require the second call doesn't double-apply (Applied
	// must be a strict subset of the first).
	res2, err := runInitApply(context.Background(), mkInitApplyReq(nil, false, repo))
	if err != nil {
		t.Fatalf("second runInitApply: %v", err)
	}
	got2 := res2.StructuredContent.(initApplyResult)
	if len(got2.Skipped) == 0 {
		t.Errorf("second call: Skipped is empty; want at least one already-applied recipe; got Applied=%v Pending=%v", got2.Applied, got2.Pending)
	}
	if len(got2.Applied) >= len(got1.Applied) {
		t.Errorf("second call: Applied=%d should be smaller than first call's %d; idempotency broken", len(got2.Applied), len(got1.Applied))
	}
}

// TestInitApply_CoreOnlyFalseExpandsSet — core_only=false widens
// the set to all Stable recipes; the Applied + Pending +
// Skipped sum should be ≥ the core_only=true count.
func TestInitApply_CoreOnlyFalseExpandsSet(t *testing.T) {
	repo := t.TempDir()

	coreFalse := false
	resWide, err := runInitApply(context.Background(), mkInitApplyReq(&coreFalse, true, repo))
	if err != nil {
		t.Fatalf("runInitApply wide: %v", err)
	}
	gotWide := resWide.StructuredContent.(initApplyResult)
	if gotWide.CoreOnly {
		t.Error("CoreOnly = true with core_only=false arg")
	}

	repo2 := t.TempDir()
	resCore, err := runInitApply(context.Background(), mkInitApplyReq(nil, true, repo2))
	if err != nil {
		t.Fatalf("runInitApply core: %v", err)
	}
	gotCore := resCore.StructuredContent.(initApplyResult)

	wideCount := len(gotWide.Applied) + len(gotWide.Pending) + len(gotWide.Skipped)
	coreCount := len(gotCore.Applied) + len(gotCore.Pending) + len(gotCore.Skipped)
	if wideCount < coreCount {
		t.Errorf("core_only=false saw %d recipes; core_only=true saw %d. The wider set should be ≥ the narrower one.", wideCount, coreCount)
	}
}

// stat is a tiny os.Stat alias kept for readability at call
// sites. Returns (any, error) instead of (os.FileInfo, error)
// so the test's err-only check stays terse.
func stat(path string) (any, error) { return os.Stat(path) }

// contains is a thin wrapper around strings.Contains so the
// test code reads as English ("does this nextStep contain the
// follow-up phrase?"). Pulling strings into the test namespace
// keeps the imports list above readable too.
func contains(s, sub string) bool { return strings.Contains(s, sub) }
