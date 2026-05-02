// Package cli — `clawtool autopilot` subcommand. Self-direction
// backlog: items the agent itself works on, dequeued in a loop so
// each finished task triggers the next without operator re-prompting.
//
// ─── Design (200 words) ────────────────────────────────────────────
//
// Shape of a backlog item: id (auto-generated, sortable), prompt
// (free text the agent reads), priority (int; higher first),
// status (pending|in_progress|done|skipped), created_at, claimed_at,
// done_at, optional note.
//
// Storage: TOML at $XDG_CONFIG_HOME/clawtool/autopilot/queue.toml
// (defaults to ~/.config/clawtool/autopilot/queue.toml). Per-host,
// not per-repo — a queue the operator builds at lunch survives until
// they reopen Claude Code in the evening. Atomic writes via
// internal/atomicfile keep the file from tearing if the daemon
// crashes mid-rewrite.
//
// Six verbs:
//
//   - clawtool autopilot add "<prompt>" [--priority N] [--note "..."]
//   - clawtool autopilot next                — atomic dequeue, marks in_progress
//   - clawtool autopilot done <id> [--note]  — marks done
//   - clawtool autopilot skip <id> [--note]  — drops without finishing
//   - clawtool autopilot list  [--status X]  [--format text|json]
//   - clawtool autopilot status              — histogram
//
// MCP mirror: AutopilotAdd / AutopilotNext / AutopilotDone /
// AutopilotSkip / AutopilotList / AutopilotStatus, defined in
// internal/tools/core/autopilot_tool.go. Same TOML store; CLI and
// MCP are interchangeable surfaces.
//
// Agent loop pattern: agent calls AutopilotNext → does the work →
// calls AutopilotDone → calls AutopilotNext again. When Next returns
// empty (no pending items), the agent ends the loop. This is the
// "devam edebilme yeteneği" — non-stalling continuation — the
// operator described.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cogitave/clawtool/internal/autopilot"
)

const autopilotUsage = `Usage:
  clawtool autopilot add "<prompt>" [--priority N] [--note "..."]
                                  Append a new pending item to the backlog.
  clawtool autopilot next [--format text|json]
                                  Atomically claim the highest-priority pending
                                  item (marks in_progress) and print its prompt.
                                  Empty queue → exit 0 with no output (text) or
                                  '{}' (json). Call this in a loop after each
                                  task to keep working without operator re-prompting.
  clawtool autopilot done <id> [--note "..."]
                                  Mark the named item done.
  clawtool autopilot skip <id> [--note "..."]
                                  Drop the named item without finishing it.
  clawtool autopilot list [--status pending|in_progress|done|skipped] [--format text|json]
                                  Show every item, optionally filtered by status.
  clawtool autopilot status [--format text|json]
                                  Histogram: pending / in_progress / done / skipped / total.

Storage:
  $XDG_CONFIG_HOME/clawtool/autopilot/queue.toml (default
  ~/.config/clawtool/autopilot/queue.toml). Per-host, atomic writes.

Agent loop:
  while true; do
    item="$(clawtool autopilot next --format json)"
    [ "$item" = "{}" ] && break
    id="$(jq -r .id <<<"$item")"
    # ... do the work ...
    clawtool autopilot done "$id"
  done
`

func (a *App) runAutopilot(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, autopilotUsage)
		return 2
	}
	switch argv[0] {
	case "add":
		return a.runAutopilotAdd(argv[1:])
	case "next":
		return a.runAutopilotNext(argv[1:])
	case "done", "complete":
		return a.runAutopilotDone(argv[1:])
	case "skip":
		return a.runAutopilotSkip(argv[1:])
	case "list":
		return a.runAutopilotList(argv[1:])
	case "status":
		return a.runAutopilotStatus(argv[1:])
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, autopilotUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool autopilot: unknown subcommand %q\n\n%s",
			argv[0], autopilotUsage)
		return 2
	}
}

func (a *App) runAutopilotAdd(argv []string) int {
	fs := flag.NewFlagSet("autopilot add", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	priority := fs.Int("priority", 0, "Priority (higher dequeues first).")
	note := fs.String("note", "", "Optional note attached to the item.")
	flagArgs, positional := splitFlagsAndPositionals(argv,
		map[string]bool{"--priority": true, "--note": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positional) == 0 {
		fmt.Fprintln(a.Stderr, "usage: clawtool autopilot add \"<prompt>\" [--priority N] [--note \"...\"]")
		return 2
	}
	prompt := strings.Join(positional, " ")
	q := autopilot.Open()
	it, err := q.Add(prompt, *priority, *note)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autopilot add: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "%s\n", it.ID)
	return 0
}

// splitFlagsAndPositionals separates flag tokens from positional
// words. Knows the value-flags (those declared in valueFlags) take
// the next token as their value. Boolean flags (foo without a
// trailing word) are passed through to flag.Parse intact. Used by
// `add` so `add "prompt words" --priority 5` works regardless of
// the operator's flag/positional ordering.
func splitFlagsAndPositionals(argv []string, valueFlags map[string]bool) (flags, positional []string) {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		// Tokens like `--key=value` are self-contained.
		if strings.Contains(a, "=") {
			flags = append(flags, a)
			continue
		}
		flags = append(flags, a)
		if valueFlags[a] && i+1 < len(argv) {
			i++
			flags = append(flags, argv[i])
		}
	}
	return flags, positional
}

