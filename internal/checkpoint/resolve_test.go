package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// commitFile stages and commits the file with msg in dir using
// `git commit -m`. Bypasses the package's Run/Autocommit so the
// resolve test can fabricate exact subjects (including non-wip
// real subjects) without going through the wip!: prepender.
func commitFile(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	writeFile(t, dir, name, body)
	if out, err := exec.Command("git", "-C", dir, "add", name).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v (%s)", name, err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", msg).CombinedOutput(); err != nil {
		t.Fatalf("git commit %q: %v (%s)", msg, err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v (%s)", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestResolve_AutosquashesWipCommits(t *testing.T) {
	dir := initWipRepo(t)
	// Capture initial commit (`feat: init`) as base.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v (%s)", err, out)
	}
	base := strings.TrimSpace(string(out))

	// Layer: real subject, then 3 wip!: checkpoints folding into it.
	commitFile(t, dir, "feature.go", "package x // v1", "feat(x): add feature")
	commitFile(t, dir, "feature.go", "package x // v2", "wip!: tweak v2")
	commitFile(t, dir, "feature.go", "package x // v3", "wip!: tweak v3")
	commitFile(t, dir, "feature.go", "package x // final", "wip!: tweak final")

	if got := commitCount(t, dir, base); got != 4 {
		t.Fatalf("pre-resolve commit count = %d, want 4", got)
	}

	// Disable rebase autostash quirks for the test environment.
	_ = exec.Command("git", "-C", dir, "config", "rebase.autostash", "false").Run()

	if err := ResolveAt(context.Background(), dir, base); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Post-resolve: 1 commit since base, subject = `feat(x): …`.
	if got := commitCount(t, dir, base); got != 1 {
		t.Errorf("post-resolve commit count = %d, want 1", got)
	}
	subj := headSubject(t, dir)
	if subj != "feat(x): add feature" {
		t.Errorf("post-resolve subject = %q, want %q", subj, "feat(x): add feature")
	}

	// Working-tree state must reflect the FINAL diff, not the v1 one
	// — the squashed fixups carry their content forward.
	body, err := os.ReadFile(dir + "/feature.go")
	if err != nil {
		t.Fatalf("read feature.go: %v", err)
	}
	if !strings.Contains(string(body), "// final") {
		t.Errorf("squashed file body lost the final wip diff: %q", string(body))
	}
}

func TestResolve_NoOpWhenNoWipCommits(t *testing.T) {
	dir := initWipRepo(t)
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	base := strings.TrimSpace(string(out))

	commitFile(t, dir, "a.go", "package a", "feat: a")
	commitFile(t, dir, "b.go", "package b", "fix: b")

	before := commitCount(t, dir, base)
	if err := ResolveAt(context.Background(), dir, base); err != nil {
		t.Fatalf("Resolve no-op: %v", err)
	}
	if got := commitCount(t, dir, base); got != before {
		t.Errorf("no-op resolve changed commit count: before=%d after=%d", before, got)
	}
}

func TestResolve_ErrorsWhenAllCommitsAreWip(t *testing.T) {
	dir := initWipRepo(t)
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	base := strings.TrimSpace(string(out))

	commitFile(t, dir, "a.go", "package a", "wip!: a")
	commitFile(t, dir, "a.go", "package a // v2", "wip!: a v2")

	err := ResolveAt(context.Background(), dir, base)
	if err == nil {
		t.Fatal("expected error when every commit since base is wip!:")
	}
	if !strings.Contains(err.Error(), "every commit since") {
		t.Errorf("error message should explain the all-wip situation, got: %v", err)
	}
}

func TestResolve_RequiresBaseRef(t *testing.T) {
	dir := initWipRepo(t)
	if err := ResolveAt(context.Background(), dir, ""); err == nil {
		t.Error("expected error on empty base, got nil")
	}
}
