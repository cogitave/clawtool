package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestBrain_Registered(t *testing.T) {
	r := setup.Lookup("brain")
	if r == nil {
		t.Fatal("brain recipe should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryKnowledge {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set (wrap-don't-reinvent enforcement)")
	}
	// Beta until we feel the install-prompt UX is polished.
	if r.Meta().Stability != setup.StabilityBeta {
		t.Errorf("expected Stability=Beta, got %q", r.Meta().Stability)
	}
}

func TestBrain_PrereqsCoverObsidianAndPlugin(t *testing.T) {
	r := setup.Lookup("brain")
	prs := r.Prereqs()
	if len(prs) != 2 {
		t.Fatalf("expected 2 prereqs (Obsidian + plugin), got %d", len(prs))
	}
	gotObsidian, gotPlugin := false, false
	for _, p := range prs {
		if strings.Contains(strings.ToLower(p.Name), "obsidian") && !strings.Contains(strings.ToLower(p.Name), "plugin") {
			gotObsidian = true
		}
		if strings.Contains(strings.ToLower(p.Name), "claude-obsidian") {
			gotPlugin = true
		}
	}
	if !gotObsidian || !gotPlugin {
		t.Errorf("prereq names didn't cover Obsidian + plugin: %+v", prs)
	}
}

func TestBrain_PrereqsHavePlatformInstallCommands(t *testing.T) {
	r := setup.Lookup("brain")
	prs := r.Prereqs()
	for _, p := range prs {
		if len(p.Install) == 0 {
			t.Errorf("prereq %q has no Install commands; ManualHint alone leaves users stranded", p.Name)
		}
		if p.ManualHint == "" {
			t.Errorf("prereq %q has no ManualHint; users on unsupported platforms get no fallback", p.Name)
		}
	}
}

func TestBrain_DetectAbsentInEmptyDir(t *testing.T) {
	r := setup.Lookup("brain")
	dir := t.TempDir()
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	// In an empty tempdir without Obsidian/plugin, status is Absent.
	// On a host that DOES have Obsidian or the plugin installed, this
	// becomes Partial (cfg missing). Either way it must not be Applied
	// because we never wrote brain.toml.
	if status == setup.StatusApplied {
		t.Errorf("Detect should not report Applied without brain.toml; got %q", status)
	}
}

func TestBrain_ApplyWritesConfigWithVault(t *testing.T) {
	r := setup.Lookup("brain")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, setup.Options{
		"vault": "/some/vault/path",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool/brain.toml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	s := string(body)
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	if !strings.Contains(s, `vault = "/some/vault/path"`) {
		t.Errorf("vault path not written: %s", s)
	}
	if !strings.Contains(s, `plugin = "claude-obsidian"`) {
		t.Error("plugin = claude-obsidian missing")
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}
}

func TestBrain_ApplyWithoutVaultLeavesPlaceholder(t *testing.T) {
	r := setup.Lookup("brain")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".clawtool/brain.toml"))
	s := string(body)
	if strings.Contains(s, `vault = "`) {
		// If a default vault was found that's fine; otherwise the
		// placeholder comment must be present so the user knows where
		// to fill in.
		return
	}
	if !strings.Contains(s, "vault =") {
		t.Errorf("expected vault line (or commented placeholder); got %s", s)
	}
}

func TestBrain_ApplyRefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("brain")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool/brain.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored, no marker\n[brain]\nvault = \"/x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, setup.Options{"vault": "/y"}); err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged brain.toml")
	}
}

func TestBrain_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("brain")
	dir := t.TempDir()
	opts := setup.Options{"vault": "/v"}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestBrain_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("brain")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when brain.toml is absent")
	}
}

// detectObsidian + detectClaudeObsidianPlugin run against the live
// host. We can't assert their boolean result is fixed, but we can
// assert they don't panic and return a sensible struct shape.
func TestDetectObsidian_ReturnsStructWithoutPanicking(t *testing.T) {
	d := detectObsidian()
	if !d.found && d.via == "" {
		t.Error("detection.via should be populated even when not found")
	}
}

func TestWSLWindowsAppData_StableUnderRepeatedCalls(t *testing.T) {
	a, ok1 := wslWindowsAppData()
	b, ok2 := wslWindowsAppData()
	if a != b || ok1 != ok2 {
		t.Errorf("wslWindowsAppData should be deterministic; got %v(%v) and %v(%v)", a, ok1, b, ok2)
	}
}
