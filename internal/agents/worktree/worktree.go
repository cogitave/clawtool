// Package worktree — opt-in git-worktree isolation per dispatch
// (ADR-014 T5, design from the 2026-04-26 multi-CLI fan-out).
//
// Lifecycle:
//
//  1. `clawtool send --isolated` resolves the operator's repo root.
//  2. Worktree.Manager.Create reserves
//     `~/.cache/clawtool/worktrees/{taskID}` under an advisory file
//     lock and shells out to `git worktree add --detach`.
//  3. Transport.Send dispatches the upstream agent with the worktree
//     as cwd; the agent can stage/commit freely without touching the
//     operator's working tree.
//  4. On success the cleanup func removes the worktree and prunes
//     git's bookkeeping. On failure with `--keep-on-error` the
//     worktree is left in place and `clawtool worktree show <taskID>`
//     points the operator at it.
//
// Per ADR-007 we wrap `git worktree add/remove/prune` shell-outs; we
// never reimplement git. The worktree dir gets a marker JSON so
// `clawtool worktree gc` can reap orphans whose owning process died.
package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// MarkerFilename is the JSON marker every worktree carries. GC
// inspects it to decide reapability.
const MarkerFilename = ".clawtool-worktree.json"

// Marker is the on-disk state we stamp into each worktree. PID and
// CreatedAt let GC distinguish live work from orphans.
type Marker struct {
	TaskID    string    `json:"task_id"`
	RepoRoot  string    `json:"repo_root"`
	BaseRef   string    `json:"base_ref"`
	Agent     string    `json:"agent"`
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
}

// Manager creates and disposes ephemeral git worktrees.
type Manager interface {
	// Create reserves a worktree at ~/.cache/clawtool/worktrees/{taskID},
	// shells out to git worktree add, stamps a marker, and returns the
	// workdir path plus a cleanup func. The cleanup is idempotent and
	// safe to call from multiple goroutines.
	//
	// Concurrency: holds a per-repo advisory file lock around the
	// add/remove/prune operations. Two parallel Create calls against
	// the same repo serialise creation but the workdirs (and dispatch
	// runs) execute in parallel.
	Create(ctx context.Context, repoPath, taskID, agent string) (workdir string, cleanup func(), err error)
}

type manager struct {
	cacheDir string // override for tests; default is xdgCacheDir/worktrees
	lockDir  string // override for tests; default is xdgCacheDir/locks
}

// New returns a Manager rooted at the user's XDG cache dir.
func New() Manager { return &manager{cacheDir: defaultWorktreeRoot(), lockDir: defaultLockRoot()} }

func defaultWorktreeRoot() string {
	if v := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "worktrees")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "clawtool-worktrees")
	}
	return filepath.Join(home, ".cache", "clawtool", "worktrees")
}

func defaultLockRoot() string {
	if v := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "locks")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "clawtool-locks")
	}
	return filepath.Join(home, ".cache", "clawtool", "locks")
}

func (m *manager) Create(ctx context.Context, repoPath, taskID, agent string) (string, func(), error) {
	if strings.TrimSpace(taskID) == "" {
		return "", nil, errors.New("worktree: taskID is required")
	}
	repoRoot, err := gitTopLevel(ctx, repoPath)
	if err != nil {
		return "", nil, fmt.Errorf("worktree: %w", err)
	}

	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("worktree: mkdir cache: %w", err)
	}
	if err := os.MkdirAll(m.lockDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("worktree: mkdir lockdir: %w", err)
	}

	workdir := filepath.Join(m.cacheDir, taskID)
	if _, err := os.Stat(workdir); err == nil {
		return "", nil, fmt.Errorf("worktree: %s already exists (taskID collision)", workdir)
	}

	// Advisory lock per canonical repo root: only the create / remove
	// /prune steps serialise; agents run concurrently in distinct
	// workdirs.
	lockPath := filepath.Join(m.lockDir, repoLockKey(repoRoot)+".lock")
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return "", nil, fmt.Errorf("worktree: acquire lock: %w", err)
	}

	// Capture base ref before mutating anything so the marker records it.
	baseRef, _ := gitHead(ctx, repoRoot)

	addCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", "--detach", workdir, "HEAD")
	if out, err := addCmd.CombinedOutput(); err != nil {
		_ = lock.Unlock()
		return "", nil, fmt.Errorf("worktree: git worktree add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	_ = lock.Unlock()

	marker := Marker{
		TaskID:    taskID,
		RepoRoot:  repoRoot,
		BaseRef:   baseRef,
		Agent:     agent,
		PID:       os.Getpid(),
		CreatedAt: time.Now().UTC(),
	}
	if err := writeMarker(workdir, marker); err != nil {
		// Best-effort cleanup: remove the worktree we just made.
		_ = removeWorktree(ctx, repoRoot, workdir, m.lockDir)
		return "", nil, fmt.Errorf("worktree: write marker: %w", err)
	}

	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		_ = removeWorktree(ctx, repoRoot, workdir, m.lockDir)
	}
	return workdir, cleanup, nil
}

