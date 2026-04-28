package config

import (
	"os"
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

// TestLoad_TelemetryUpgradeMergesDefaultOn covers the v0.22.19+
// upgrade path: a config.toml that exists but omits `[telemetry]
// enabled` should NOT silently flip the user to off (zero-value
// of bool). Pre-fix, Load() returned Enabled=false here, which
// contradicted Default() and the wizard's "pre-1.0 default = on"
// claim. The fix: mergeDefaults patches absent telemetry-enabled
// keys with Default()'s value.
func TestLoad_TelemetryUpgradeMergesDefaultOn(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want bool
	}{
		{
			name: "omitted entirely → default on",
			toml: "profile = { active = \"default\" }\n",
			want: true,
		},
		{
			name: "section present but enabled key absent → default on",
			toml: "[telemetry]\napi_key = \"x\"\n",
			want: true,
		},
		{
			name: "explicit enabled = false → respected",
			toml: "[telemetry]\nenabled = false\n",
			want: false,
		},
		{
			name: "explicit enabled = true → respected",
			toml: "[telemetry]\nenabled = true\n",
			want: true,
		},
		{
			name: "comment-only between section and key → still treated as absent",
			toml: "[telemetry]\n# enabled = true (commented out)\napi_key = \"x\"\n",
			want: true,
		},
	}
	for _, c := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		if err := os.WriteFile(path, []byte(c.toml), 0o644); err != nil {
			t.Fatalf("%s: write: %v", c.name, err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("%s: load: %v", c.name, err)
		}
		if cfg.Telemetry.Enabled != c.want {
			t.Errorf("%s: Telemetry.Enabled = %v, want %v", c.name, cfg.Telemetry.Enabled, c.want)
		}
	}
}

// TestHasTelemetryEnabledKey_Direct unit-tests the string scanner
// independently of Load() so future TOML grammar surprises
// (whitespace variants, inline tables) get caught at the helper
// boundary, not via the higher-level Load round-trip.
func TestHasTelemetryEnabledKey_Direct(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"[telemetry]\n", false},
		{"[telemetry]\nenabled = true\n", true},
		{"[telemetry]\nenabled=false\n", true},
		{"[telemetry]\n  enabled = true\n", true},
		{"[telemetry]\n# enabled = true\n", false},
		{"[other]\nenabled = true\n", false},
		{"[telemetry]\napi_key = \"x\"\n[other]\nenabled = false\n", false},
	}
	for _, c := range cases {
		if got := hasTelemetryEnabledKey([]byte(c.raw)); got != c.want {
			t.Errorf("hasTelemetryEnabledKey(%q) = %v, want %v", c.raw, got, c.want)
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
