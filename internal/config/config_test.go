package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault_EnablesAllKnownCoreTools(t *testing.T) {
	c := Default()
	for _, name := range KnownCoreTools {
		got, ok := c.CoreTools[name]
		if !ok {
			t.Errorf("Default() did not populate core_tools.%s", name)
			continue
		}
		if got.Enabled == nil || !*got.Enabled {
			t.Errorf("Default() core_tools.%s should be enabled", name)
		}
	}
	if c.Profile.Active != "default" {
		t.Errorf("Default profile.active = %q, want %q", c.Profile.Active, "default")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := Default()
	original.SetToolEnabled("Bash", false)
	original.SetToolEnabled("github-work.delete_repo", false)

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	bashOverride, ok := loaded.Tools["Bash"]
	if !ok || bashOverride.Enabled == nil || *bashOverride.Enabled {
		t.Errorf("loaded Bash override = %+v, want disabled", bashOverride)
	}

	deleteOverride, ok := loaded.Tools["github-work.delete_repo"]
	if !ok || deleteOverride.Enabled == nil || *deleteOverride.Enabled {
		t.Errorf("loaded github-work.delete_repo override = %+v, want disabled", deleteOverride)
	}
}

func TestLoadOrDefault_MissingFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.toml")

	cfg, err := LoadOrDefault(path)
	if err != nil {
		t.Fatalf("LoadOrDefault on missing file errored: %v", err)
	}
	if len(cfg.CoreTools) != len(KnownCoreTools) {
		t.Errorf("LoadOrDefault returned %d core tools, want %d (default)", len(cfg.CoreTools), len(KnownCoreTools))
	}
}

func TestIsEnabled_Precedence(t *testing.T) {
	t.Run("default is enabled", func(t *testing.T) {
		c := Config{}
		r := c.IsEnabled("Bash")
		if !r.Enabled || r.Rule != "default" {
			t.Errorf("got %+v, want enabled=true rule=default", r)
		}
	})

	t.Run("core_tools entry wins over default", func(t *testing.T) {
		f := false
		c := Config{CoreTools: map[string]CoreTool{"Bash": {Enabled: &f}}}
		r := c.IsEnabled("Bash")
		if r.Enabled || r.Rule != "core_tools.Bash" {
			t.Errorf("got %+v, want disabled rule=core_tools.Bash", r)
		}
	})

	t.Run("tools-level override wins over core_tools", func(t *testing.T) {
		f := false
		tr := true
		c := Config{
			CoreTools: map[string]CoreTool{"Bash": {Enabled: &tr}},
			Tools:     map[string]ToolOverride{"Bash": {Enabled: &f}},
		}
		r := c.IsEnabled("Bash")
		if r.Enabled {
			t.Errorf("got enabled, want disabled")
		}
		if r.Rule != "tools.Bash" {
			t.Errorf("rule = %q, want tools.Bash", r.Rule)
		}
	})

	t.Run("sourced tool selector with dot uses tools quoted form", func(t *testing.T) {
		f := false
		c := Config{Tools: map[string]ToolOverride{"github-work.delete_repo": {Enabled: &f}}}
		r := c.IsEnabled("github-work.delete_repo")
		if r.Enabled {
			t.Errorf("got enabled, want disabled")
		}
		if !strings.Contains(r.Rule, `"github-work.delete_repo"`) {
			t.Errorf("rule = %q, want it to contain quoted selector", r.Rule)
		}
	})

	t.Run("dotted selector does not collide with core_tools resolution", func(t *testing.T) {
		// A sourced tool selector starts lowercase and contains a dot;
		// it should never trigger the core_tools branch.
		c := Config{}
		r := c.IsEnabled("github-personal.create_issue")
		if !r.Enabled || r.Rule != "default" {
			t.Errorf("got %+v, want enabled rule=default", r)
		}
	})
}

func TestIsCoreToolSelector(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Bash", true},
		{"Read", true},
		{"WebFetch", true},
		{"bash", false},                         // lowercase = not core
		{"github-personal.create_issue", false}, // sourced selector
		{"github-work__delete_repo", false},     // wire form
		{"", false},
		{"Bash.foo", false}, // dotted = not core
	}
	for _, c := range cases {
		if got := isCoreToolSelector(c.in); got != c.want {
			t.Errorf("isCoreToolSelector(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestListCoreTools_StableOrder(t *testing.T) {
	c := Default()
	entries := c.ListCoreTools()
	if len(entries) != len(KnownCoreTools) {
		t.Fatalf("listed %d entries, want %d", len(entries), len(KnownCoreTools))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Selector >= entries[i].Selector {
			t.Errorf("entries not sorted: %q before %q", entries[i-1].Selector, entries[i].Selector)
		}
	}
}