// removeWorktree shells out to `git worktree remove --force` then
// `git worktree prune`. Idempotent: a missing worktree is a no-op.
func removeWorktree(ctx context.Context, repoRoot, workdir, lockDir string) error {
	lockPath := filepath.Join(lockDir, repoLockKey(repoRoot)+".lock")
	lock := flock.New(lockPath)
	_ = lock.Lock()
	defer lock.Unlock()

	rmCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "remove", "--force", workdir)
	_, _ = rmCmd.CombinedOutput()
	// Even if remove fails (e.g. directory already gone), force-delete
	// the directory so the marker doesn't leak.
	_ = os.RemoveAll(workdir)
	pruneCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "prune")
	_, _ = pruneCmd.CombinedOutput()
	return nil
}

// gitTopLevel resolves the git toplevel for the given path. Exported
// errors carry the underlying git stderr so the operator sees what
// went wrong (e.g. "not a git repo").
func gitTopLevel(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// gitHead returns the short SHA of HEAD; empty on error.
func gitHead(ctx context.Context, repoRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// repoLockKey is a stable filename-safe key for the canonical repo
// root path. Hashing avoids overlong / illegal filenames on weird
// repo paths.
func repoLockKey(repoRoot string) string {
	h := sha256.Sum256([]byte(filepath.Clean(repoRoot)))
	return hex.EncodeToString(h[:8])
}

// writeMarker stamps the marker JSON inside the worktree.
func writeMarker(workdir string, m Marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workdir, MarkerFilename), b, 0o644)
}

// ReadMarker decodes the marker JSON at workdir. Used by GC.
func ReadMarker(workdir string) (Marker, error) {
	var m Marker
	b, err := os.ReadFile(filepath.Join(workdir, MarkerFilename))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

// GC scans the cache root and removes worktrees whose marker PID is
// no longer live AND whose CreatedAt is older than `minAge`. Returns
// the list of reaped paths (for logging) and any non-fatal errors.
func (m *manager) GC(ctx context.Context, minAge time.Duration) ([]string, error) {
	entries, err := os.ReadDir(m.cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var reaped []string
	cutoff := time.Now().Add(-minAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.cacheDir, e.Name())
		marker, err := ReadMarker(dir)
		if err != nil {
			// No marker → not ours to reap.
			continue
		}
		if !marker.CreatedAt.Before(cutoff) {
			continue
		}
		if processAlive(marker.PID) {
			continue
		}
		_ = removeWorktree(ctx, marker.RepoRoot, dir, m.lockDir)
		reaped = append(reaped, dir)
	}
	return reaped, nil
}

// processAlive reports whether the given PID corresponds to a running
// process. On unix-likes we send signal 0; the kernel returns ESRCH
// when the process is gone. On Windows os.FindProcess + signal 0 has
// no equivalent, but the worktree GC is unix-targeted in v0.14.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscallZero()); err != nil {
		return false
	}
	return true
}

// GCManager exposes GC on the *manager type for the CLI subcommand.
// We don't add it to the Manager interface to keep the dispatch path
// minimal; gc is a maintenance command.
type GCManager interface {
	GC(ctx context.Context, minAge time.Duration) ([]string, error)
}

// AsGCManager surfaces the GC method on a Manager built by New().
// Returns nil for non-default Managers.
func AsGCManager(m Manager) GCManager {
	if mm, ok := m.(*manager); ok {
		return mm
	}
	return nil
}
