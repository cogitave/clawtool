// Package cli — `clawtool skill` subcommand. Scaffolds and lists
// Agent Skills per the agentskills.io spec: a folder containing
// SKILL.md (required, with frontmatter name + description) plus
// optional scripts/, references/, assets/ subdirectories.
//
// The standard was authored by Anthropic and is now open. clawtool's
// skill subcommand is the bootstrap layer — `clawtool skill new` is
// the analogue of `npm init` for Agent Skills.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/cli/listfmt"
	"github.com/cogitave/clawtool/internal/skillgen"
)

const skillUsage = `Usage:
  clawtool skill new <name> --description "..." [options]
                            Scaffold a new Agent Skill (agentskills.io
                            standard): SKILL.md with frontmatter +
                            scripts/ + references/ + assets/.
  clawtool skill list       Enumerate installed skills under
                            ~/.claude/skills and ./.claude/skills.
  clawtool skill path [<name>]
                            Print the on-disk path of a skill.

Options for 'new':
  --description "..."       Required. One-paragraph description that
                            also tells agents WHEN to load the skill.
  --triggers "a,b,c"        Optional. Comma-separated phrases that the
                            agent's loader matches against. Captured in
                            frontmatter as 'triggers:' (one per line).
  --user                    Install under ~/.claude/skills/<name>/ (default).
  --local                   Install under ./.claude/skills/<name>/ instead.
  --force                   Overwrite an existing SKILL.md.
`

// runSkill is the dispatcher entry. Wired from cli.go's Run as
// `case "skill":`.
func (a *App) runSkill(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, skillUsage)
		return 2
	}
	switch argv[0] {
	case "new":
		return a.runSkillNew(argv[1:])
	case "list":
		return a.runSkillList(argv[1:])
	case "path":
		return a.runSkillPath(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool skill: unknown subcommand %q\n\n%s", argv[0], skillUsage)
		return 2
	}
}

// skillRoots returns the canonical search roots for installed
// skills. ./.claude/skills (project-local) precedes the user
// directory because a project-scoped override should win when
// both are present — same convention `clawtool` itself uses for
// its config files.
func skillRoots() []string {
	roots := []string{}
	if _, err := os.Stat(".claude/skills"); err == nil {
		roots = append(roots, ".claude/skills")
	}
	if x := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); x != "" {
		roots = append(roots, filepath.Join(x, "skills"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".claude", "skills"))
	}
	return roots
}

// ── new ────────────────────────────────────────────────────────────

