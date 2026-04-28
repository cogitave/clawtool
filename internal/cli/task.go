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
  clawtool task list [--active|--all|--status S] [--limit N]
                                                Recent BIAM tasks. Default = --active (live
                                                only: pending + active). --all shows everything,
                                                including terminal rows. --status filters to a
                                                single state (done | failed | cancelled | expired).
                                                Limit defaults to 50; raise with --limit (max 1000).
  clawtool task get <task_id>                    Snapshot of one task + its message timeline.
  clawtool task wait <task_id> [--timeout 5m]    Block until the task hits a terminal state.
  clawtool task watch [<task_id> | --all] [--json] [--poll-interval 250ms]
                                                Stream state transitions as one stdout line per
                                                event. Pair with Claude Code's Monitor tool to
                                                surface dispatch progress as inline chat events.
                                                Without --all, watches a single task. With --all,
                                                watches every active dispatch in the BIAM store.
  clawtool task cancel <task_id>                Flip a pending/active task to "cancelled" and
                                                propagate the signal to the in-flight dispatch
                                                goroutine. Idempotent — a terminal task is a
                                                no-op.

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
		// Default = active-only so the eye lands on live work
		// even when the store has thousands of historical
		// terminal rows. --all opens the floodgates; --status
		// filters to a single state.
		limit := 50
		filter := taskFilterActive
		statusOverride := ""
		for i := 1; i < len(argv); i++ {
			switch argv[i] {
			case "--limit":
				if i+1 < len(argv) {
					if n, err := parseIntArg(argv[i+1]); err == nil {
						limit = n
					}
					i++
				}
			case "--active":
				filter = taskFilterActive
			case "--all":
				filter = taskFilterAll
			case "--status":
				if i+1 < len(argv) {
					filter = taskFilterStatus
					statusOverride = strings.ToLower(strings.TrimSpace(argv[i+1]))
					i++
				}
			}
		}
		if err := a.TaskList(limit, filter, statusOverride); err != nil {
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
	case "cancel":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool task cancel <task_id>\n")
			return 2
		}
		if err := a.TaskCancel(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool task cancel: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool task: unknown subcommand %q\n\n%s", argv[0], taskUsage)
		return 2
	}
	return 0
}

// taskFilter selects which subset of the BIAM store rows
// `clawtool task list` renders. Default is taskFilterActive — the
// operator's "I want to see what's running RIGHT NOW" view; the
// store may have thousands of historical terminal rows that we
// don't dump on every invocation.
type taskFilter int

const (
	taskFilterActive taskFilter = iota
	taskFilterAll
	taskFilterStatus
)

// TaskList prints the recent BIAM task summary, filtered by
// `filter`. When filter == taskFilterStatus, `statusOverride`
// names the single status to keep (done | failed | cancelled |
// expired). To honour the operator-supplied --limit while still
// filtering meaningfully, we pull a wider window from the store
// (10× limit, capped at 1000) and slice client-side.
func (a *App) TaskList(limit int, filter taskFilter, statusOverride string) error {
	store, err := openBiamStore()
	if err != nil {
		return err
	}
	defer store.Close()

	pull := limit * 10
	if pull < 200 {
		pull = 200
	}
	if pull > 1000 {
		pull = 1000
	}
	tasks, err := store.ListTasks(context.Background(), pull)
	if err != nil {
		return err
	}

	out := make([]biam.Task, 0, len(tasks))
	for _, t := range tasks {
		switch filter {
		case taskFilterActive:
			if !t.Status.IsTerminal() {
				out = append(out, t)
			}
		case taskFilterStatus:
			if string(t.Status) == statusOverride {
				out = append(out, t)
			}
		default:
			out = append(out, t)
		}
		if len(out) >= limit {
			break
		}
	}

	if len(out) == 0 {
		switch filter {
		case taskFilterActive:
			fmt.Fprintln(a.Stdout, "(no live tasks — pass --all to see history, or run `clawtool send --async ...`)")
		case taskFilterStatus:
			fmt.Fprintf(a.Stdout, "(no tasks with status %q — pass --all to see every status)\n", statusOverride)
		default:
			fmt.Fprintln(a.Stdout, "(no tasks — submit one via `clawtool send --async ...`)")
		}
		return nil
	}

	header := "Tasks"
	switch filter {
	case taskFilterActive:
		header = fmt.Sprintf("Live tasks (%d shown)", len(out))
	case taskFilterStatus:
		header = fmt.Sprintf("Tasks (%s, %d shown)", statusOverride, len(out))
	default:
		header = fmt.Sprintf("Tasks (%d shown of %d in store window)", len(out), len(tasks))
	}
	fmt.Fprintln(a.Stdout, header)
	fmt.Fprintf(a.Stdout, "%-36s %-10s %-15s %s\n", "TASK_ID", "STATUS", "AGENT", "LAST")
	for _, t := range out {
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

// TaskCancel flips a pending/active task to "cancelled". The CLI
// invocation is a separate process from the runner that owns the
// dispatch goroutine, so we do a store-only flip + Notifier publish
// here — the runner side handles in-process cancel via Runner.Cancel
// when the same caller already holds it. Cross-process pollers
// (`clawtool task watch`) wake on the Notifier broadcast.
//
// Audit fix #204: pairs with Runner.Cancel — without this the CLI
// had no way to abort a runaway --async dispatch short of kill -9 on
// the binary.
func (a *App) TaskCancel(taskID string) error {
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
	if t.Status == biam.TaskDone || t.Status == biam.TaskFailed ||
		t.Status == biam.TaskCancelled || t.Status == biam.TaskExpired {
		fmt.Fprintf(a.Stdout, "task %s already terminal (status=%s)\n", taskID, t.Status)
		return nil
	}
	if err := store.SetTaskStatus(context.Background(), taskID, biam.TaskCancelled, "cancelled by operator"); err != nil {
		return err
	}
	biam.Notifier.Publish(biam.Task{TaskID: taskID, Status: biam.TaskCancelled, Agent: t.Agent})
	fmt.Fprintf(a.Stdout, "✓ cancelled task %s\n", taskID)
	return nil
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
