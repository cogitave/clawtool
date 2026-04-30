package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"

	// Side-effect: register every recipe so the matrix builder
	// and runInitAll see the production catalog.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// TestSetupWizard_IncludesCoreBeta pins that buildSetupMatrix
// surfaces a Beta+Core recipe (promptfoo-redteam) — the legacy
// matrix filter dropped every Beta row, leaving the operator with
// no way to enable a Core+Beta default. Regression guard.
func TestSetupWizard_IncludesCoreBeta(t *testing.T) {
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	items := buildSetupMatrix(a, t.TempDir())
	want := "recipe:promptfoo-redteam"
	for _, it := range items {
		if it.key == want {
			return
		}
	}
	t.Errorf("matrix missing %q — Beta+Core recipes must ride along with Stable rows", want)
}

// TestSetupWizard_PreChecksCore confirms every matrix item flagged
// core=true has its key in the default-selection set the wizard
// pre-applies. The default-selection logic lives inside runSetupV2;
// we replay it here against the pure matrix slice.
func TestSetupWizard_PreChecksCore(t *testing.T) {
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	items := buildSetupMatrix(a, t.TempDir())
	defaults := map[string]bool{}
	for _, it := range items {
		if it.category == matrixHost || it.category == matrixDaemon || it.core {
			defaults[it.key] = true
		}
	}
	// At least one Core recipe must surface as Absent in a clean
	// tempdir AND be pre-checked. promptfoo-redteam is the
	// canonical Beta+Core anchor.
	anchor := "recipe:promptfoo-redteam"
	if !defaults[anchor] {
		t.Errorf("Core recipe row %q must be in default-selection set; got defaults=%v", anchor, defaults)
	}
	// And no recipe row that isn't Core should have leaked in.
	for _, it := range items {
		if it.category == matrixRecipe && !it.core && defaults[it.key] {
			t.Errorf("non-Core recipe %q got pre-selected — opt-in only", it.key)
		}
	}
}

// TestInit_AllAppliesEveryCore drives `clawtool init --all` against
// a clean tempdir and asserts every Core recipe with no required
// options + no environment dependency lands. Non-Core recipes
// stay absent. Output uses the documented "✓ <name> applied"
// vocabulary so wrappers can grep it.
func TestInit_AllAppliesEveryCore(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}, ConfigPath: filepath.Join(dir, "config.toml")}

	rc := app.runInit([]string{"--all"})
	if rc != 0 {
		t.Fatalf("runInit --all exit = %d, want 0; output: %s", rc, out.String())
	}
	got := out.String()

	// Pin: at least the file-writing Core recipes for this repo
	// surfaced as applied. claude-md, conventional-commits-ci,
	// promptfoo-redteam all just write files in the cwd, so they
	// must land.
	for _, want := range []string{
		"claude-md applied",
		"conventional-commits-ci applied",
		"promptfoo-redteam applied",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got: %s", want, got)
		}
	}
	// Non-Core recipes must NOT have been applied. release-please
	// is Stable but not Core — it must stay out of the --all run.
	if strings.Contains(got, "release-please applied") {
		t.Errorf("non-Core recipe release-please should not be applied by --all; got: %s", got)
	}
	// Files actually landed.
	for _, rel := range []string{"CLAUDE.md", ".github/workflows/commit-format.yml", "promptfooconfig.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("Core recipe artifact %q not on disk: %v", rel, err)
		}
	}
}

// TestOnboard_DefaultsNudgePromptYes pins that maybeNudgeCoreDefaults
// applies every Core recipe when the operator hits enter / --yes /
// non-TTY, and skips entirely when --no-defaults is set. Drives the
// onboard tail-end nudge contract.
func TestOnboard_DefaultsNudgePromptYes(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Y default branch (non-TTY → unconditional apply).
	out := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: &bytes.Buffer{}, ConfigPath: filepath.Join(dir, "config.toml")}
	app.maybeNudgeCoreDefaults(true /*yes*/, false /*noDefaults*/)
	got := out.String()
	if !strings.Contains(got, "core defaults") {
		t.Errorf("nudge should print the 'core defaults' banner when applying; got: %s", got)
	}
	if !strings.Contains(got, "applied") {
		t.Errorf("nudge with yes=true must apply at least one Core recipe; got: %s", got)
	}

	// --no-defaults branch must be silent.
	dir2 := t.TempDir()
	if err := os.Chdir(dir2); err != nil {
		t.Fatal(err)
	}
	out2 := &bytes.Buffer{}
	app2 := &App{Stdout: out2, Stderr: &bytes.Buffer{}, ConfigPath: filepath.Join(dir2, "config.toml")}
	app2.maybeNudgeCoreDefaults(true /*yes*/, true /*noDefaults*/)
	if out2.Len() != 0 {
		t.Errorf("nudge with --no-defaults must be silent; got: %q", out2.String())
	}
	// And no Core recipe artifact landed.
	if _, err := os.Stat(filepath.Join(dir2, "CLAUDE.md")); err == nil {
		t.Errorf("--no-defaults must NOT apply Core recipes; CLAUDE.md was created")
	}

	// Marker recipes registered Core must report it via Meta.
	for _, name := range []string{"agent-claim", "claude-md", "conventional-commits-ci", "promptfoo-redteam"} {
		r := setup.Lookup(name)
		if r == nil {
			t.Errorf("recipe %q not registered", name)
			continue
		}
		if !r.Meta().Core {
			t.Errorf("recipe %q missing Core: true on Meta", name)
		}
	}
}
