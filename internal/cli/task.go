package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
)

const taskUsage = `Usage:
  clawtool task list [--limit N]                 Recent BIAM tasks (default 50, max 1000).
  clawtool task get <task_id>                    Snapshot of one task + its message timeline.
  clawtool task wait <task_id> [--timeout 5m]    Block until the task hits a terminal state.
  clawtool task watch [<task_id> | --all] [--json] [--poll-interval 250ms]
                                                Stream state transitions as one stdout line per
                                                event. Pair with Claude Code's Monitor tool to
                                                surface dispatch progress as inline chat events
                                                (ADR-026). Without --all, watches a single task.
                                                With --all, watches every active dispatch in the
                                                BIAM store.

Tasks are created when you dispatch with 'clawtool send --async' or
'mcp__clawtool__SendMessage --bidi=true'. The store lives at
$XDG_DATA_HOME/clawtool/biam.db (or ~/.local/share/clawtool/biam.db).
`

func (a *App) runTask(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, taskUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		limit := 50
		for i := 1; i < len(argv); i++ {
			if argv[i] == "--limit" && i+1 < len(argv) {
				if n, err := parseIntArg(argv[i+1]); err == nil {
					limit = n
				}
				i++
			}
		}
		if err := a.TaskList(limit); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool task list: %v\n", err)
			return 1
		}
	case "get":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool task get <task_id>\n")
			return 2
		}
		if err := a.TaskGet(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool task get: %v\n", err)
			return 1
		}
	case "wait":
		if len(argv) < 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool task wait <task_id> [--timeout DUR]\n")
			return 2
		}
		taskID := argv[1]
		timeout := 5 * time.Minute
		for i := 2; i < len(argv); i++ {
			if argv[i] == "--timeout" && i+1 < len(argv) {
				d, err := time.ParseDuration(argv[i+1])
				if err != nil {
					fmt.Fprintf(a.Stderr, "invalid --timeout: %v\n", err)
					return 2
				}
				timeout = d
				i++
			}
		}
		if err := a.TaskWait(taskID, timeout); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool task wait: %v\n", err)
			return 1
		}
	case "watch":
		return a.runTaskWatch(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool task: unknown subcommand %q\n\n%s", argv[0], taskUsage)
		return 2
	}
	return 0
}

// TaskList prints the recent BIAM task summary.
func (a *App) TaskList(limit int) error {
	store, err := openBiamStore()
	if err != nil {
		return err
	}
	defer store.Close()
	tasks, err := store.ListTasks(context.Background(), limit)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Fprintln(a.Stdout, "(no tasks — submit one via `clawtool send --async ...`)")
		return nil
	}
	fmt.Fprintf(a.Stdout, "%-36s %-10s %-15s %s\n", "TASK_ID", "STATUS", "AGENT", "LAST")
	for _, t := range tasks {
		last := truncateLine(t.LastMessage, 80)
		fmt.Fprintf(a.Stdout, "%-36s %-10s %-15s %s\n", t.TaskID, t.Status, t.Agent, last)
	}
	return nil
}

// TaskGet prints the task row + every message envelope for the task,
// JSON-formatted so a script can parse it.
func (a *App) TaskGet(taskID string) error {
	store, err := openBiamStore()
	if err != nil {
		return err
	}
	defer store.Close()
	t, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("task %q not found", taskID)
	}
	msgs, _ := store.MessagesFor(context.Background(), taskID)
	out := map[string]any{"task": t, "messages": msgs}
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// TaskWait blocks until the task is terminal, then dumps the same shape TaskGet does.
func (a *App) TaskWait(taskID string, timeout time.Duration) error {
	store, err := openBiamStore()
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	t, err := store.WaitForTerminal(ctx, taskID, 250*time.Millisecond)
	if err != nil {
		return err
	}
	msgs, _ := store.MessagesFor(context.Background(), taskID)
	out := map[string]any{"task": t, "messages": msgs}
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// openBiamStore returns a fresh handle to the BIAM SQLite file. CLI
// callers don't share the server's process-wide store; SQLite WAL
// makes concurrent open / close cheap.
func openBiamStore() (*biam.Store, error) {
	return biam.OpenStore("")
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func parseIntArg(s string) (int, error) {
	var n int
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
