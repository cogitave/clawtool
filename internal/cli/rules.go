// Package cli — `clawtool rules` subcommand. Lifecycle management
// for the operator's predicate-based invariants in
// .clawtool/rules.toml (project-local) or
// ~/.config/clawtool/rules.toml (user-global).
//
// Operator-facing surface:
//
//	clawtool rules list                     show every loaded rule + its source
//	clawtool rules show <name>              detail view of one rule
//	clawtool rules new <name> [flags]       add a new rule (asks scope when ambiguous)
//	clawtool rules remove <name>            delete a rule
//	clawtool rules path [--user|--local]    print the rules file path
//	clawtool rules check <event> [flags]    one-shot evaluation against current state
//
// Why this lives in CLI: the operator wants to add a rule from a
// fresh-context shell without firing up an editor; the parallel
// MCP-side tool (RulesAdd) is a thin wrapper that calls the same
// rules.AppendRule helper this CLI does.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cogitave/clawtool/internal/rules"
)

// ruleListEntry is the JSON shape produced by `rules list --json`.
// snake_case keys mirror the project's wire convention (matches
// agentListEntry, agents.Status, version.BuildInfo). Source is
// the rules.toml path the rule was loaded from — same value
// `rules path` prints, surfaced here so a script can correlate
// "which file has rule X" without a second invocation.
type ruleListEntry struct {
	Name        string `json:"name"`
	When        string `json:"when"`
	Severity    string `json:"severity"`
	Description string `json:"description,omitempty"`
	Condition   string `json:"condition"`
	Hint        string `json:"hint,omitempty"`
	Source      string `json:"source"`
}

const rulesUsage = `Usage:
  clawtool rules list                              List every loaded rule with its source path.
  clawtool rules show <name>                       Detail view of one rule (when, condition, severity, hint).
  clawtool rules new <name> --when <event> --condition '<expr>' [options]
                                                   Add a new rule. Defaults: severity=warn, scope=local.
  clawtool rules remove <name> [--user|--local]    Delete the rule. Without scope flag, removes from the
                                                   first file that contains the rule.
  clawtool rules path [--user|--local]             Print the rules file path.

Options for 'new':
  --description "..."                              One-line human description (optional).
  --severity off|warn|block                        Default warn.
  --hint "..."                                     Operator-facing hint when the rule fires.
  --user                                           Write to ~/.config/clawtool/rules.toml (or
                                                   $XDG_CONFIG_HOME). Default --local.
  --local                                          Write to ./.clawtool/rules.toml (default).

Events:
  pre_commit, post_edit, session_end, pre_send, pre_unattended

See docs/rules.md for the predicate DSL (changed / commit_message_contains /
tool_call_count / arg / true / false + AND/OR/NOT).
`

func (a *App) runRules(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, rulesUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		return a.runRulesList(argv[1:])
	case "show":
		return a.runRulesShow(argv[1:])
	case "new", "add":
		return a.runRulesNew(argv[1:])
	case "remove", "rm", "delete":
		return a.runRulesRemove(argv[1:])
	case "path":
		return a.runRulesPath(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool rules: unknown subcommand %q\n\n%s",
			argv[0], rulesUsage)
		return 2
	}
}

// resolveScope returns the rules file path based on flags. Default
// is local (./.clawtool/rules.toml) — operators typically scope
// rules to a project; user-global is opt-in.
func resolveScope(argv []string) (path string, fromFlag string, err error) {
	user, local := false, false
	for _, a := range argv {
		switch a {
		case "--user":
			user = true
		case "--local":
			local = true
		}
	}
	if user && local {
		return "", "", fmt.Errorf("--user and --local are mutually exclusive")
	}
	if user {
		return rules.UserRulesPath(), "user", nil
	}
	return rules.LocalRulesPath(), "local", nil
}

