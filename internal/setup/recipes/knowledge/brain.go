// Package knowledge hosts recipes for the `knowledge` category —
// project memory and documentation tooling. The brain recipe wraps
// claude-obsidian (the Claude Code plugin + Obsidian app pair) so a
// repo can declare which Obsidian vault is its working memory; it
// does NOT reimplement the vault layer.
//
// Per ADR-013, brain is the highest-leverage knowledge recipe: most
// users find "give my AI persistent memory" the hardest thing to set
// up. clawtool's job is to detect Obsidian + the claude-obsidian
// Claude Code plugin, offer install prompts when either is missing,
// and drop a tiny `.clawtool/brain.toml` recording the vault path.
package knowledge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cogitave/clawtool/internal/setup"
)

const (
	brainConfigPath = ".clawtool/brain.toml"
	pluginID        = "claude-obsidian"
	marketplaceID   = "claude-obsidian-marketplace"
)

type brainRecipe struct{}

func (brainRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "brain",
		Category:    setup.CategoryKnowledge,
		Description: "Wire this repo to a claude-obsidian vault — drops .clawtool/brain.toml + verifies Obsidian app and the claude-obsidian Claude Code plugin are installed.",
		Upstream:    "https://github.com/AgriciDaniel/claude-obsidian",
		Stability:   setup.StabilityStable,
	}
}

// Detect inspects (a) whether .clawtool/brain.toml exists in the
// repo, (b) whether Obsidian is detected on the host, (c) whether
// the claude-obsidian plugin is installed. Status is the conjunction
// of those — Applied iff all three are true.
func (brainRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	cfgPath := filepath.Join(repo, brainConfigPath)
	cfgExists, err := setup.FileExists(cfgPath)
	if err != nil {
		return setup.StatusError, "", err
	}
	obsidian := detectObsidian()
	plugin := detectClaudeObsidianPlugin()

	switch {
	case cfgExists && obsidian.found && plugin:
		return setup.StatusApplied, fmt.Sprintf("brain.toml present · Obsidian: %s · plugin: ok", obsidian.via), nil
	case cfgExists && (!obsidian.found || !plugin):
		return setup.StatusPartial, fmt.Sprintf("brain.toml present but prereq missing — Obsidian: %v · plugin: %v", obsidian.via, plugin), nil
	case !cfgExists && (obsidian.found || plugin):
		return setup.StatusPartial, fmt.Sprintf("Obsidian: %v · plugin: %v · brain.toml not yet written", obsidian.via, plugin), nil
	default:
		return setup.StatusAbsent, "Obsidian + claude-obsidian plugin not detected; brain.toml missing", nil
	}
}

// Prereqs surfaces Obsidian and the claude-obsidian plugin so the
// wizard / runner can offer per-platform install commands.
func (brainRecipe) Prereqs() []setup.Prereq {
	return []setup.Prereq{
		{
			Name: "Obsidian (markdown knowledge app)",
			Check: func(_ context.Context) error {
				if !detectObsidian().found {
					return fmt.Errorf("Obsidian not detected")
				}
				return nil
			},
			Install: map[setup.Platform][]string{
				setup.PlatformDarwin:  {"brew", "install", "--cask", "obsidian"},
				setup.PlatformLinux:   {"flatpak", "install", "-y", "flathub", "md.obsidian.Obsidian"},
				setup.PlatformWindows: {"winget", "install", "-e", "--id", "Obsidian.Obsidian"},
			},
			ManualHint: "Install Obsidian from https://obsidian.md (universal: macOS / Linux / Windows / iPadOS / Android). On WSL, install on the Windows side and clawtool detects it automatically via /mnt/c/.../AppData.",
		},
		{
			Name: "claude-obsidian Claude Code plugin",
			Check: func(_ context.Context) error {
				if !detectClaudeObsidianPlugin() {
					return fmt.Errorf("claude-obsidian plugin not installed")
				}
				return nil
			},
			Install: map[setup.Platform][]string{
				setup.PlatformDarwin: {"sh", "-c", "claude plugin marketplace add AgriciDaniel/claude-obsidian 2>/dev/null; claude plugin install claude-obsidian@" + marketplaceID},
				setup.PlatformLinux:  {"sh", "-c", "claude plugin marketplace add AgriciDaniel/claude-obsidian 2>/dev/null; claude plugin install claude-obsidian@" + marketplaceID},
			},
			ManualHint: "Run: claude plugin marketplace add AgriciDaniel/claude-obsidian && claude plugin install claude-obsidian. Inside Obsidian, also enable the claude-obsidian community plugin so the bridge can see your vault.",
		},
	}
}