func (a *App) runAutopilotNext(argv []string) int {
	fs := flag.NewFlagSet("autopilot next", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	format := fs.String("format", "text", "Output format: text | json.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	q := autopilot.Open()
	it, ok, err := q.Claim()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autopilot next: %v\n", err)
		return 1
	}
	if !ok {
		// Empty queue — silent exit on text, `{}` on json so a shell
		// loop can `[ "$item" = "{}" ] && break` reliably.
		if *format == "json" {
			fmt.Fprintln(a.Stdout, "{}")
		}
		return 0
	}
	switch *format {
	case "json":
		body, _ := json.MarshalIndent(it, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
	default:
		fmt.Fprintf(a.Stdout, "%s\n%s\n", it.ID, it.Prompt)
	}
	return 0
}

func (a *App) runAutopilotDone(argv []string) int {
	fs := flag.NewFlagSet("autopilot done", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	note := fs.String("note", "", "Optional completion note.")
	id, rest, err := splitFirstPositional(argv)
	if err != nil {
		fmt.Fprintln(a.Stderr, "usage: clawtool autopilot done <id> [--note \"...\"]")
		return 2
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	q := autopilot.Open()
	it, err := q.Complete(id, *note)
	if err != nil {
		if errors.Is(err, autopilot.ErrNotFound) {
			fmt.Fprintf(a.Stderr, "clawtool autopilot done: %s: not found\n", id)
			return 1
		}
		if errors.Is(err, autopilot.ErrAlreadyTerminal) {
			fmt.Fprintf(a.Stderr, "clawtool autopilot done: %s: already %s\n", id, it.Status)
			return 1
		}
		fmt.Fprintf(a.Stderr, "clawtool autopilot done: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "%s done\n", it.ID)
	return 0
}

func (a *App) runAutopilotSkip(argv []string) int {
	fs := flag.NewFlagSet("autopilot skip", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	note := fs.String("note", "", "Optional skip reason.")
	id, rest, err := splitFirstPositional(argv)
	if err != nil {
		fmt.Fprintln(a.Stderr, "usage: clawtool autopilot skip <id> [--note \"...\"]")
		return 2
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	q := autopilot.Open()
	it, err := q.Skip(id, *note)
	if err != nil {
		if errors.Is(err, autopilot.ErrNotFound) {
			fmt.Fprintf(a.Stderr, "clawtool autopilot skip: %s: not found\n", id)
			return 1
		}
		if errors.Is(err, autopilot.ErrAlreadyTerminal) {
			fmt.Fprintf(a.Stderr, "clawtool autopilot skip: %s: already %s\n", id, it.Status)
			return 1
		}
		fmt.Fprintf(a.Stderr, "clawtool autopilot skip: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "%s skipped\n", it.ID)
	return 0
}

// splitFirstPositional pops the first non-flag argument from argv
// and returns it plus the remainder for the flagset. Stdlib `flag`
// stops at the first non-flag, so `done <id> --note "..."` would
// see `--note` as a trailing positional. This helper lets verbs
// accept the natural shape `<verb> <id> --flag value`.
func splitFirstPositional(argv []string) (id string, rest []string, err error) {
	for i, a := range argv {
		if strings.HasPrefix(a, "-") {
			continue
		}
		// Found the positional.
		out := append([]string(nil), argv[:i]...)
		out = append(out, argv[i+1:]...)
		return a, out, nil
	}
	return "", nil, errors.New("autopilot: missing required <id> positional")
}

func (a *App) runAutopilotList(argv []string) int {
	fs := flag.NewFlagSet("autopilot list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	statusFlag := fs.String("status", "", "Filter: pending | in_progress | done | skipped.")
	format := fs.String("format", "text", "Output format: text | json.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	q := autopilot.Open()
	items, err := q.List(autopilot.Status(*statusFlag))
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autopilot list: %v\n", err)
		return 1
	}
	if *format == "json" {
		body, _ := json.MarshalIndent(items, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}
	if len(items) == 0 {
		fmt.Fprintln(a.Stdout, "(empty)")
		return 0
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tPRIO\tCREATED\tPROMPT")
	for _, it := range items {
		prompt := it.Prompt
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			it.ID, it.Status, it.Priority,
			it.CreatedAt.Format(time.RFC3339), prompt)
	}
	_ = tw.Flush()
	return 0
}

func (a *App) runAutopilotStatus(argv []string) int {
	fs := flag.NewFlagSet("autopilot status", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	format := fs.String("format", "text", "Output format: text | json.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	q := autopilot.Open()
	c, err := q.Status()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool autopilot status: %v\n", err)
		return 1
	}
	if *format == "json" {
		body, _ := json.MarshalIndent(c, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}
	fmt.Fprintf(a.Stdout,
		"pending=%d in_progress=%d done=%d skipped=%d total=%d\n",
		c.Pending, c.InProgress, c.Done, c.Skipped, c.Total)
	return 0
}
