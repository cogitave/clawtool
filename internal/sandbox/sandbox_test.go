package sandbox

import (
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

func TestParseProfile_FullShape(t *testing.T) {
	cfg := config.SandboxConfig{
		Description: "test",
		Paths: []config.SandboxPath{
			{Path: ".", Mode: "rw"},
			{Path: "/etc/ssl", Mode: "ro"},
			{Path: "/proc", Mode: "none"},
		},
		Network: config.SandboxNetwork{
			Policy: "allowlist",
			Allow:  []string{"api.openai.com:443"},
		},
		Limits: config.SandboxLimits{
			Timeout:      "5m",
			Memory:       "1GB",
			CPUShares:    1024,
			ProcessCount: 32,
		},
		Env: config.SandboxEnv{
			Allow: []string{"PATH"},
			Deny:  []string{"AWS_*"},
		},
	}
	p, err := ParseProfile("workspace-write", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "workspace-write" {
		t.Errorf("Name wrong: %q", p.Name)
	}
	if len(p.Paths) != 3 {
		t.Fatalf("Paths len: %d", len(p.Paths))
	}
	if p.Paths[0].Mode != ModeReadWrite {
		t.Errorf("path[0] mode: %q", p.Paths[0].Mode)
	}
	if p.Network.Mode != "allowlist" {
		t.Errorf("network mode: %q", p.Network.Mode)
	}
	if p.Limits.Timeout != 5*time.Minute {
		t.Errorf("timeout: %s", p.Limits.Timeout)
	}
	if p.Limits.MemoryBytes != 1<<30 {
		t.Errorf("memory: %d", p.Limits.MemoryBytes)
	}
}

func TestParseProfile_RejectsBadMode(t *testing.T) {
	_, err := ParseProfile("x", config.SandboxConfig{
		Paths: []config.SandboxPath{{Path: ".", Mode: "bogus"}},
	})
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("expected mode error, got %v", err)
	}
}

func TestParseProfile_RejectsBadNetwork(t *testing.T) {
	_, err := ParseProfile("x", config.SandboxConfig{
		Network: config.SandboxNetwork{Policy: "everywhere"},
	})
	if err == nil || !strings.Contains(err.Error(), "network") {
		t.Fatalf("expected network error, got %v", err)
	}
}

func TestParseProfile_RejectsAllowWithoutAllowlist(t *testing.T) {
	_, err := ParseProfile("x", config.SandboxConfig{
		Network: config.SandboxNetwork{Policy: "open", Allow: []string{"x:1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("expected error about allow without allowlist, got %v", err)
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{
		"":      0,
		"512":   512,
		"512B":  512,
		"4K":    4 << 10,
		"4KB":   4 << 10,
		"1M":    1 << 20,
		"1MB":   1 << 20,
		"1G":    1 << 30,
		"1GB":   1 << 30,
		"  2g ": 2 << 30,
	}
	for in, want := range cases {
		got, err := parseBytes(in)
		if err != nil {
			t.Errorf("parseBytes(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseBytes(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSelectEngine_NoopAlwaysAvailable(t *testing.T) {
	// SelectEngine never returns nil — at minimum the noop
	// engine satisfies Available.
	e := SelectEngine()
	if e == nil {
		t.Fatal("SelectEngine returned nil")
	}
	if e.Name() == "" {
		t.Error("engine has empty name")
	}
}

func TestAvailableEngines_IncludesNoop(t *testing.T) {
	statuses := AvailableEngines()
	found := false
	for _, st := range statuses {
		if st.Name == "noop" {
			found = true
			if !st.Available {
				t.Error("noop should always be available")
			}
		}
	}
	if !found {
		t.Error("AvailableEngines missing noop")
	}
}
