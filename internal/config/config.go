// Package config reads, writes, and resolves the clawtool configuration.
//
// Schema mirrors ADR-006: core_tools, sources, tools (per-selector overrides),
// tags, groups, profile. v0.2 implements parsing of the full schema and the
// tool-level + server-level enabled resolver. Tag- and group-level resolution
// land in v0.3 once a source instance has actually been wired so we have
// real tools to tag.
//
// Path resolution honors $XDG_CONFIG_HOME, falling back to ~/.config/clawtool.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the full on-disk shape of ~/.config/clawtool/config.toml.
type Config struct {
	CoreTools map[string]CoreTool        `toml:"core_tools,omitempty"`
	Sources   map[string]Source          `toml:"sources,omitempty"`
	Tools     map[string]ToolOverride    `toml:"tools,omitempty"`
	Tags      map[string]TagRule         `toml:"tags,omitempty"`
	Groups    map[string]GroupDef        `toml:"groups,omitempty"`
	Profile   ProfileConfig              `toml:"profile,omitempty"`
	Agents    map[string]AgentConfig     `toml:"agents,omitempty"`
	Bridges   map[string]BridgeOverrides `toml:"bridge,omitempty"`
}

// AgentConfig declares one runtime agent instance per ADR-006 instance
// scoping. Multiple instances of the same family (claude-personal,
// claude-work, codex1, …) get separate auth scopes and HOME overrides.
// Per ADR-014, the supervisor reads this map plus installed bridges
// to compose its agent registry.
type AgentConfig struct {
	Family       string `toml:"family"`                  // CLI family ("claude", "codex", "opencode", "gemini")
	SecretsScope string `toml:"secrets_scope,omitempty"` // [secrets.X] section to resolve env from; defaults to instance name
	HomeOverride string `toml:"home,omitempty"`          // optional HOME override (e.g. "~/.claude-personal") so each instance has its own auth dir
}

// BridgeOverrides lets a power user point a bridge family at a
// non-canonical plugin (e.g. internal mirror, fork). Per ADR-014's
// "no install-time plugin shopping on the CLI" rule this is the
// only override surface; the CLI exposes no `--plugin` flag.
type BridgeOverrides struct {
	Plugin string `toml:"plugin,omitempty"` // org/repo of the plugin to install instead of the default
}

// CoreTool toggles a clawtool-shipped tool. Default (missing entry) = enabled.
type CoreTool struct {
	Enabled *bool `toml:"enabled,omitempty"`
}

// Source defines a sourced MCP server instance. v0.2 stores the spec but
// does not yet spawn it; instance spawning lands when source instances ship.
type Source struct {
	Type    string            `toml:"type"`              // currently only "mcp"
	Command []string          `toml:"command,omitempty"` // argv to spawn the MCP server
	Env     map[string]string `toml:"env,omitempty"`     // env vars (`${VAR}` expansion at use)
}

// ToolOverride is a per-selector explicit enable/disable. Pointer so absence
// is distinguishable from `false`.
type ToolOverride struct {
	Enabled *bool `toml:"enabled,omitempty"`
}

// TagRule applies an enable/disable across every tool whose selector matches
// any pattern in `match` (glob, evaluated against the selector form).
type TagRule struct {
	Match    []string `toml:"match,omitempty"`
	Disabled bool     `toml:"disabled,omitempty"`
	Enabled  bool     `toml:"enabled,omitempty"`
}

// GroupDef bundles selectors. Toggling a group toggles every member.
type GroupDef struct {
	Include []string `toml:"include,omitempty"`
}

// ProfileConfig selects the active profile. Profiles themselves layer on top
// of the same shape; v0.2 ships a single profile as part of the file.
type ProfileConfig struct {
	Active string `toml:"active,omitempty"`
}

// DefaultPath returns the path the config should live at on this machine.
//
// Honors $XDG_CONFIG_HOME, then $HOME/.config/clawtool/config.toml. If neither
// resolves we return a relative path so callers fail predictably with a
// recognizable error rather than reading from "/".
func DefaultPath() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "clawtool", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "clawtool", "config.toml")
}

