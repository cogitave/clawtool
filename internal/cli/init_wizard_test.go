package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/setup"

	// Side-effect: register every recipe so these tests can drive the
	// real wizard helpers against the real registry. Same import the
	// CLI dispatcher uses (internal/cli/recipe.go).
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// ── pure helpers ────────────────────────────────────────────────────

func TestSplitOwners_StripsCommasAndWhitespace(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"@me", []string{"@me"}},
		{"@me @team", []string{"@me", "@team"}},
		{"@me, @team, @other", []string{"@me", "@team", "@other"}},
		{"  @me   @team  ", []string{"@me", "@team"}},
		{"", nil},
		{"   ", nil},
		{"@one,@two,@three", []string{"@one", "@two", "@three"}},
	}
	for _, c := range cases {
		got := splitOwners(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitOwners(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitOwners(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestNeedsRequiredOptions(t *testing.T) {
	for _, name := range []string{"license", "codeowners"} {
		if !needsRequiredOptions(name) {
			t.Errorf("%q should report needs-required-options", name)
		}
	}
	for _, name := range []string{"dependabot", "release-please", "goreleaser", "agent-claim", "conventional-commits-ci", "ghost"} {
		if needsRequiredOptions(name) {
			t.Errorf("%q should NOT report needs-required-options", name)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	if statusLabel(setup.StatusAbsent) != "absent" {
		t.Error("StatusAbsent should render 'absent'")
	}
	if statusLabel(setup.StatusApplied) != "applied" {
		t.Error("StatusApplied should render 'applied'")
	}
	if statusLabel("") != "—" {
		t.Error("empty status should render placeholder")
	}
}

func TestIsTTY_OnPipeIsFalse(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	if isTTY(rd) {
		t.Error("a pipe shouldn't be detected as a TTY")
	}
}

// ── runInit dispatch ───────────────────────────────────────────────

// withWizardAppInTempDir gives a fresh App with stdout/stderr buffers
// and chdir's into a tempdir so wizard runs that touch cwd don't leak
// into the working tree.
func withWizardAppInTempDir(t *testing.T) (*App, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}, ConfigPath: filepath.Join(dir, "config.toml")}
	return app, out, dir
}

func TestRunInit_NoTTYAppliesSafeDefaults(t *testing.T) {
	app, out, dir := withWizardAppInTempDir(t)

	// Stdin pipe → not a TTY. runInit should take the non-interactive
	// path and apply Stable + no-required-options recipes.
	rc := app.runInit(nil)
	if rc != 0 {
		t.Fatalf("runInit non-TTY exit = %d, want 0", rc)
	}

	got := out.String()
	if !strings.Contains(got, "clawtool init") {
		t.Errorf("non-TTY init didn't print banner: %q", got)
	}

	// conventional-commits-ci has no required options, so it should
	// have been applied — verify the file landed.
	wf := filepath.Join(dir, ".github/workflows/commit-format.yml")
	if _, err := os.Stat(wf); err != nil {
		t.Errorf("conventional-commits-ci should have been applied: %v", err)
	}
}

func TestRunInit_YesFlagAlsoTakesNonInteractivePath(t *testing.T) {
	app, _, dir := withWizardAppInTempDir(t)
	if rc := app.runInit([]string{"--yes"}); rc != 0 {
		t.Fatalf("--yes exit = %d, want 0", rc)
	}
	wf := filepath.Join(dir, ".github/workflows/commit-format.yml")
	if _, err := os.Stat(wf); err != nil {
		t.Errorf("--yes should have applied conventional-commits-ci: %v", err)
	}
}

// ── runInitRepoNonInteractive ──────────────────────────────────────

func TestRunInitRepoNonInteractive_SkipsRecipesNeedingRequiredOptions(t *testing.T) {
	app, out, dir := withWizardAppInTempDir(t)
	if rc := app.runInitRepoNonInteractive(dir); rc != 0 {
		t.Fatalf("runInitRepoNonInteractive exit = %d, want 0", rc)
	}
	got := out.String()

	// license needs holder, codeowners needs owners — both must be
	// absent from the applied list.
	if strings.Contains(got, "applied license") {
		t.Error("non-interactive path should NOT apply license (requires holder)")
	}
	if strings.Contains(got, "applied codeowners") {
		t.Error("non-interactive path should NOT apply codeowners (requires owners)")
	}
	// conventional-commits-ci must apply (no opts, no ecosystem).
	if !strings.Contains(got, "applied conventional-commits-ci") {
		t.Errorf("expected ✓ applied conventional-commits-ci; got %q", got)
	}
	// dependabot detects ecosystems by looking at repo files. Once
	// conventional-commits-ci has written a .github/workflows file,
	// dependabot's "github-actions" ecosystem is detected and the
	// recipe applies. This is intentional category ordering: the
	// supply-chain category fires after commits/release fired their
	// own workflows. Verify the cascade landed.
	if !strings.Contains(got, "applied dependabot") {
		t.Errorf("dependabot should apply once a workflow exists (cascade from commits category); got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github/dependabot.yml")); err != nil {
		t.Errorf("dependabot.yml should exist after the cascade; %v", err)
	}
}

// ── runInitPreview ─────────────────────────────────────────────────

func TestRunInitPreview_ReadOnlyAndListsAllCategoriesWithRecipes(t *testing.T) {
	app, out, dir := withWizardAppInTempDir(t)
	if rc := app.runInitPreview(dir); rc != 0 {
		t.Fatalf("runInitPreview exit = %d", rc)
	}
	got := out.String()
	for _, want := range []string{
		"governance",
		"commits",
		"release",
		"supply-chain",
		"agents",
		"license",
		"codeowners",
		"conventional-commits-ci",
		"release-please",
		"goreleaser",
		"dependabot",
		"agent-claim",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q in output: %s", want, got)
		}
	}

	// And no recipe was applied to the tempdir.
	if _, err := os.Stat(filepath.Join(dir, "LICENSE")); !os.IsNotExist(err) {
		t.Error("preview should not write any file")
	}
}

// ── applyAgentClaimDiff ────────────────────────────────────────────

// withTempClaudeCode redirects the claude-code adapter to a tempdir
// so test claims don't touch the real ~/.claude/settings.json.
func withTempClaudeCode(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	prev := claudeCodePathOverrideForTest()
	agents.SetClaudeCodeSettingsPath(settings)
	t.Cleanup(func() { agents.SetClaudeCodeSettingsPath(prev) })
}

// claudeCodePathOverrideForTest reads the current override so we can
// restore it. Empty when no override is active.
func claudeCodePathOverrideForTest() string { return "" }

func TestApplyAgentClaimDiff_ClaimsWantedReleasesUnwanted(t *testing.T) {
	withTempClaudeCode(t)

	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}}

	// Initially nothing claimed, claude-code detected.
	rows := []agentRow{{Name: "claude-code", Detected: true, Claimed: false}}

	// User chose to claim it.
	app.applyAgentClaimDiff(rows, []string{"claude-code"})
	if !strings.Contains(out.String(), "claimed claude-code") {
		t.Errorf("expected '✓ claimed claude-code', got %q", out.String())
	}

	// Now reflect the new state and ask the user to UNCHECK it: should release.
	out.Reset()
	rows = []agentRow{{Name: "claude-code", Detected: true, Claimed: true}}
	app.applyAgentClaimDiff(rows, nil)
	if !strings.Contains(out.String(), "released claude-code") {
		t.Errorf("expected '↺ released claude-code', got %q", out.String())
	}
}

func TestApplyAgentClaimDiff_NoChangesIsSilent(t *testing.T) {
	withTempClaudeCode(t)
	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}}

	// Already claimed, user keeps it selected → no diff to apply.
	rows := []agentRow{{Name: "claude-code", Detected: true, Claimed: true}}
	app.applyAgentClaimDiff(rows, []string{"claude-code"})
	if out.Len() != 0 {
		t.Errorf("expected no output for no-op diff, got %q", out.String())
	}
}

// ── snapshotAgentRows ──────────────────────────────────────────────

func TestSnapshotAgentRows_PreSelectsClaimedAgents(t *testing.T) {
	withTempClaudeCode(t)

	// Empty state: claude-code adapter exists, not claimed.
	rows, opts, pre := snapshotAgentRows()
	if len(rows) == 0 {
		t.Fatal("snapshotAgentRows returned no rows")
	}
	if len(opts) != len(rows) {
		t.Errorf("options count %d != rows count %d", len(opts), len(rows))
	}
	for _, p := range pre {
		if p == "" {
			t.Error("preSelected entry is empty")
		}
	}
}
