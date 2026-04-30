package productivity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestMattpocockSkills_Registered(t *testing.T) {
	r := setup.Lookup("mattpocock-skills")
	if r == nil {
		t.Fatal("mattpocock-skills should self-register")
	}
	if r.Meta().Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q (want %q)", r.Meta().Category, setup.CategoryAgents)
	}
	if r.Meta().Upstream != "https://github.com/mattpocock/skills" {
		t.Errorf("upstream wrong: %q", r.Meta().Upstream)
	}
	if r.Meta().Stability != setup.StabilityBeta {
		t.Errorf("stability should be beta during the bake-in window: %q", r.Meta().Stability)
	}
}

func TestMattpocockSkills_DetectAbsent(t *testing.T) {
	r := setup.Lookup("mattpocock-skills")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestMattpocockSkills_ApplyDropsAllExpectedFiles(t *testing.T) {
	r := setup.Lookup("mattpocock-skills")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, s := range shippedSkills {
		path := filepath.Join(dir, ".claude", "skills", s.name, "SKILL.md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s after Apply: %v", s.name, err)
		}
		if !setup.HasMarker(body, setup.ManagedByMarker) {
			t.Errorf("skill %q lacks managed-by marker", s.name)
		}
		// Marker MUST be the first line per task contract — the
		// upstream YAML frontmatter parser tolerates an HTML
		// comment above the `---` block.
		if !strings.HasPrefix(string(body), markerLine) {
			t.Errorf("skill %q marker is not the first line; got prefix %q", s.name, string(body[:min(80, len(body))]))
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

func TestMattpocockSkills_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("mattpocock-skills")
	dir := t.TempDir()
	// Plant an operator-authored SKILL.md for one of the shipped
	// skills — Apply must refuse the whole batch (no half-state).
	planted := filepath.Join(dir, ".claude", "skills", "diagnose", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(planted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, []byte("# operator's own diagnose playbook\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged SKILL.md")
	}
	// And no other shipped skills should have been written —
	// pre-flight refusal must be all-or-nothing.
	for _, s := range shippedSkills {
		if s.name == "diagnose" {
			continue
		}
		path := filepath.Join(dir, ".claude", "skills", s.name, "SKILL.md")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Apply leaked partial write for %q (err=%v)", s.name, err)
		}
	}
	status, msg, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q (msg=%q)", status, msg)
	}
	// force=true overrides the refusal.
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("Apply with force should succeed over unmanaged file: %v", err)
	}
}

func TestMattpocockSkills_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("mattpocock-skills")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	// Second Apply over clawtool-managed files must succeed and
	// must NOT stack marker lines on top of each other.
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over managed files should succeed; got %v", err)
	}
	for _, s := range shippedSkills {
		path := filepath.Join(dir, ".claude", "skills", s.name, "SKILL.md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", s.name, err)
		}
		if n := strings.Count(string(body), markerLine); n != 1 {
			t.Errorf("skill %q has %d marker lines after re-Apply; want exactly 1", s.name, n)
		}
	}
}

func TestMattpocockSkills_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("mattpocock-skills")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when skills are missing")
	}
}

func TestMattpocockSkills_DetectPartialOnMissingSkill(t *testing.T) {
	// One managed skill present, the rest missing → Partial.
	r := setup.Lookup("mattpocock-skills")
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "skills", "diagnose", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(markerLine+"# managed copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if status != setup.StatusPartial {
		t.Errorf("got %q, want Partial", status)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
