package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestResolve_MultipleAnchors exercises the harder shape the
// new pure-Go implementation supports: more than one non-wip
// commit, each with its own trailing wip group. The original
// rebase-based code handled this implicitly via autosquash; the
// pure-Go replacement has explicit groupForFold logic, so it
// deserves a dedicated regression case.
func TestResolve_MultipleAnchors(t *testing.T) {
	dir := initWipRepo(t)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v (%s)", err, out)
	}
	base := strings.TrimSpace(string(out))

	// Layer:
	//   anchor1: feat(a) — followed by 1 wip
	//   anchor2: feat(b) — followed by 2 wips
	commitFile(t, dir, "a.go", "package a // v1", "feat(a): first")
	commitFile(t, dir, "a.go", "package a // a-final", "wip!: tweak a")
	commitFile(t, dir, "b.go", "package b // v1", "feat(b): second")
	commitFile(t, dir, "b.go", "package b // v2", "wip!: tweak b1")
	commitFile(t, dir, "b.go", "package b // b-final", "wip!: tweak b2")

	if got := commitCount(t, dir, base); got != 5 {
		t.Fatalf("pre-resolve commit count = %d, want 5", got)
	}

	if err := ResolveAt(context.Background(), dir, base); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Post-resolve: 2 anchors survive, wips folded in.
	if got := commitCount(t, dir, base); got != 2 {
		t.Errorf("post-resolve commit count = %d, want 2", got)
	}

	// Verify both files reflect the FINAL wip diff (not the
	// anchor's first version).
	bodyA, err := os.ReadFile(dir + "/a.go")
	if err != nil {
		t.Fatalf("read a.go: %v", err)
	}
	if !strings.Contains(string(bodyA), "// a-final") {
		t.Errorf("a.go did not retain final wip diff: %q", string(bodyA))
	}
	bodyB, err := os.ReadFile(dir + "/b.go")
	if err != nil {
		t.Fatalf("read b.go: %v", err)
	}
	if !strings.Contains(string(bodyB), "// b-final") {
		t.Errorf("b.go did not retain final wip diff: %q", string(bodyB))
	}

	// Verify the surviving subjects, oldest-first.
	logOut, err := exec.Command("git", "-C", dir, "log", "--reverse", "--format=%s", base+"..HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v (%s)", err, logOut)
	}
	got := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	want := []string{"feat(a): first", "feat(b): second"}
	if len(got) != len(want) {
		t.Fatalf("subjects = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("subject[%d] = %q, want %q", i, got[i], w)
		}
	}
}