func (a *App) runSkillNew(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, "usage: clawtool skill new <name> --description \"...\" [options]\n")
		return 2
	}
	name := argv[0]
	if !skillgen.IsValidName(name) {
		fmt.Fprintf(a.Stderr, "clawtool skill: %q is not a valid skill name (kebab-case, [a-z0-9-]+)\n", name)
		return 2
	}

	var (
		description string
		triggers    string
		root        = skillgen.UserSkillsRoot()
		force       bool
		dryRun      bool
	)
	rest := argv[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--description":
			if i+1 >= len(rest) {
				fmt.Fprint(a.Stderr, "clawtool skill new: --description requires a value\n")
				return 2
			}
			description = rest[i+1]
			i++
		case "--triggers":
			if i+1 >= len(rest) {
				fmt.Fprint(a.Stderr, "clawtool skill new: --triggers requires a value\n")
				return 2
			}
			triggers = rest[i+1]
			i++
		case "--user":
			root = skillgen.UserSkillsRoot()
		case "--local":
			root = skillgen.LocalSkillsRoot()
		case "--force", "-f":
			force = true
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(a.Stderr, "clawtool skill new: unknown flag %q\n", rest[i])
			return 2
		}
	}

	if strings.TrimSpace(description) == "" {
		fmt.Fprintln(a.Stderr, "clawtool skill new: --description is required (the agentskills.io standard mandates a description so agents know when to load the skill)")
		return 2
	}

	skillDir := filepath.Join(root, name)
	skillFile := filepath.Join(skillDir, "SKILL.md")

	exists := false
	if _, err := os.Stat(skillFile); err == nil {
		exists = true
	}
	if exists && !force {
		fmt.Fprintf(a.Stderr, "clawtool skill new: %s already exists (pass --force to overwrite)\n", skillFile)
		return 1
	}

	if dryRun {
		// Validation has already passed (name + description +
		// existing-file check). Print the same preview the
		// non-dry-run path would print on success — minus the
		// "✓ created" verb — so operators can sanity-check the
		// scaffold before committing the writes. Symmetric with
		// `rules new --dry-run` (5824012) and `agents claim
		// --dry-run` (pre-existing).
		verb := "would create"
		if exists && force {
			verb = "would overwrite"
		}
		fmt.Fprintf(a.Stdout, "(dry-run) %s skill %q at %s\n", verb, name, skillDir)
		fmt.Fprintln(a.Stdout, "  ├── SKILL.md")
		fmt.Fprintln(a.Stdout, "  ├── scripts/   (executable helpers)")
		fmt.Fprintln(a.Stdout, "  ├── references/ (reference docs)")
		fmt.Fprintln(a.Stdout, "  └── assets/    (templates / fixtures)")
		fmt.Fprintln(a.Stdout)
		fmt.Fprintf(a.Stdout, "  description: %s\n", description)
		if t := strings.TrimSpace(triggers); t != "" {
			fmt.Fprintf(a.Stdout, "  triggers:    %s\n", t)
		}
		return 0
	}

	body := skillgen.Render(name, description, skillgen.ParseTriggers(triggers))
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool skill new: mkdir %s: %v\n", skillDir, err)
		return 1
	}
	for _, sub := range []string{"scripts", "references", "assets"} {
		_ = os.MkdirAll(filepath.Join(skillDir, sub), 0o755)
		_ = os.WriteFile(filepath.Join(skillDir, sub, ".gitkeep"), nil, 0o644)
	}
	if err := os.WriteFile(skillFile, []byte(body), 0o644); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool skill new: write %s: %v\n", skillFile, err)
		return 1
	}

	fmt.Fprintf(a.Stdout, "✓ created skill %q at %s\n", name, skillDir)
	fmt.Fprintln(a.Stdout, "  ├── SKILL.md")
	fmt.Fprintln(a.Stdout, "  ├── scripts/   (executable helpers)")
	fmt.Fprintln(a.Stdout, "  ├── references/ (reference docs)")
	fmt.Fprintln(a.Stdout, "  └── assets/    (templates / fixtures)")
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "Edit SKILL.md to flesh out the body. Claude Code reloads skills on startup; restart the agent to pick up your new skill.")
	return 0
}

// ── list ───────────────────────────────────────────────────────────

func (a *App) runSkillList(argv []string) int {
	format, residual, err := listfmt.ExtractFlag(argv)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool skill list: %v\n", err)
		return 2
	}
	if len(residual) > 0 {
		fmt.Fprint(a.Stderr, "usage: clawtool skill list [--format table|tsv|json]\n")
		return 2
	}

	cols := listfmt.Cols{Header: []string{"SKILL", "ROOT"}}
	for _, root := range skillRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, e.Name(), "SKILL.md")); err == nil {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			cols.Rows = append(cols.Rows, []string{n, root})
		}
	}

	// Empty case: JSON path always emits an array (`[]`) so
	// pipelines see the same shape across configured /
	// unconfigured machines. table/tsv get the human hint —
	// an interactive `clawtool skill list` on a fresh box should
	// nudge toward `skill new` rather than print just a header.
	if len(cols.Rows) == 0 && format != listfmt.FormatJSON {
		fmt.Fprintln(a.Stdout, "(no skills installed)")
		fmt.Fprintln(a.Stdout, "Try: clawtool skill new my-first-skill --description \"...\"")
		return 0
	}
	if err := listfmt.Render(a.Stdout, format, cols); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool skill list: %v\n", err)
		return 1
	}
	return 0
}

// ── path ───────────────────────────────────────────────────────────

func (a *App) runSkillPath(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(a.Stdout, skillgen.UserSkillsRoot())
		return 0
	}
	name := argv[0]
	for _, root := range skillRoots() {
		candidate := filepath.Join(root, name, "SKILL.md")
		if _, err := os.Stat(candidate); err == nil {
			fmt.Fprintln(a.Stdout, filepath.Dir(candidate))
			return 0
		}
	}
	fmt.Fprintf(a.Stderr, "clawtool skill path: %q not found in %s\n", name, strings.Join(skillRoots(), ", "))
	return 1
}

// All template / validator helpers live in internal/skillgen so
// the SkillNew MCP tool reuses them without a cross-package
// dependency loop.
