package cli

// `clawtool playbook` is the surface that exposes external workflow
// loaders to the operator. Phase 1 ships exactly one subcommand:
// `list-archon`, a read-only listing of every workflow under the
// current repo's `.archon/workflows/`. Phase 2 will add `run`.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/playbooks"
)

// runPlaybook dispatches `clawtool playbook <subcommand>`. Mirrors
// the runApm shape so a future `playbook run` slot in cleanly.
func (a *App) runPlaybook(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, playbookUsage)
		return 2
	}
	switch argv[0] {
	case "list-archon":
		return a.runPlaybookListArchon(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool playbook: unknown subcommand %q\n\n%s", argv[0], playbookUsage)
		return 2
	}
}

func (a *App) runPlaybookListArchon(argv []string) int {
	fs := flag.NewFlagSet("playbook list-archon", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	dir := fs.String("dir", "", "Repo root to scan. Default: current working directory.")
	format := fs.String("format", "text", "Output format: text | json.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{"dir": true, "format": true})); err != nil {
		return 2
	}
	root := *dir
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool playbook list-archon: %v\n", err)
			return 1
		}
		root = cwd
	}

	wfs, err := playbooks.LoadFromDir(root)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool playbook list-archon: %v\n", err)
		return 1
	}

	switch *format {
	case "json":
		// Stable shape: `[{name, description, path, node_count}]`.
		// We don't expose the full Nodes slice yet — phase 2's
		// `playbook run` will need it but list shouldn't bloat.
		type row struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			NodeCount   int    `json:"node_count"`
		}
		out := make([]row, 0, len(wfs))
		for _, w := range wfs {
			out = append(out, row{
				Name:        w.Name,
				Description: w.Description,
				Path:        w.Path,
				NodeCount:   len(w.Nodes),
			})
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool playbook list-archon: encode: %v\n", err)
			return 1
		}
		return 0
	case "text":
		if len(wfs) == 0 {
			fmt.Fprintf(a.Stdout, "no Archon workflows under %s/.archon/workflows\n", root)
			return 0
		}
		fmt.Fprintf(a.Stdout, "Archon workflows in %s/.archon/workflows:\n", root)
		for _, w := range wfs {
			fmt.Fprintf(a.Stdout, "  %s\n", w.Summary())
		}
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool playbook list-archon: unknown --format %q (want text | json)\n", *format)
		return 2
	}
}

const playbookUsage = `Usage:
  clawtool playbook list-archon [--dir <path>] [--format <text|json>]
                            List Archon (coleam00/Archon) DAG workflows
                            under <path>/.archon/workflows/. Read-only:
                            phase 1 parses + surfaces, phase 2 will wire
                            execution. JSON output carries name,
                            description, path, node_count.

  Default --dir is the current working directory. Default --format is text.
`
