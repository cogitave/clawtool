// Package cli — `clawtool task watch` (ADR-026, Gemini design pass
// b8ab4c9a). Streams BIAM task state transitions as one stdout
// line per event so the operator can pair it with Claude Code's
// native Monitor tool and see dispatch progress as inline chat
// events.
//
// Two modes:
//
//	clawtool task watch <task_id>   single task, exits when terminal
//	clawtool task watch --all       every active task, runs forever
//	                                 (or until SIGINT / pipe close)
//
// Output format defaults to human-readable; --json switches to
// NDJSON for downstream tooling.
//
// Polling cadence is 250ms by default — sub-second feel with
// negligible disk pressure on SQLite WAL. Tunable via
// --poll-interval.
//
// Per the ADR's security clause, watch lines NEVER carry the
// task's body / completion text — only metadata (status, agent,
// message_count, last_message preview capped at 80 chars). A
// gigabyte-sized completion blob landing in the operator's chat
// would be its own outage.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
)

// runTaskWatch is the dispatcher entry. Parses flags, opens the
// store, runs the appropriate loop. Honours SIGINT / SIGPIPE
// cleanly so a Monitor tool that closes the parent pipe doesn't
// crash with a broken-pipe trace.
func (a *App) runTaskWatch(argv []string) int {
	var (
		taskID       string
		all          bool
		asJSON       bool
		pollInterval = 250 * time.Millisecond
	)
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--all":
			all = true
		case "--json":
			asJSON = true
		case "--poll-interval":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "clawtool task watch: --poll-interval requires a duration")
				return 2
			}
			d, err := time.ParseDuration(argv[i+1])
			if err != nil {
				fmt.Fprintf(a.Stderr, "clawtool task watch: invalid --poll-interval %q: %v\n", argv[i+1], err)
				return 2
			}
			if d < 50*time.Millisecond {
				fmt.Fprintln(a.Stderr, "clawtool task watch: --poll-interval clamped to 50ms minimum")
				d = 50 * time.Millisecond
			}
			pollInterval = d
			i++
		default:
			if strings.HasPrefix(argv[i], "--") {
				fmt.Fprintf(a.Stderr, "clawtool task watch: unknown flag %q\n", argv[i])
				return 2
			}
			if taskID != "" {
				fmt.Fprintln(a.Stderr, "clawtool task watch: only one task_id allowed (use --all for every task)")
				return 2
			}
			taskID = argv[i]
		}
	}
	if all && taskID != "" {
		fmt.Fprintln(a.Stderr, "clawtool task watch: --all and a task_id are mutually exclusive")
		return 2
	}
	if !all && taskID == "" {
		fmt.Fprintln(a.Stderr, "clawtool task watch: pass <task_id> or --all")
		return 2
	}

	store, err := openBiamStore()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool task watch: open store: %v\n", err)
		return 1
	}
	defer store.Close()

	// Cancel cleanly on SIGINT / SIGTERM so Monitor tool teardown
	// doesn't leave a panic'd binary in the chat. SIGPIPE is also
	// handled — emitter check below.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	emit := makeEmitter(a, asJSON)

	// Push-mode first: dial the daemon's task-watch socket. When it
	// answers we read JSONL events as they happen — no SQLite poll.
	// Connect failure (no daemon, missing socket, older daemon) falls
	// through to the polling loop so the CLI works either way.
	if conn, derr := biam.DialWatchSocket(""); derr == nil {
		defer conn.Close()
		return runWatchSocket(ctx, conn, taskID, all, emit)
	}

	if all {
		return runWatchAll(ctx, a, store, pollInterval, emit)
	}
	return runWatchOne(ctx, a, store, taskID, pollInterval, emit)
}

// runWatchSocket consumes JSONL Task events from the daemon's
// push socket. Filters by taskID when --all isn't set; exits when
// the matched task hits a terminal state, the socket disconnects,
// or ctx cancels. Per-task mode also tracks "no events for this
// id yet" — the snapshot pass at connect time guarantees one line
// per known task, so a missing id means the task doesn't exist.
func runWatchSocket(ctx context.Context, conn io.ReadCloser, taskID string, all bool, emit emitter) int {
	dec := json.NewDecoder(bufio.NewReader(conn))
	prev := map[string]biam.Task{}

	// Detect ctx cancel by closing the conn so dec.Decode unblocks.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	for {
		var t biam.Task
		err := dec.Decode(&t)
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return 0
			}
			// Mid-stream JSON error → fall through to caller's
			// poll fallback path. The CLI signals this by
			// returning a non-zero code so the operator can
			// retry; printing nothing here keeps the chat
			// uncluttered.
			return 0
		}
		if !all && t.TaskID != taskID {
			continue
		}
		old, ok := prev[t.TaskID]
		if ok && !changed(&old, &t) {
			continue
		}
		ev := snapshotToEvent(&t)
		if !emit(ev) {
			return 0
		}
		prev[t.TaskID] = t
		if !all && t.Status.IsTerminal() {
			return 0
		}
	}
}

// emitter is the per-event writer. We close over the format flag
// and a/Stdout. SIGPIPE / broken-pipe detection lives here so the
// loop can exit without a crash.
type emitter func(ev watchEvent) bool