// Apply writes .clawtool/brain.toml. Vault path comes from
// Options[vault] (string); if absent and we can probe a default
// vault location we use that. Otherwise the recipe refuses with a
// clear error pointing at the option.
func (brainRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	vault, _ := setup.GetOption[string](opts, "vault")
	if strings.TrimSpace(vault) == "" {
		// Probe a default vault if Obsidian's detected. Empty
		// fallback if not — caller passes Options[vault] explicitly.
		if v := defaultVaultPath(); v != "" {
			vault = v
		}
	}

	cfgPath := filepath.Join(repo, brainConfigPath)
	existing, err := setup.ReadIfExists(cfgPath)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", brainConfigPath)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", setup.ManagedByMarker)
	b.WriteString("# Generated by `clawtool recipe apply brain`.\n")
	b.WriteString("# Edits are safe — re-running the recipe refuses to overwrite an unmanaged file.\n\n")
	b.WriteString("[brain]\n")
	if vault != "" {
		fmt.Fprintf(&b, "vault = %q\n", vault)
	} else {
		b.WriteString("# vault = \"<absolute path to your Obsidian vault>\"  # set via opts[vault] or edit here\n")
	}
	b.WriteString("plugin = \"claude-obsidian\"\n")
	return setup.WriteAtomic(cfgPath, []byte(b.String()), 0o644)
}

func (brainRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, brainConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", brainConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", brainConfigPath)
	}
	return nil
}

// ── prerequisite detection ─────────────────────────────────────────

type obsidianDetection struct {
	found bool
	via   string // path or platform-specific identifier that succeeded
}

// detectObsidian probes platform-canonical install locations + PATH
// + (on WSL) the Windows-side AppData directory. Returns the first
// match. Order matters: PATH first so explicit installs win.
func detectObsidian() obsidianDetection {
	if p, err := exec.LookPath("obsidian"); err == nil {
		return obsidianDetection{found: true, via: p}
	}

	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "linux":
		// Native Linux installs.
		for _, p := range []string{
			filepath.Join(home, ".config", "obsidian"),
			filepath.Join(home, ".var", "app", "md.obsidian.Obsidian"),
			"/snap/obsidian",
		} {
			if exists, _ := setup.FileExists(p); exists {
				return obsidianDetection{found: true, via: p}
			}
		}
		// WSL fallback: probe the Windows-side AppData. On Microsoft
		// WSL2 distros /mnt/c is mounted with the user's NTFS root.
		if p, ok := wslWindowsAppData(); ok {
			candidate := filepath.Join(p, "obsidian")
			if exists, _ := setup.FileExists(candidate); exists {
				return obsidianDetection{found: true, via: candidate + " (via WSL→Windows)"}
			}
		}
	case "darwin":
		if exists, _ := setup.FileExists("/Applications/Obsidian.app"); exists {
			return obsidianDetection{found: true, via: "/Applications/Obsidian.app"}
		}
		if exists, _ := setup.FileExists(filepath.Join(home, "Applications", "Obsidian.app")); exists {
			return obsidianDetection{found: true, via: filepath.Join(home, "Applications", "Obsidian.app")}
		}
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidate := filepath.Join(appData, "obsidian")
			if exists, _ := setup.FileExists(candidate); exists {
				return obsidianDetection{found: true, via: candidate}
			}
		}
	}
	return obsidianDetection{found: false, via: "not detected"}
}

// wslWindowsAppData returns the AppData/Roaming path of the Windows
// host user, or false if the host isn't a WSL distro that mounts
// /mnt/c. Best-effort heuristic — we look at /mnt/c/Users/* and pick
// the first dir that contains an AppData/Roaming sub-path.
func wslWindowsAppData() (string, bool) {
	if _, err := os.Stat("/mnt/c/Users"); err != nil {
		return "", false
	}
	entries, err := os.ReadDir("/mnt/c/Users")
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip system-y user dirs.
		switch strings.ToLower(e.Name()) {
		case "default", "default user", "public", "all users":
			continue
		}
		candidate := filepath.Join("/mnt/c/Users", e.Name(), "AppData", "Roaming")
		if exists, _ := setup.FileExists(candidate); exists {
			return candidate, true
		}
	}
	return "", false
}

// detectClaudeObsidianPlugin shells out to `claude plugin list` and
// scans for the plugin id. Returns false on any error or absence.
// Cheap: the command exits in <100ms typically. We deliberately don't
// parse JSON because `claude plugin list` doesn't expose a stable
// JSON output flag at this writing — we treat the human format as
// fingerprintable text.
func detectClaudeObsidianPlugin() bool {
	if _, err := exec.LookPath("claude"); err != nil {
		return false
	}
	cmd := exec.Command("claude", "plugin", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	// Match the plugin name with or without marketplace suffix so a
	// renamed marketplace doesn't break the check.
	return strings.Contains(strings.ToLower(string(out)), pluginID)
}

// defaultVaultPath returns a best-guess vault path when the user
// hasn't supplied one. We don't auto-create vaults; we just pick the
// most-likely existing one. Empty string means "no good guess —
// caller should ask the user".
func defaultVaultPath() string {
	d := detectObsidian()
	if !d.found {
		return ""
	}
	// On WSL the user's vault is almost always on the Windows side.
	if strings.Contains(d.via, "WSL→Windows") {
		// Best we can do without parsing Obsidian's own config: leave
		// the option for the user to supply explicitly.
		return ""
	}
	return ""
}

func init() { setup.Register(brainRecipe{}) }
