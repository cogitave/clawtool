package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// initRepo creates a tiny git repo with one initial commit so
// `git worktree add HEAD` has something to detach from. Skips the
// test when git isn't installed (CI without git would fail noisily
// otherwise — better to skip than misreport).
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=clawtool-test", "-c", "user.email=t@t.t", "config", "user.email", "t@t.t"},
		{"-c", "user.name=clawtool-test", "config", "user.name", "clawtool-test"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	return dir
}

// newTestManager points cacheDir + lockDir at t.TempDir so tests
// don't pollute the user's real ~/.cache.
func newTestManager(t *testing.T) *manager {
	t.Helper()
	root := t.TempDir()
	return &manager{
		cacheDir: filepath.Join(root, "worktrees"),
		lockDir:  filepath.Join(root, "locks"),
	}
}

func TestCreate_AndCleanup(t *testing.T) {
	repo := initRepo(t)
	mgr := newTestManager(t)
	workdir, cleanup, err := mgr.Create(context.Background(), repo, "task-1", "codex")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	// Marker should be present. macOS resolves /var → /private/var via
	// symlink; resolve both sides before comparing so the test runs
	// on Darwin and Linux without flapping.
	marker, err := ReadMarker(workdir)
	if err != nil {
		t.Fatalf("ReadMarker: %v", err)
	}
	wantRepo, _ := filepath.EvalSymlinks(repo)
	gotRepo, _ := filepath.EvalSymlinks(marker.RepoRoot)
	if marker.TaskID != "task-1" || marker.Agent != "codex" || gotRepo != wantRepo {
		t.Errorf("marker mismatch: %+v (want repo=%s)", marker, wantRepo)
	}
	if marker.PID != os.Getpid() {
		t.Errorf("marker PID: got %d, want %d", marker.PID, os.Getpid())
	}

	cleanup()
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("cleanup should remove worktree; got err=%v", err)
	}
	// Idempotent.
	cleanup()
}

func TestCreate_ParallelSafe(t *testing.T) {
	repo := initRepo(t)
	mgr := newTestManager(t)

	var wg sync.WaitGroup
	cleanups := make([]func(), 5)
	dirs := make([]string, 5)
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, c, err := mgr.Create(context.Background(), repo, "task-parallel-"+string(rune('a'+i)), "codex")
			dirs[i], cleanups[i], errs[i] = d, c, err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		if errs[i] != nil {
			t.Errorf("parallel Create %d: %v", i, errs[i])
			continue
		}
		if seen[dirs[i]] {
			t.Errorf("duplicate workdir %q", dirs[i])
		}
		seen[dirs[i]] = true
	}
	for _, c := range cleanups {
		if c != nil {
			c()
		}
	}
}

func TestGC_ReapsOrphan(t *testing.T) {
	repo := initRepo(t)
	mgr := newTestManager(t)

	workdir, _, err := mgr.Create(context.Background(), repo, "orphan-task", "codex")
	if err != nil {
		t.Fatal(err)
	}

	// Re-stamp the marker with a dead PID and an old CreatedAt.
	marker, _ := ReadMarker(workdir)
	marker.PID = 1 // PID 1 is alive on every unix; we want a "definitely dead" PID
	marker.PID = 999_999_999
	marker.CreatedAt = time.Now().Add(-48 * time.Hour)
	if err := writeMarker(workdir, marker); err != nil {
		t.Fatal(err)
	}

	reaped, err := mgr.GC(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 1 || reaped[0] != workdir {
		t.Errorf("expected to reap %q; got %v", workdir, reaped)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("GC should remove the orphan dir; stat err=%v", err)
	}
}

func TestGC_SkipsLiveProcess(t *testing.T) {
	repo := initRepo(t)
	mgr := newTestManager(t)

	workdir, cleanup, err := mgr.Create(context.Background(), repo, "live-task", "codex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	// Marker has our PID + a recent CreatedAt; GC should leave it.
	marker, _ := ReadMarker(workdir)
	marker.CreatedAt = time.Now().Add(-48 * time.Hour) // old enough for the cutoff
	if err := writeMarker(workdir, marker); err != nil {
		t.Fatal(err)
	}

	reaped, err := mgr.GC(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 0 {
		t.Errorf("GC should skip live PIDs; reaped %v", reaped)
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("live worktree should still exist; got err=%v", err)
	}
}

func TestRepoLockKey_Stable(t *testing.T) {
	a := repoLockKey("/some/repo")
	b := repoLockKey("/some/repo")
	if a != b {
		t.Errorf("repoLockKey should be deterministic; got %q vs %q", a, b)
	}
	c := repoLockKey("/different/repo")
	if a == c {
		t.Errorf("repoLockKey should differ across paths; got %q both", a)
	}
}
