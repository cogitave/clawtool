package xdg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDir_HonoursEnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/custom-config")
	if got := ConfigDir(); got != "/tmp/custom-config/clawtool" {
		t.Errorf("ConfigDir() = %q, want /tmp/custom-config/clawtool", got)
	}
}

func TestConfigDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/operator")
	got := ConfigDir()
	want := filepath.Join("/home/operator", ".config", "clawtool")
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestStateDir_UsesLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/operator")
	got := StateDir()
	if !strings.HasSuffix(got, filepath.Join(".local", "state", "clawtool")) {
		t.Errorf("StateDir() = %q; expected to end with .local/state/clawtool", got)
	}
}

func TestDataDir_UsesLocalShare(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/operator")
	got := DataDir()
	if !strings.HasSuffix(got, filepath.Join(".local", "share", "clawtool")) {
		t.Errorf("DataDir() = %q; expected to end with .local/share/clawtool", got)
	}
}

func TestCacheDir_UsesDotCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/operator")
	got := CacheDir()
	if !strings.HasSuffix(got, filepath.Join(".cache", "clawtool")) {
		t.Errorf("CacheDir() = %q; expected to end with .cache/clawtool", got)
	}
}

func TestResolve_EmptyHomeFallsBackToCwdRelative(t *testing.T) {
	// Defensive: when both env and HOME are empty (rare — minimal
	// containers without /etc/passwd) we should still produce a
	// non-empty path, not panic. UserHomeDir returns "" + an err
	// in that scenario.
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	// Some platforms also consult USERPROFILE; clear that too.
	if old, ok := os.LookupEnv("USERPROFILE"); ok {
		t.Setenv("USERPROFILE", "")
		defer t.Setenv("USERPROFILE", old)
	}
	got := ConfigDir()
	if got == "" {
		t.Error("ConfigDir() returned empty string when env+home were both empty")
	}
	if !strings.Contains(got, "clawtool") {
		t.Errorf("ConfigDir() = %q; expected to contain 'clawtool'", got)
	}
}