func (a *App) runRulesList(argv []string) int {
	fs := flag.NewFlagSet("rules list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asJSON := fs.Bool("json", false, "Emit machine-readable JSON instead of the human table.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if len(fs.Args()) > 0 {
		fmt.Fprint(a.Stderr, "usage: clawtool rules list [--json]\n")
		return 2
	}

	loaded, path, ok, err := rules.LoadDefault()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool rules list: %v\n", err)
		return 1
	}

	if *asJSON {
		// Always emit a JSON array (possibly empty) — shape stays
		// uniform whether rules.toml exists or not, so `jq '.[]'`
		// pipelines don't have to special-case the no-config
		// branch.
		entries := make([]ruleListEntry, 0, len(loaded))
		for _, r := range loaded {
			entries = append(entries, ruleListEntry{
				Name:        r.Name,
				When:        string(r.When),
				Severity:    string(r.Severity),
				Description: r.Description,
				Condition:   r.Condition,
				Hint:        r.Hint,
				Source:      path,
			})
		}
		body, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool rules list: marshal: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}

	if !ok {
		fmt.Fprintln(a.Stdout, "(no rules configured — try `clawtool rules new <name> --when pre_commit --condition '...'`)")
		return 0
	}
	fmt.Fprintf(a.Stdout, "source: %s\n\n", path)
	fmt.Fprintf(a.Stdout, "%-30s %-20s %-10s %s\n", "NAME", "WHEN", "SEVERITY", "DESCRIPTION")
	for _, r := range loaded {
		desc := r.Description
		if len(desc) > 60 {
			desc = desc[:57] + "…"
		}
		fmt.Fprintf(a.Stdout, "%-30s %-20s %-10s %s\n",
			r.Name, string(r.When), string(r.Severity), desc)
	}
	return 0
}

func (a *App) runRulesShow(argv []string) int {
	fs := flag.NewFlagSet("rules show", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	asJSON := fs.Bool("json", false, "Emit machine-readable JSON instead of the human key:value block.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{})); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool rules show <name> [--json]\n")
		return 2
	}
	target := rest[0]
	loaded, path, ok, err := rules.LoadDefault()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool rules show: %v\n", err)
		return 1
	}
	if !ok {
		// JSON path emits an error object so a script can
		// distinguish "no rules configured" from "rule not
		// found" via the structured `error` field; both still
		// exit 1.
		if *asJSON {
			fmt.Fprintln(a.Stdout, `{"error":"no rules configured"}`)
		} else {
			fmt.Fprintln(a.Stderr, "no rules configured")
		}
		return 1
	}
	for _, r := range loaded {
		if r.Name != target {
			continue
		}
		if *asJSON {
			body, marshalErr := json.MarshalIndent(ruleListEntry{
				Name:        r.Name,
				When:        string(r.When),
				Severity:    string(r.Severity),
				Description: r.Description,
				Condition:   r.Condition,
				Hint:        r.Hint,
				Source:      path,
			}, "", "  ")
			if marshalErr != nil {
				fmt.Fprintf(a.Stderr, "clawtool rules show: marshal: %v\n", marshalErr)
				return 1
			}
			fmt.Fprintln(a.Stdout, string(body))
			return 0
		}
		fmt.Fprintf(a.Stdout, "name:        %s\n", r.Name)
		fmt.Fprintf(a.Stdout, "source:      %s\n", path)
		fmt.Fprintf(a.Stdout, "when:        %s\n", string(r.When))
		fmt.Fprintf(a.Stdout, "severity:    %s\n", string(r.Severity))
		if r.Description != "" {
			fmt.Fprintf(a.Stdout, "description: %s\n", r.Description)
		}
		fmt.Fprintf(a.Stdout, "condition:   %s\n", r.Condition)
		if r.Hint != "" {
			fmt.Fprintf(a.Stdout, "hint:        %s\n", r.Hint)
		}
		return 0
	}
	if *asJSON {
		body, _ := json.Marshal(map[string]string{
			"error":  fmt.Sprintf("rule %q not found", target),
			"source": path,
		})
		fmt.Fprintln(a.Stdout, string(body))
	} else {
		fmt.Fprintf(a.Stderr, "rule %q not found in %s\n", target, path)
	}
	return 1
}

