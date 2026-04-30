package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestShellMcp_Registered(t *testing.T) {
	r := setup.Lookup("shell-mcp")
	if r == nil {
		t.Fatal("shell-mcp should self-register")
	}
	m := r.Meta()
	if m.Category != setup.CategoryRuntime {
		t.Errorf("wrong category: %q", m.Category)
	}
	if m.Upstream == "" {
		t.Error("Upstream must be set")
	}
	if m.Stability != setup.StabilityBeta {
		t.Errorf("stability = %q, want beta", m.Stability)
	}
	if m.Core {
		t.Error("Core must be false — shell sandbox is opt-in")
	}
	if !strings.Contains(strings.ToLower(m.Description), "sandbox-aware") {
		t.Errorf("description should mention sandbox-aware shell execution; got %q", m.Description)
	}
}

func TestShellMcp_DetectAbsent(t *testing.T) {
	r := setup.Lookup("shell-mcp")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestShellMcp_ApplyDropsConfig(t *testing.T) {
	r := setup.Lookup("shell-mcp")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool/shell-mcp.toml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Allowlist starter must mirror rtk's allowlisted commands.
	for _, cmd := range []string{
		`"git **"`, `"ls **"`, `"grep **"`, `"cat **"`,
		`"head **"`, `"tail **"`, `"find **"`, `"tree **"`,
		`"diff **"`, `"stat **"`, `"wc **"`,
	} {
		if !strings.Contains(s, cmd) {
			t.Errorf("allowlist starter missing %s", cmd)
		}
	}
	// Curated denylist documentation must call out the catastrophic
	// patterns the upstream hard denylist already kills.
	for _, deny := range []string{
		"rm -rf /", ":(){ :|:& };:", "sudo", "sh -c",
	} {
		if !strings.Contains(s, deny) {
			t.Errorf("denylist excerpt missing %q", deny)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestShellMcp_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("shell-mcp")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool/shell-mcp.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\nallow = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged shell-mcp.toml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestShellMcp_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("shell-mcp")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}
