package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/hooks"
)

const hooksUsage = `Usage:
  clawtool hooks list                      Configured events + entry counts.
  clawtool hooks show <event>              Print the entries for one event.
  clawtool hooks test <event> [--payload <json>]
                                           Synthesise the event and run every
                                           configured entry. Prints success/
                                           failure per entry.

Hooks are configured in ~/.config/clawtool/config.toml under
[hooks.events.<name>]. Each entry is a HookEntry { cmd | argv,
timeout_ms, block_on_error }. Use 'hooks test' to verify your shell
snippets without firing the actual lifecycle event.
`

// runHooks dispatches `clawtool hooks …`.
func (a *App) runHooks(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, hooksUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		if err := a.HooksList(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool hooks list: %v\n", err)
			return 1
		}
	case "show":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool hooks show <event>\n")
			return 2
		}
		if err := a.HooksShow(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool hooks show: %v\n", err)
			return 1
		}
	case "test":
		if len(argv) < 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool hooks test <event> [--payload <json>]\n")
			return 2
		}
		event := argv[1]
		payload := map[string]any{"synthetic": true}
		for i := 2; i < len(argv); i++ {
			if argv[i] == "--payload" && i+1 < len(argv) {
				if err := json.Unmarshal([]byte(argv[i+1]), &payload); err != nil {
					fmt.Fprintf(a.Stderr, "invalid --payload JSON: %v\n", err)
					return 2
				}
				i++
			}
		}
		if err := a.HooksTest(event, payload); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool hooks test: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool hooks: unknown subcommand %q\n\n%s", argv[0], hooksUsage)
		return 2
	}
	return 0
}

// HooksList prints every configured event with its entry count.
// Empty config → friendly hint.
func (a *App) HooksList() error {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if len(cfg.Hooks.Events) == 0 {
		fmt.Fprintln(a.Stdout, "(no hooks configured — see https://github.com/cogitave/clawtool#hooks for examples)")
		return nil
	}
	names := make([]string, 0, len(cfg.Hooks.Events))
	for n := range cfg.Hooks.Events {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(a.Stdout, "%-24s %s\n", "EVENT", "ENTRIES")
	for _, n := range names {
		entries := cfg.Hooks.Events[n]
		fmt.Fprintf(a.Stdout, "%-24s %d\n", n, len(entries))
	}
	return nil
}

// HooksShow dumps the per-entry config for a single event.
func (a *App) HooksShow(event string) error {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	entries, ok := cfg.Hooks.Events[event]
	if !ok || len(entries) == 0 {
		fmt.Fprintf(a.Stdout, "(no entries configured for %q)\n", event)
		return nil
	}
	for i, e := range entries {
		spec := e.Cmd
		if spec == "" {
			spec = strings.Join(e.Argv, " ")
		}
		fmt.Fprintf(a.Stdout, "[%d] timeout=%dms block_on_error=%v\n    %s\n", i, e.TimeoutMs, e.BlockOnErr, spec)
	}
	return nil
}

// HooksTest synthesises the event with the given payload and runs
// every configured entry. Prints per-entry success/failure so the
// operator can iterate on hook scripts without firing the real
// lifecycle event (which might be hard to reproduce).
func (a *App) HooksTest(event string, payload map[string]any) error {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	entries, ok := cfg.Hooks.Events[event]
	if !ok || len(entries) == 0 {
		fmt.Fprintf(a.Stdout, "(no entries configured for %q — nothing to do)\n", event)
		return nil
	}
	mgr := hooks.New(cfg.Hooks)
	if err := mgr.Emit(context.Background(), hooks.Event(event), payload); err != nil {
		fmt.Fprintf(a.Stdout, "✘ %s: %v\n", event, err)
		return nil // exit 0 — the test already printed the failure
	}
	fmt.Fprintf(a.Stdout, "✓ %s: %d entry/entries ran cleanly\n", event, len(entries))
	return nil
}
