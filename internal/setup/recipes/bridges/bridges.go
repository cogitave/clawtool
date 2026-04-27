// Package bridges hosts the bridge recipes for the `agents` category —
// connectors from Claude Code to other coding-agent CLIs (Codex,
// OpenCode, Gemini). Per ADR-014 (and ADR-007 applied recursively) we
// install canonical bridges via `claude plugin install` rather than
// re-implementing them ourselves. Each recipe shells out to the
// upstream's marketplace + install commands and verifies the plugin
// landed.
//
// OpenCode is the exception: its `acp` mode ships in the upstream
// binary, so the recipe verifies the binary on PATH instead of
// installing a Claude Code plugin.
package bridges

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
)

// bridgeRecipe is the per-family bridge install recipe. Same shape as
// agentclaim/pluginRecipe but with a separate package + naming so
// "bridge to another CLI" stays distinct from "Claude Code skill or
// enhancement plugin" (caveman, superclaude, claude-flow).
//
// Apply doesn't write any repo file — bridge plugins are host-level.
// We still satisfy the recipe contract so the install path goes
// through the same wizard / MCP / CLI surface as everything else.
type bridgeRecipe struct {
	name        string // recipe id ("codex-bridge", "gemini-bridge", "opencode-bridge")
	family      string // CLI family ("codex", "gemini", "opencode") — what `clawtool bridge add <family>` accepts
	description string
	upstream    string // canonical URL of the bridge

	// pluginSlug is the plugin id Claude Code stores after install
	// (`codex` for codex-plugin-cc, `gemini` for gemini-plugin-cc).
	// Empty for non-plugin bridges (opencode).
	pluginSlug string

	// repoSlug is the org/repo for `claude plugin marketplace add`.
	// Empty for non-plugin bridges.
	repoSlug string

	// marketplace is the alias Claude Code assigns the marketplace
	// (e.g. "openai-codex", "abiswas97-gemini"). Empty for non-plugin
	// bridges.
	marketplace string

	// binaryName, when non-empty, switches the recipe into
	// "verify CLI on PATH" mode (used for opencode — its `acp`
	// subcommand ships with the binary, no separate plugin to install).
	binaryName string
}

func (b bridgeRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        b.name,
		Category:    setup.CategoryAgents,
		Description: b.description,
		Upstream:    b.upstream,
		Stability:   setup.StabilityBeta,
	}
}

// Detect: for plugin bridges, parse `claude plugin list` for the
// plugin slug. For binary-only bridges (opencode), check PATH.
func (b bridgeRecipe) Detect(_ context.Context, _ string) (setup.Status, string, error) {
	if b.binaryName != "" {
		if _, err := exec.LookPath(b.binaryName); err != nil {
			return setup.StatusAbsent, fmt.Sprintf("%s binary not on PATH", b.binaryName), nil
		}
		return setup.StatusApplied, fmt.Sprintf("%s binary present on PATH", b.binaryName), nil
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return setup.StatusAbsent, "claude CLI not on PATH (install Claude Code first)", nil
	}
	cmd := exec.Command("claude", "plugin", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return setup.StatusError, "", fmt.Errorf("claude plugin list: %w", err)
	}
	body := strings.ToLower(string(out))
	if strings.Contains(body, strings.ToLower(b.pluginSlug)) {
		return setup.StatusApplied, fmt.Sprintf("%s plugin installed", b.pluginSlug), nil
	}
	return setup.StatusAbsent, fmt.Sprintf("%s plugin not installed", b.pluginSlug), nil
}

func (b bridgeRecipe) Prereqs() []setup.Prereq {
	if b.binaryName != "" {
		return []setup.Prereq{
			{
				Name: fmt.Sprintf("%s binary", b.binaryName),
				Check: func(_ context.Context) error {
					if _, err := exec.LookPath(b.binaryName); err != nil {
						return fmt.Errorf("%s not on PATH", b.binaryName)
					}
					return nil
				},
				ManualHint: fmt.Sprintf(
					"Install the %s CLI from %s. The bridge uses %[1]s's built-in `acp` subcommand — no Claude Code plugin to install.",
					b.binaryName, b.upstream,
				),
			},
		}
	}
	return []setup.Prereq{
		{
			Name: "Claude Code CLI",
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("claude"); err != nil {
					return fmt.Errorf("claude CLI not on PATH")
				}
				return nil
			},
			ManualHint: "Install Claude Code from https://claude.ai/code (or follow Anthropic's install instructions for your platform). claude must be on PATH for this recipe to detect or install the bridge plugin.",
		},
		{
			Name: fmt.Sprintf("%s plugin (Claude Code marketplace)", b.pluginSlug),
			Check: func(_ context.Context) error {
				if _, err := exec.LookPath("claude"); err != nil {
					return fmt.Errorf("claude CLI not on PATH")
				}
				out, err := exec.Command("claude", "plugin", "list").CombinedOutput()
				if err != nil {
					return fmt.Errorf("claude plugin list failed: %w", err)
				}
				if !strings.Contains(strings.ToLower(string(out)), strings.ToLower(b.pluginSlug)) {
					return fmt.Errorf("plugin %q not installed", b.pluginSlug)
				}
				return nil
			},
			Install: map[setup.Platform][]string{
				setup.PlatformDarwin:  bridgeInstallCmd(b),
				setup.PlatformLinux:   bridgeInstallCmd(b),
				setup.PlatformWindows: bridgeInstallCmd(b),
			},
			ManualHint: fmt.Sprintf(
				"Run: claude plugin marketplace add %s && claude plugin install %s@%s",
				b.repoSlug, b.pluginSlug, b.marketplace,
			),
		},
	}
}

