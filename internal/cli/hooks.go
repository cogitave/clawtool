package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cogitave/clawtool/internal/cli/listfmt"
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
  clawtool hooks install <runtime>         Print the hook config snippet that
                                           wires <runtime> into clawtool's peer
                                           registry. <runtime> = claude-code |
                                           codex | gemini | opencode.

Hooks are configured in ~/.config/clawtool/config.toml under
[hooks.events.<name>]. Each entry is a HookEntry { cmd | argv,
timeout_ms, block_on_error }. Use 'hooks test' to verify your shell
snippets without firing the actual lifecycle event.

'hooks install' is the runtime-side wiring helper for ADR-024 peer
discovery: it prints the snippet you drop into the runtime's config
file so the runtime calls 'clawtool peer register / heartbeat /
deregister' at session boundaries. claude-code is bundled — you only
need install for codex/gemini/opencode.
`

// runHooks dispatches `clawtool hooks …`.
func (a *App) runHooks(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, hooksUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		format, _, err := listfmt.ExtractFlag(argv[1:])
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool hooks list: %v\n", err)
			return 2
		}
		if err := a.HooksList(format); err != nil {
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
	case "install":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool hooks install <claude-code|codex|gemini|opencode>\n")
			return 2
		}
		if err := a.HooksInstall(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool hooks install: %v\n", err)
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
func (a *App) HooksList(format listfmt.Format) error {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	header := []string{"EVENT", "ENTRIES"}
	if len(cfg.Hooks.Events) == 0 {
		// Empty-state contract (sister of source / sandbox /
		// portal list): table mode keeps the actionable hint,
		// JSON / TSV consumers get the structured empty shape
		// (`[]\n` and a header line) so a `clawtool hooks list
		// --format json | jq '. | length'` pipeline returns 0
		// instead of choking on the human banner.
		if format == listfmt.FormatTable {
			fmt.Fprintln(a.Stdout, "(no hooks configured — see https://github.com/cogitave/clawtool#hooks for examples)")
			return nil
		}
		return listfmt.Render(a.Stdout, format, listfmt.Cols{Header: header})
	}
	names := make([]string, 0, len(cfg.Hooks.Events))
	for n := range cfg.Hooks.Events {
		names = append(names, n)
	}
	sort.Strings(names)
	cols := listfmt.Cols{Header: header}
	for _, n := range names {
		entries := cfg.Hooks.Events[n]
		cols.Rows = append(cols.Rows, []string{n, strconv.Itoa(len(entries))})
	}
	return listfmt.Render(a.Stdout, format, cols)
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

// HooksInstall prints the runtime-specific snippet that wires
// <runtime> into clawtool's peer registry. We deliberately *print*
// rather than mutate config files: each runtime's config layout
// changes between versions, and an operator can paste the snippet
// into whichever location their version expects. claude-code's
// bundled hooks/hooks.json already covers it via the plugin, so we
// short-circuit there.
func (a *App) HooksInstall(runtime string) error {
	switch runtime {
	case "claude-code", "claude":
		fmt.Fprintln(a.Stdout, "claude-code hooks are bundled in this plugin's hooks/hooks.json — no manual install needed.")
		fmt.Fprintln(a.Stdout, "After upgrading clawtool, restart your Claude Code session so it re-reads hooks.json.")
		return nil
	case "codex":
		fmt.Fprint(a.Stdout, codexHookSnippet)
		return nil
	case "gemini":
		fmt.Fprint(a.Stdout, geminiHookSnippet)
		return nil
	case "opencode":
		fmt.Fprint(a.Stdout, opencodeHookSnippet)
		return nil
	default:
		return fmt.Errorf("unknown runtime %q (expected claude-code | codex | gemini | opencode)", runtime)
	}
}

const codexHookSnippet = `# Codex peer-discovery hooks (clawtool ADR-024 Phase 1).
# Drop into ~/.codex/config.toml under [hooks]:

[hooks]
session_start = "clawtool peer register --backend codex"
session_end   = "clawtool peer deregister"
# Optional: heartbeat every turn. Codex doesn't expose a turn-end
# event today; until it does, rely on the daemon's stale-sweep
# (peers flip to offline after 60s without a heartbeat).
`

const geminiHookSnippet = `# Gemini-CLI peer-discovery hooks (clawtool ADR-024 Phase 1).
# Gemini-CLI ships a hooks system in v0.4+; until then, run these
# manually at the start/end of each session, or wrap your launcher
# script around them:

clawtool peer register --backend gemini
# ... gemini session runs ...
clawtool peer deregister

# When Gemini-CLI's hooks land, the equivalent config lives in
# ~/.config/gemini/hooks.toml — same shape as codex.
`

const opencodeHookSnippet = `# OpenCode peer-discovery hooks (clawtool ADR-024 Phase 1).
# OpenCode reads ~/.config/opencode/hooks.json. Add:

{
  "hooks": {
    "session.start": [{ "command": "clawtool peer register --backend opencode" }],
    "session.end":   [{ "command": "clawtool peer deregister" }]
  }
}

# OpenCode is research-only in clawtool's send/dispatch routing;
# peer discovery still works — it just shows up in the registry as
# "opencode" so the operator knows it's available for inspection.
`

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
