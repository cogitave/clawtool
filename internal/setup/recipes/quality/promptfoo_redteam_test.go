package quality

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestPromptfooRedteam_Registered(t *testing.T) {
	r := setup.Lookup("promptfoo-redteam")
	if r == nil {
		t.Fatal("promptfoo-redteam should self-register")
	}
	if r.Meta().Category != setup.CategoryQuality {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestPromptfooRedteam_DetectAbsent(t *testing.T) {
	r := setup.Lookup("promptfoo-redteam")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestPromptfooRedteam_ApplyDropsConfig(t *testing.T) {
	r := setup.Lookup("promptfoo-redteam")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "promptfooconfig.yaml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	// Every clawtool agent family must surface in the providers list
	// — that's the whole point of this recipe (cover BIAM dispatch).
	for _, family := range []string{"claude", "codex", "gemini", "aider", "hermes", "opencode"} {
		if !strings.Contains(s, "clawtool send --agent "+family) {
			t.Errorf("provider for agent %q missing from generated config", family)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestPromptfooRedteam_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("promptfoo-redteam")
	dir := t.TempDir()
	target := filepath.Join(dir, "promptfooconfig.yaml")
	if err := os.WriteFile(target, []byte("# user-authored\nproviders: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged promptfooconfig.yaml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestPromptfooRedteam_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("promptfoo-redteam")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestPromptfooRedteam_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("promptfoo-redteam")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when config is missing")
	}
}