func bridgeInstallCmd(b bridgeRecipe) []string {
	return []string{
		"sh", "-c",
		fmt.Sprintf(
			"claude plugin marketplace add %s 2>/dev/null; claude plugin install %s@%s",
			b.repoSlug, b.pluginSlug, b.marketplace,
		),
	}
}

// Apply: idempotent re-detect, then install. For binary-only bridges
// we don't run an install; the user must install the upstream CLI
// themselves (we surface the ManualHint via the wizard's Prereq path).
func (b bridgeRecipe) Apply(ctx context.Context, _ string, _ setup.Options) error {
	status, _, err := b.Detect(ctx, "")
	if err != nil {
		return err
	}
	if status == setup.StatusApplied {
		return nil
	}
	if b.binaryName != "" {
		return fmt.Errorf("%s binary not on PATH; install it from %s and re-run", b.binaryName, b.upstream)
	}
	cmd := bridgeInstallCmd(b)
	if _, err := exec.LookPath(cmd[0]); err != nil {
		return fmt.Errorf("install requires %q on PATH: %w", cmd[0], err)
	}
	out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bridge install failed: %s", strings.TrimSpace(string(out)))
	}
	status, _, _ = b.Detect(ctx, "")
	if status != setup.StatusApplied {
		return fmt.Errorf("bridge %q install command ran but plugin not detected afterwards", b.pluginSlug)
	}
	return nil
}

func (b bridgeRecipe) Verify(ctx context.Context, _ string) error {
	status, _, err := b.Detect(ctx, "")
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if status != setup.StatusApplied {
		if b.binaryName != "" {
			return fmt.Errorf("verify: %s binary not on PATH", b.binaryName)
		}
		return fmt.Errorf("verify: %q plugin not installed", b.pluginSlug)
	}
	return nil
}

// Family returns the CLI family this bridge connects to. Used by the
// CLI's `clawtool bridge add <family>` resolver to find the matching
// recipe by family rather than by recipe name.
func (b bridgeRecipe) Family() string { return b.family }

// LookupByFamily returns the bridge recipe registered for the given
// family ("codex", "opencode", "gemini"), or nil. Driven by the CLI
// surface (`clawtool bridge add codex`).
func LookupByFamily(family string) setup.Recipe {
	target := strings.ToLower(strings.TrimSpace(family))
	for _, r := range setup.InCategory(setup.CategoryAgents) {
		if br, ok := r.(bridgeRecipe); ok && br.family == target {
			return r
		}
	}
	return nil
}

// Families returns the set of families with a registered bridge
// recipe. Stable across runs (sorted).
func Families() []string {
	out := make([]string, 0, 4)
	for _, r := range setup.InCategory(setup.CategoryAgents) {
		if br, ok := r.(bridgeRecipe); ok {
			out = append(out, br.family)
		}
	}
	return out
}

// ── concrete bridges ───────────────────────────────────────────────

func init() {
	setup.Register(bridgeRecipe{
		name:        "codex-bridge",
		family:      "codex",
		description: "Codex bridge: official OpenAI Claude Code plugin wrapping `codex app-server` JSON-RPC. Adds /codex:review, /codex:adversarial-review, /codex:rescue, /codex:status, /codex:result, /codex:cancel, /codex:setup slash commands and a codex:codex-rescue subagent inside Claude Code.",
		upstream:    "https://github.com/openai/codex-plugin-cc",
		pluginSlug:  "codex",
		repoSlug:    "openai/codex-plugin-cc",
		marketplace: "openai-codex",
	})
	setup.Register(bridgeRecipe{
		name:        "gemini-bridge",
		family:      "gemini",
		description: "Gemini bridge: community Claude Code plugin (abiswas97/gemini-plugin-cc) wrapping the Gemini CLI via ACP. Adds /gemini:review, /gemini:adversarial-review, /gemini:rescue, /gemini:task, /gemini:status, /gemini:result, /gemini:cancel, /gemini:setup slash commands and a gemini:gemini-rescue subagent.",
		upstream:    "https://github.com/abiswas97/gemini-plugin-cc",
		pluginSlug:  "gemini",
		repoSlug:    "abiswas97/gemini-plugin-cc",
		marketplace: "abiswas97-gemini",
	})
	setup.Register(bridgeRecipe{
		name:        "opencode-bridge",
		family:      "opencode",
		description: "OpenCode bridge: built-in `opencode acp` subcommand (Agent Client Protocol v1, used by Zed in production). No Claude Code plugin to install — the recipe verifies the opencode binary is on PATH.",
		upstream:    "https://github.com/sst/opencode",
		binaryName:  "opencode",
	})
	setup.Register(bridgeRecipe{
		name:        "hermes-bridge",
		family:      "hermes",
		description: "Hermes bridge: NousResearch hermes-agent — self-improving CLI agent with 47 built-in tools, 20+ inference providers (OpenRouter, Anthropic, Codex, Gemini, NIM, Bedrock, Ollama). Headless mode via `hermes chat -q`. No Claude Code plugin — recipe verifies the hermes binary is on PATH.",
		upstream:    "https://github.com/nousresearch/hermes-agent",
		binaryName:  "hermes",
	})
}
