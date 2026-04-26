// Package cli implements clawtool's user-facing subcommands.
//
// Subcommand layout (ADR-004 §4):
//
//	clawtool init              write a default config if missing
//	clawtool serve             run as MCP server (delegates to internal/server)
//	clawtool version           print version
//	clawtool help              print top-level usage
//	clawtool tools list        list known tools and resolved enabled state
//	clawtool tools enable <s>  set tools.<selector>.enabled = true
//	clawtool tools disable <s> set tools.<selector>.enabled = false
//	clawtool tools status <s>  print resolved state and the rule that won
//
// Source / profile / group subcommands are scaffolded in main.go usage but
// not wired in v0.2 — they land alongside the source-instance feature.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cogitave/clawtool/internal/config"
)

// App holds CLI dependencies. Stdout/stderr are injected so tests can capture.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	// ConfigPath overrides the default config location. Empty = config.DefaultPath().
	ConfigPath string
}

// New returns an App writing to the process's stdout/stderr and using the
// default config path.
func New() *App {
	return &App{Stdout: os.Stdout, Stderr: os.Stderr}
}

// Path returns the resolved config path (override > default).
func (a *App) Path() string {
	if a.ConfigPath != "" {
		return a.ConfigPath
	}
	return config.DefaultPath()
}

// Init writes a default config to disk if the file does not already exist.
// Returns an "already exists" error if it does — callers can ignore that.
func (a *App) Init() error {
	path := a.Path()
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(a.Stdout, "config already exists at %s\n", path)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ wrote default config to %s\n", path)
	return nil
}

// ToolsList prints registered core tools and their resolved enabled state.
func (a *App) ToolsList() error {
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return err
	}
	entries := cfg.ListCoreTools()
	w := a.Stdout
	fmt.Fprintln(w, "TOOL                          STATE      RULE")
	for _, e := range entries {
		state := "enabled"
		if !e.Resolution.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(w, "%-29s %-10s %s\n", e.Selector, state, e.Resolution.Rule)
	}
	// v0.2 doesn't yet enumerate sourced tools — note that explicitly so
	// users know the full picture is coming.
	if len(cfg.Sources) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "(%d source instance(s) configured but their tools are not yet enumerated; v0.3.)\n", len(cfg.Sources))
	}
	return nil
}

// ToolsEnable writes tools.<selector>.enabled = true.
func (a *App) ToolsEnable(selector string) error {
	return a.toolsSet(selector, true)
}

// ToolsDisable writes tools.<selector>.enabled = false.
func (a *App) ToolsDisable(selector string) error {
	return a.toolsSet(selector, false)
}

func (a *App) toolsSet(selector string, enabled bool) error {
	if err := validateSelector(selector); err != nil {
		return err
	}
	path := a.Path()
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return err
	}
	cfg.SetToolEnabled(selector, enabled)
	if err := cfg.Save(path); err != nil {
		return err
	}
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	fmt.Fprintf(a.Stdout, "✓ %s %s (rule: tools.%s)\n", selector, state, quoteIfDot(selector))
	return nil
}

// ToolsStatus prints the resolved enabled state for a selector and the rule
// that won, per ADR-004 §4.
func (a *App) ToolsStatus(selector string) error {
	if err := validateSelector(selector); err != nil {
		return err
	}
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		return err
	}
	r := cfg.IsEnabled(selector)
	state := "enabled"
	if !r.Enabled {
		state = "disabled"
	}
	fmt.Fprintf(a.Stdout, "%s %s (rule: %s)\n", selector, state, r.Rule)
	return nil
}

// Run dispatches argv (excluding program name) to the right subcommand.
// Returns the exit code; 0 = success, 2 = usage error, 1 = runtime failure.
func (a *App) Run(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, topUsage)
		return 2
	}
	switch argv[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(a.Stderr)
		if err := fs.Parse(argv[1:]); err != nil {
			return 2
		}
		if err := a.Init(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool init: %v\n", err)
			return 1
		}
	case "tools":
		return a.runTools(argv[1:])
	case "version", "--version", "-v":
		// Version printed by caller (it owns the version package import to
		// avoid an import cycle with cli — keeps cli a leaf package).
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, topUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool: unknown command %q\n\n%s", argv[0], topUsage)
		return 2
	}
	return 0
}

func (a *App) runTools(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, toolsUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		if err := a.ToolsList(); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools list: %v\n", err)
			return 1
		}
	case "enable":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool tools enable <selector>\n")
			return 2
		}
		if err := a.ToolsEnable(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools enable: %v\n", err)
			return 1
		}
	case "disable":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool tools disable <selector>\n")
			return 2
		}
		if err := a.ToolsDisable(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools disable: %v\n", err)
			return 1
		}
	case "status":
		if len(argv) != 2 {
			fmt.Fprint(a.Stderr, "usage: clawtool tools status <selector>\n")
			return 2
		}
		if err := a.ToolsStatus(argv[1]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool tools status: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(a.Stderr, "clawtool tools: unknown subcommand %q\n\n%s", argv[0], toolsUsage)
		return 2
	}
	return 0
}

// validateSelector enforces the ADR-006 charset rules at the user's first
// touchpoint. We do not yet implement tag:/group:/profile-aware selectors;
// rejecting them up front prevents silent no-ops.
func validateSelector(s string) error {
	if s == "" {
		return errors.New("selector is empty")
	}
	if strings.HasPrefix(s, "tag:") || strings.HasPrefix(s, "group:") {
		return fmt.Errorf("selector %q uses tag:/group: prefix — supported in v0.3, not yet wired", s)
	}
	// Cheap validation: tools must be either a PascalCase core tool name OR
	// `<instance>.<tool>` with kebab-case instance and snake_case tool.
	if isCoreLooking(s) {
		return nil
	}
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		return fmt.Errorf("selector %q is not a known shape (expected PascalCase core tool name or `<instance>.<tool>`)", s)
	}
	instance, tool := s[:dot], s[dot+1:]
	if !isKebab(instance) {
		return fmt.Errorf("selector %q: instance %q must be kebab-case [a-z0-9-]+", s, instance)
	}
	if !isSnake(tool) {
		return fmt.Errorf("selector %q: tool %q must be snake_case [a-z0-9_]+", s, tool)
	}
	return nil
}

func isCoreLooking(s string) bool {
	if s == "" {
		return false
	}
	if c := s[0]; c < 'A' || c > 'Z' {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func isKebab(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func isSnake(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func quoteIfDot(s string) string {
	if strings.Contains(s, ".") {
		return `"` + s + `"`
	}
	return s
}

const topUsage = `clawtool — canonical tool layer for AI coding agents

Usage:
  clawtool serve            Run as an MCP server over stdio.
  clawtool init             Write a default config to ~/.config/clawtool/config.toml.
  clawtool tools list       List known tools and their resolved enabled state.
  clawtool tools enable <selector>
  clawtool tools disable <selector>
  clawtool tools status <selector>
  clawtool version          Print the build version.
  clawtool help             Show this help.

Selector forms:
  Bash                      A core tool (PascalCase, ADR-006).
  github-personal.create_issue
                            A sourced tool: <instance>.<tool>. Instance is
                            kebab-case, tool is snake_case.

Future (v0.3+):
  tag:destructive           Tag-level selector (ADR-004 §4).
  group:review-set          Group-level selector.
  clawtool source add <name> -- <command...>
  clawtool profile use <name>
  clawtool group create <name> <selectors...>
`

const toolsUsage = `Usage:
  clawtool tools list
  clawtool tools enable <selector>
  clawtool tools disable <selector>
  clawtool tools status <selector>
`
