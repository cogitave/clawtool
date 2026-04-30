package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestClawtoolAutonomousSkill_Registered(t *testing.T) {
	r := setup.Lookup("clawtool-autonomous-loop")
	if r == nil {
		t.Fatal("clawtool-autonomous-loop should self-register")
	}
	m := r.Meta()
	if m.Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q (want %q)", m.Category, setup.CategoryAgents)
	}
	if m.Stability != setup.StabilityBeta {
		t.Errorf("stability should be beta during the bake-in window: %q", m.Stability)
	}
	if !m.Core {
		t.Errorf("Core should be true — every onboarded repo can host an autonomous run")
	}
	if m.Upstream == "" {
		t.Errorf("Upstream should link to clawtool autonomous docs / source")
	}
}

func TestClawtoolAutonomousSkill_DetectAbsent(t *testing.T) {
	r := setup.Lookup("clawtool-autonomous-loop")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestClawtoolAutonomousSkill_ApplyDropsSkill(t *testing.T) {
	r := setup.Lookup("clawtool-autonomous-loop")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	path := filepath.Join(dir, ".claude", "skills", "clawtool-autonomous-loop", "SKILL.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SKILL.md after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Errorf("dropped SKILL.md lacks managed-by marker")
	}
	if !strings.HasPrefix(string(body), autonomousLoopMarker) {
		t.Errorf("marker is not the first line; got prefix %q", string(body[:min(80, len(body))]))
	}
	// Body must contain the YAML frontmatter name + tick.json contract
	// section so the dropped skill is actually informative.
	for _, want := range []string{"name: clawtool-autonomous-loop", "tick", "done", "next_steps"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("dropped SKILL.md missing expected substring %q", want)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusApplied {
		t.Errorf("post-Apply Detect should be Applied; got %q", status)
	}
}

func TestClawtoolAutonomousSkill_RefusesUnmanaged(t *testing.T) {
	r := setup.Lookup("clawtool-autonomous-loop")
	dir := t.TempDir()
	planted := filepath.Join(dir, ".claude", "skills", "clawtool-autonomous-loop", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(planted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, []byte("# operator's own playbook\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged SKILL.md")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
	// force=true overrides the refusal.
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("Apply with force should succeed over unmanaged file: %v", err)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after forced Apply: %v", err)
	}
}

func TestClawtoolAutonomousSkill_Idempotent(t *testing.T) {
	r := setup.Lookup("clawtool-autonomous-loop")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over managed file should succeed; got %v", err)
	}
	path := filepath.Join(dir, ".claude", "skills", "clawtool-autonomous-loop", "SKILL.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), autonomousLoopMarker); n != 1 {
		t.Errorf("SKILL.md has %d marker lines after re-Apply; want exactly 1", n)
	}
}