// Default returns a Config preloaded with every known core tool enabled.
// Used by `clawtool init` to write a sensible starting point.
func Default() Config {
	enabled := true
	tools := map[string]CoreTool{}
	for _, name := range KnownCoreTools {
		tools[name] = CoreTool{Enabled: &enabled}
	}
	return Config{
		CoreTools: tools,
		Profile:   ProfileConfig{Active: "default"},
	}
}

// KnownCoreTools is the compile-time list of core tools clawtool ships.
// Adding a tool here makes it appear in `clawtool init` output and
// `clawtool tools list`.
var KnownCoreTools = []string{
	"Bash",
	"Edit",
	"Glob",
	"Grep",
	"Read",
	"ToolSearch",
	"WebFetch",
	"WebSearch",
	"Write",
}

// Load reads and parses a config file. Returns os.ErrNotExist (wrapped) when
// the file is absent so callers can distinguish "no config" from a parse error.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// LoadOrDefault returns Load if the file exists, or Default() with no error
// when the file is missing. Used by `serve` so a fresh user can run without
// running `init` first.
func LoadOrDefault(path string) (Config, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	return Config{}, err
}

// Save writes the config to path, creating parent directories. File mode
// is 0600 because env values may carry secrets.
func (c Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Resolution holds the result of resolving an enable/disable check.
type Resolution struct {
	Enabled bool
	Rule    string // "tools.<sel>", "core_tools.<name>", "default"
}

// IsEnabled answers "should clawtool expose this tool?" for the given
// selector (CLI-form, e.g. "Bash" or "github-personal.create_issue").
//
// Precedence per ADR-004 — v0.2 implements tool > server only:
//
//  1. tools."<selector>".enabled (per-tool explicit override)
//  2. core_tools.<Name>.enabled (only for core tools, where selector is the bare PascalCase name)
//  3. default = true
//
// Tag and group precedence land in v0.3.
func (c Config) IsEnabled(selector string) Resolution {
	if override, ok := c.Tools[selector]; ok && override.Enabled != nil {
		return Resolution{Enabled: *override.Enabled, Rule: "tools." + quoteIfNeeded(selector)}
	}
	if isCoreToolSelector(selector) {
		if t, ok := c.CoreTools[selector]; ok && t.Enabled != nil {
			return Resolution{Enabled: *t.Enabled, Rule: "core_tools." + selector}
		}
	}
	return Resolution{Enabled: true, Rule: "default"}
}

// SetToolEnabled writes (or creates) an explicit per-tool override for a
// selector. Used by `clawtool tools enable / disable`.
func (c *Config) SetToolEnabled(selector string, enabled bool) {
	if c.Tools == nil {
		c.Tools = map[string]ToolOverride{}
	}
	c.Tools[selector] = ToolOverride{Enabled: &enabled}
}

// ListCoreTools returns the alphabetic list of known core tools paired with
// their resolved enabled state.
func (c Config) ListCoreTools() []ToolListEntry {
	out := make([]ToolListEntry, 0, len(KnownCoreTools))
	for _, name := range KnownCoreTools {
		out = append(out, ToolListEntry{
			Selector:   name,
			Resolution: c.IsEnabled(name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Selector < out[j].Selector })
	return out
}

// ToolListEntry is one row of `clawtool tools list`.
type ToolListEntry struct {
	Selector   string
	Resolution Resolution
}

// isCoreToolSelector returns true if selector names a clawtool core tool.
// Per ADR-006, core tools are PascalCase and contain no `__`.
func isCoreToolSelector(selector string) bool {
	if selector == "" || strings.Contains(selector, "__") || strings.Contains(selector, ".") {
		return false
	}
	c := selector[0]
	return c >= 'A' && c <= 'Z'
}

// quoteIfNeeded returns the selector wrapped in quotes when it contains a
// dot, so the rule string read back by humans matches the on-disk TOML form
// (`tools."github-personal.create_issue"`).
func quoteIfNeeded(selector string) string {
	if strings.Contains(selector, ".") {
		return `"` + selector + `"`
	}
	return selector
}
