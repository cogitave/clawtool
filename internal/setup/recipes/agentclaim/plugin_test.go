package agentclaim

import (
	"context"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

// The plugin recipes spawn `claude plugin list` for Detect; we
// can't unit-test the success path without a Claude CLI on the
// runner. What we CAN cover deterministically:
//   - registration (Meta is well-formed, Upstream non-empty)
//   - the canonical install command shape
//   - Detect returning Absent when claude is not on PATH

func TestPluginRecipes_AreAllRegistered(t *testing.T) {
	for _, name := range []string{"caveman", "superclaude", "claude-flow"} {
		r := setup.Lookup(name)
		if r == nil {
			t.Fatalf("plugin recipe %q should self-register via init()", name)
		}
		if r.Meta().Category != setup.CategoryAgents {
			t.Errorf("%s: wrong category %q", name, r.Meta().Category)
		}
		if r.Meta().Upstream == "" {
			t.Errorf("%s: Upstream must be set", name)
		}
		if r.Meta().Stability != setup.StabilityBeta {
			t.Errorf("%s: expected Beta stability for new plugin recipe; got %q", name, r.Meta().Stability)
		}
	}
}

func TestPluginInstallCmd_BuildsCanonicalForm(t *testing.T) {
	p := pluginRecipe{
		name:        "demo",
		repoSlug:    "org/repo",
		marketplace: "repo-marketplace",
	}
	cmd := pluginInstallCmd(p)
	// The shell wrapper is the contract — sh -c "claude plugin
	// marketplace add <slug> 2>/dev/null; claude plugin install
	// <name>@<marketplace>". Anything else is a regression.
	if len(cmd) != 3 {
		t.Fatalf("expected sh -c <command> form (3 elements); got %v", cmd)
	}
	if cmd[0] != "sh" || cmd[1] != "-c" {
		t.Errorf("expected `sh -c …` invocation; got %v", cmd)
	}
	want := "claude plugin marketplace add org/repo 2>/dev/null; claude plugin install demo@repo-marketplace"
	if cmd[2] != want {
		t.Errorf("install command:\n  got:  %s\n  want: %s", cmd[2], want)
	}
}

func TestPluginRecipe_PrereqsCoverClaudeAndPlugin(t *testing.T) {
	r := setup.Lookup("caveman")
	prs := r.Prereqs()
	if len(prs) != 2 {
		t.Fatalf("expected 2 prereqs (Claude CLI + plugin), got %d", len(prs))
	}
	gotCLI, gotPlugin := false, false
	for _, p := range prs {
		if p.Name == "Claude Code CLI" {
			gotCLI = true
			if p.ManualHint == "" {
				t.Error("Claude CLI prereq missing ManualHint")
			}
		}
		if p.Name == "caveman plugin (Claude Code marketplace)" {
			gotPlugin = true
			// Install commands across all three platforms must be present.
			for _, plat := range []setup.Platform{setup.PlatformDarwin, setup.PlatformLinux, setup.PlatformWindows} {
				if len(p.Install[plat]) == 0 {
					t.Errorf("plugin prereq missing Install for %s", plat)
				}
			}
		}
	}
	if !gotCLI {
		t.Error("Claude CLI prereq missing")
	}
	if !gotPlugin {
		t.Error("plugin-specific prereq missing")
	}
}

// Detect should not panic when claude isn't on PATH; instead
// returns Absent with a clear hint. We can't reliably hide claude
// from PATH inside go test, so we just verify the call doesn't
// blow up under any state.
func TestPluginRecipe_DetectIsRobust(t *testing.T) {
	r := setup.Lookup("superclaude")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil && status != setup.StatusError {
		t.Errorf("Detect returned non-Error status with non-nil err: status=%q err=%v", status, err)
	}
}
