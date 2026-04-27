package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents/worktree"
)

const worktreeUsage = `Usage:
  clawtool worktree list                    List all isolated worktrees with marker info.
  clawtool worktree show <taskID>           Print worktree path + marker JSON.
  clawtool worktree gc [--min-age 24h]      Reap orphan worktrees whose owning PID is gone.
`

// runWorktree dispatches the `clawtool worktree` subcommands.
func (a *App) runWorktree(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, worktreeUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		if err := a.WorktreeList(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool worktree list: %v\n", err)
			return 1
		}
	case "show":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool worktree show <taskID>\n")
			return 2
		}
		if err := a.WorktreeShow(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool worktree show: %v\n", err)
			return 1
		}
	case "gc":
		minAge := 24 * time.Hour
		for i := 1; i < len(argv); i++ {
			switch argv[i] {
			case "--min-age":
				if i+1 >= len(argv) {
					fmt.Fprint(a.Stderr, "--min-age requires a duration (e.g. 24h)\n")
					return 2
				}
				d, err := time.ParseDuration(argv[i+1])
				if err != nil {
					fmt.Fprintf(a.Stderr, "invalid --min-age: %v\n", err)
					return 2
				}
				minAge = d
				i++
			default:
				fmt.Fprintf(a.Stderr, "unknown flag %q\n", argv[i])
				return 2
			}
		}
		if err := a.WorktreeGC(minAge); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool worktree gc: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool worktree: unknown subcommand %q\n\n%s", argv[0], worktreeUsage)
		return 2
	}
	return 0
}

// WorktreeList prints every worktree under ~/.cache/clawtool/worktrees
// with its marker info. Useful before running gc to see what's
// reapable.
func (a *App) WorktreeList() error {
	root := worktreeRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(a.Stdout, "(no worktrees)")
			return nil
		}
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	w := a.Stdout
	if len(entries) == 0 {
		fmt.Fprintln(w, "(no worktrees)")
		return nil
	}
	fmt.Fprintf(w, "%-32s %-10s %-30s %s\n", "TASK_ID", "AGENT", "REPO_ROOT", "AGE")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		marker, err := worktree.ReadMarker(filepath.Join(root, e.Name()))
		if err != nil {
			fmt.Fprintf(w, "%-32s %-10s %-30s (no marker)\n", e.Name(), "?", "?")
			continue
		}
		age := time.Since(marker.CreatedAt).Round(time.Second)
		fmt.Fprintf(w, "%-32s %-10s %-30s %s\n", marker.TaskID, marker.Agent, marker.RepoRoot, age)
	}
	return nil
}

// WorktreeShow dumps the marker JSON for one worktree.
func (a *App) WorktreeShow(taskID string) error {
	dir := filepath.Join(worktreeRoot(), taskID)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("worktree %q not found at %s", taskID, dir)
	}
	marker, err := worktree.ReadMarker(dir)
	if err != nil {
		return fmt.Errorf("read marker: %w", err)
	}
	fmt.Fprintf(a.Stdout, "path: %s\n\n", dir)
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(marker)
}

// WorktreeGC reaps orphans (dead PID + minAge cutoff).
func (a *App) WorktreeGC(minAge time.Duration) error {
	mgr := worktree.New()
	gc := worktree.AsGCManager(mgr)
	if gc == nil {
		return fmt.Errorf("worktree manager does not support GC")
	}
	reaped, err := gc.GC(context.Background(), minAge)
	if err != nil {
		return err
	}
	if len(reaped) == 0 {
		fmt.Fprintln(a.Stdout, "(no orphans to reap)")
		return nil
	}
	for _, p := range reaped {
		fmt.Fprintf(a.Stdout, "✓ reaped %s\n", p)
	}
	return nil
}

// worktreeRoot mirrors worktree.defaultWorktreeRoot — kept local so we
// don't have to export it from the package.
func worktreeRoot() string {
	if v := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "worktrees")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "clawtool-worktrees")
	}
	return filepath.Join(home, ".cache", "clawtool", "worktrees")
}