func (a *App) runRulesNew(argv []string) int {
	if len(argv) < 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool rules new <name> --when <event> --condition '<expr>' [options] [--dry-run]\n")
		return 2
	}
	name := argv[0]
	rest := argv[1:]
	var (
		when        string
		cond        string
		severity    = "warn"
		description string
		hint        string
		dryRun      bool
	)
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--when":
			if i+1 < len(rest) {
				when = rest[i+1]
				i++
			}
		case "--condition":
			if i+1 < len(rest) {
				cond = rest[i+1]
				i++
			}
		case "--severity":
			if i+1 < len(rest) {
				severity = rest[i+1]
				i++
			}
		case "--description":
			if i+1 < len(rest) {
				description = rest[i+1]
				i++
			}
		case "--hint":
			if i+1 < len(rest) {
				hint = rest[i+1]
				i++
			}
		case "--dry-run":
			dryRun = true
		case "--user", "--local":
			// handled by resolveScope
		default:
			if strings.HasPrefix(rest[i], "--") {
				fmt.Fprintf(a.Stderr, "clawtool rules new: unknown flag %q\n", rest[i])
				return 2
			}
		}
	}
	if when == "" || cond == "" {
		fmt.Fprintln(a.Stderr, "clawtool rules new: --when and --condition are required")
		return 2
	}
	path, scope, err := resolveScope(rest)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool rules new: %v\n", err)
		return 2
	}
	rule := rules.Rule{
		Name:        name,
		Description: description,
		When:        rules.Event(when),
		Condition:   cond,
		Severity:    rules.Severity(severity),
		Hint:        hint,
	}
	if dryRun {
		// Run every check AppendRule would run (shape, condition
		// syntax, duplicate-name) without writing rules.toml.
		// Operators preview a complex condition before
		// committing; CI can validate-only without mutating
		// the project's rules file.
		if err := rules.CheckRuleAdd(path, rule); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool rules new: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Stdout, "(dry-run) would add rule %q (scope=%s, path=%s)\n", name, scope, path)
		fmt.Fprintf(a.Stdout, "  when:      %s\n", when)
		fmt.Fprintf(a.Stdout, "  severity:  %s\n", severity)
		fmt.Fprintf(a.Stdout, "  condition: %s\n", cond)
		if description != "" {
			fmt.Fprintf(a.Stdout, "  description: %s\n", description)
		}
		if hint != "" {
			fmt.Fprintf(a.Stdout, "  hint:      %s\n", hint)
		}
		return 0
	}
	if err := rules.AppendRule(path, rule); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool rules new: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ rule %q added (scope=%s, path=%s)\n", name, scope, path)
	return 0
}

func (a *App) runRulesRemove(argv []string) int {
	if len(argv) < 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool rules remove <name> [--user|--local] [--dry-run]\n")
		return 2
	}
	name := argv[0]
	rest := argv[1:]
	// Try the explicit scope first; fall back to walking both
	// roots if the operator didn't specify.
	candidates := []string{}
	dryRun := false
	for _, a := range rest {
		switch a {
		case "--user":
			candidates = []string{rules.UserRulesPath()}
		case "--local":
			candidates = []string{rules.LocalRulesPath()}
		case "--dry-run":
			dryRun = true
		}
	}
	if len(candidates) == 0 {
		candidates = rules.DefaultRoots()
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if dryRun {
			// Preview path: locate the rule in this file
			// without writing. Symmetric with `rules new
			// --dry-run` — same kind of validate-only pass
			// before a mutation.
			r, ok, err := rules.LookupRule(p, name)
			if err != nil {
				fmt.Fprintf(a.Stderr, "clawtool rules remove: %v\n", err)
				return 1
			}
			if !ok {
				continue
			}
			fmt.Fprintf(a.Stdout, "(dry-run) would remove rule %q from %s\n", name, p)
			fmt.Fprintf(a.Stdout, "  when:      %s\n", string(r.When))
			fmt.Fprintf(a.Stdout, "  severity:  %s\n", string(r.Severity))
			fmt.Fprintf(a.Stdout, "  condition: %s\n", r.Condition)
			if r.Description != "" {
				fmt.Fprintf(a.Stdout, "  description: %s\n", r.Description)
			}
			if r.Hint != "" {
				fmt.Fprintf(a.Stdout, "  hint:      %s\n", r.Hint)
			}
			return 0
		}
		gone, err := rules.RemoveRule(p, name)
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool rules remove: %v\n", err)
			return 1
		}
		if gone {
			fmt.Fprintf(a.Stdout, "✓ rule %q removed from %s\n", name, p)
			return 0
		}
	}
	fmt.Fprintf(a.Stderr, "clawtool rules remove: %q not found in any rules file\n", name)
	return 1
}

func (a *App) runRulesPath(argv []string) int {
	for _, a := range argv {
		if a == "--user" {
			fmt.Println(rules.UserRulesPath())
			return 0
		}
		if a == "--local" {
			fmt.Println(rules.LocalRulesPath())
			return 0
		}
	}
	// No flag: print BOTH so the operator sees the lookup order.
	fmt.Printf("local: %s\n", rules.LocalRulesPath())
	fmt.Printf("user:  %s\n", rules.UserRulesPath())
	return 0
}
