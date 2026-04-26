package agentclaim

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
)

// pluginRecipe is the generalised "Claude Code plugin install"
// pattern lifted out of the brain recipe. Each instance is one
// canonical Claude Code plugin we want clawtool to be able to
// add via a checkbox in the wizard. Detection: parse
// `claude plugin list` for the plugin name. Apply: shell out to
// `claude plugin marketplace add` + `claude plugin install`.
//
// Apply doesn't write any repo file — Claude Code plugins are a
// host-level concern. We still satisfy the recipe contract so
// the install path goes through the same wizard / MCP / CLI
// surface as everything else; Detect's reply tells the user
// what state the plugin is in.
type pluginRecipe struct {
	name        string // recipe name (also matches the plugin id we grep for)
	description string
	upstream    string // canonical repo URL
	repoSlug    string // org/repo for `claude plugin marketplace add`
	marketplace string // marketplace alias (matches what add returns)
}

func (p pluginRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        p.name,
		Category:    setup.CategoryAgents,
		Description: p.description,
		Upstream:    p.upstream,
		Stability:   setup.StabilityBeta,
	}
}

// Detect parses `claude plugin list` for the plugin name. We
// fingerprint the human output deliberately — Claude CLI doesn't
// expose a stable JSON output flag at this writing.
func (p pluginRecipe) Detect(_ context.Context, _ string) (setup.Status, string, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return setup.StatusAbsent, "claude CLI not on PATH (install Claude Code first)", nil
	}
	cmd := exec.Command("claude", "plugin", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return setup.StatusError, "", fmt.Errorf("claude plugin list: %w", err)
	}
	body := strings.ToLower(string(out))
	if strings.Contains(body, strings.ToLower(p.name)) {
		return setup.StatusApplied, fmt.Sprintf("%s plugin installed", p.name), nil
	}
	return setup.StatusAbsent, fmt.Sprintf("%s plugin not installed", p.name), nil
}

// Prereqs surfaces the claude CLI itself + the host-level install
// commands. We don't try to install the claude CLI on the user's
// behalf — that's Claude Code's own install path; we just point
// at it.
func (p pluginRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{
		{
			Name: "Claude Code CLI",
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("claude"); err != nil {
					return fmt.Errorf("claude CLI not on PATH")
				}
				return nil
			},
			ManualHint: "Install Claude Code from https://claude.ai/code (or follow Anthropic's install instructions for your platform). claude must be on PATH for this recipe to detect or install plugins.",
		},
		{
			Name: fmt.Sprintf("%s plugin (Claude Code marketplace)", p.name),
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("claude"); err != nil {
					return fmt.Errorf("claude CLI not on PATH")
				}
				out, err := exec.Command("claude", "plugin", "list").CombinedOutput()
				if err != nil {
					return fmt.Errorf("claude plugin list failed: %w", err)
				}
				if !strings.Contains(strings.ToLower(string(out)), strings.ToLower(p.name)) {
					return fmt.Errorf("plugin %q not installed", p.name)
				}
				return nil
			},
			Install: map[setup.Platform][]string{
				setup.PlatformDarwin:  pluginInstallCmd(p),
				setup.PlatformLinux:   pluginInstallCmd(p),
				setup.PlatformWindows: pluginInstallCmd(p),
			},
			ManualHint: fmt.Sprintf("Run: claude plugin marketplace add %s && claude plugin install %s@%s", p.repoSlug, p.name, p.marketplace),
		},
	}
}

func pluginInstallCmd(p pluginRecipe) []string {
	return []string{
		"sh", "-c",
		fmt.Sprintf(
			"claude plugin marketplace add %s 2>/dev/null; claude plugin install %s@%s",
			p.repoSlug, p.name, p.marketplace,
		),
	}
}

// Apply for plugin recipes is a thin shell-out to the same install
// command Prereqs surfaces. We allow the wizard's prereq prompter
// to handle install consent — this Apply is the post-prereq
// "did it land?" verification, equivalent to a re-Detect.
//
// Force has no meaning for a plugin install (the plugin is either
// there or not), so opts is ignored.
func (p pluginRecipe) Apply(ctx context.Context, _ string, _ setup.Options) error {
	status, _, err := p.Detect(ctx, "")
	if err != nil {
		return err
	}
	if status == setup.StatusApplied {
		return nil // already installed; idempotent
	}
	// Try the install command directly. The wizard's interactive
	// prompter would normally have offered this through the
	// Prereq.Install path; the bare `clawtool recipe apply` path
	// or the MCP RecipeApply tool both end up here too, and we
	// run the install for them.
	cmd := pluginInstallCmd(p)
	if _, err := exec.LookPath(cmd[0]); err != nil {
		return fmt.Errorf("install requires %q on PATH: %w", cmd[0], err)
	}
	out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("plugin install failed: %s", strings.TrimSpace(string(out)))
	}
	// Re-detect to confirm.
	status, _, _ = p.Detect(ctx, "")
	if status != setup.StatusApplied {
		return fmt.Errorf("plugin %q install command ran but plugin not detected afterwards", p.name)
	}
	return nil
}

// Verify confirms the plugin is installed.
func (p pluginRecipe) Verify(ctx context.Context, _ string) error {
	status, _, err := p.Detect(ctx, "")
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if status != setup.StatusApplied {
		return fmt.Errorf("verify: %q plugin not installed", p.name)
	}
	return nil
}

// ── concrete plugin recipes ────────────────────────────────────────

func init() {
	setup.Register(pluginRecipe{
		name:        "caveman",
		description: "Caveman: minimalist Claude Code skill — strips ceremony, pure execution. Reddit-favored daily-driver.",
		upstream:    "https://github.com/lackeyjb/caveman",
		repoSlug:    "lackeyjb/caveman",
		marketplace: "caveman-marketplace",
	})
	setup.Register(pluginRecipe{
		name:        "superclaude",
		description: "SuperClaude: large slash-command + persona + workflow library for Claude Code. ~10k stars.",
		upstream:    "https://github.com/SuperClaude-Org/SuperClaude_Framework",
		repoSlug:    "SuperClaude-Org/SuperClaude_Framework",
		marketplace: "superclaude-marketplace",
	})
	setup.Register(pluginRecipe{
		name:        "claude-flow",
		description: "Claude Flow: multi-agent orchestration over Claude Code (swarm patterns, hive-mind workflows).",
		upstream:    "https://github.com/ruvnet/claude-flow",
		repoSlug:    "ruvnet/claude-flow",
		marketplace: "claude-flow-marketplace",
	})
}