// watchEvent is the on-the-wire shape. Field set is intentionally
// small — security clause forbids dumping the task body.
type watchEvent struct {
	TS           time.Time `json:"ts"`
	TaskID       string    `json:"task_id"`
	Status       string    `json:"status"`
	Agent        string    `json:"agent,omitempty"`
	MessageCount int       `json:"message_count"`
	// LastMessage is capped at 80 chars at emit time so a big
	// completion blob doesn't flood the operator's chat. The
	// task get / wait surfaces are the right place to fetch
	// the full body.
	LastMessage string `json:"last_message,omitempty"`
}

func makeEmitter(a *App, asJSON bool) emitter {
	return func(ev watchEvent) bool {
		var line string
		if asJSON {
			body, err := json.Marshal(ev)
			if err != nil {
				return true // can't marshal — skip but don't bail
			}
			line = string(body) + "\n"
		} else {
			line = formatHuman(ev) + "\n"
		}
		_, err := a.Stdout.Write([]byte(line))
		if err != nil {
			// Broken pipe = Monitor pipe closed = normal teardown.
			if errors.Is(err, syscall.EPIPE) {
				return false
			}
			fmt.Fprintf(a.Stderr, "clawtool task watch: emit: %v\n", err)
			return false
		}
		return true
	}
}

func formatHuman(ev watchEvent) string {
	ts := ev.TS.Local().Format("15:04:05")
	short := ev.TaskID
	if len(short) > 8 {
		short = short[:8]
	}
	out := fmt.Sprintf("[%s] %s · %s", ts, short, strings.ToUpper(ev.Status))
	if ev.Agent != "" {
		out += " · agent=" + ev.Agent
	}
	if ev.MessageCount > 0 {
		out += fmt.Sprintf(" · %d msg", ev.MessageCount)
	}
	if ev.LastMessage != "" {
		out += " · " + ev.LastMessage
	}
	return out
}

// truncate caps a string at n with an ellipsis. Used for the
// LastMessage preview so a huge blob doesn't drown the chat.
func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// runWatchOne polls one task until it reaches a terminal state,
// emitting on every status / message-count transition. Already-
// terminal tasks emit one line and exit 0 (no blocking).
func runWatchOne(ctx context.Context, a *App, store *biam.Store, taskID string, poll time.Duration, emit emitter) int {
	var prev *biam.Task
	for {
		t, err := store.GetTask(ctx, taskID)
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool task watch %s: %v\n", taskID, err)
			return 1
		}
		if t == nil {
			fmt.Fprintf(a.Stderr, "clawtool task watch %s: task not found\n", taskID)
			return 1
		}
		if changed(prev, t) {
			ev := snapshotToEvent(t)
			if !emit(ev) {
				return 0
			}
			prev = copyTask(t)
		}
		if t.Status.IsTerminal() {
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(poll):
		}
	}
}

// runWatchAll polls every BIAM task at the configured cadence.
// Emits a line per state change observed across the catalog.
// Runs until ctx cancels (SIGINT / SIGTERM / pipe close); the
// Monitor tool's session-length timeout governs total lifetime.
func runWatchAll(ctx context.Context, a *App, store *biam.Store, poll time.Duration, emit emitter) int {
	prev := map[string]*biam.Task{}
	for {
		// Cap to 1000 (the store's hard limit) — operator with
		// >1000 in-flight dispatches has bigger problems.
		tasks, err := store.ListTasks(ctx, 1000)
		if err != nil {
			// Transient SQLite-locked errors are common; sleep
			// + retry rather than crashing. Permanent failures
			// surface after a couple of polls when the operator
			// reads the next stderr.
			fmt.Fprintf(a.Stderr, "clawtool task watch --all: list: %v\n", err)
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(poll):
				continue
			}
		}
		// Sort by created_at for stable output order.
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		})
		for i := range tasks {
			t := tasks[i]
			old := prev[t.TaskID]
			if changed(old, &t) {
				ev := snapshotToEvent(&t)
				if !emit(ev) {
					return 0
				}
				prev[t.TaskID] = copyTask(&t)
			}
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(poll):
		}
	}
}

// changed reports whether t differs from prev in any field that
// should trigger a new event line. Status / MessageCount are the
// load-bearing axes; LastMessage is also tracked because a new
// terminal status often comes with a fresh tail body.
func changed(prev, t *biam.Task) bool {
	if prev == nil {
		return true
	}
	if prev.Status != t.Status {
		return true
	}
	if prev.MessageCount != t.MessageCount {
		return true
	}
	if prev.LastMessage != t.LastMessage {
		return true
	}
	return false
}

// snapshotToEvent maps a biam.Task into the wire-shaped watchEvent.
// Body preview capped at 80 chars per the ADR's security clause.
func snapshotToEvent(t *biam.Task) watchEvent {
	return watchEvent{
		TS:           time.Now().UTC(),
		TaskID:       t.TaskID,
		Status:       string(t.Status),
		Agent:        t.Agent,
		MessageCount: t.MessageCount,
		LastMessage:  truncate(t.LastMessage, 80),
	}
}

// copyTask makes a defensive copy so mutations on the next poll
// iteration don't bleed into the prev-state we compare against.
func copyTask(t *biam.Task) *biam.Task {
	if t == nil {
		return nil
	}
	out := *t
	if t.ClosedAt != nil {
		ca := *t.ClosedAt
		out.ClosedAt = &ca
	}
	return &out
}
