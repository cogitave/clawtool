// Package cli — `clawtool mcp` subcommand surface (ADR-019).
//
// v0.17 fills in `new`, `list`, `run`, `build`, `install`. The
// `new` verb runs the huh.Form wizard implemented in
// mcp_wizard.go; `install` lives in mcp_install.go; this file
// keeps the dispatcher + the read-only `list` walker.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const mcpUsage = `Usage:
  clawtool mcp new <project-name> [--output <dir>] [--yes]
                                   Generate a new MCP server (Go / Python /
                                   TypeScript) in <project-name>/. Wizard
                                   asks for description, language, transport,
                                   packaging, first tool. ADR-019 — generator
                                   ships v0.17.
  clawtool mcp list [--root <dir>] List MCP server projects under <dir>
                                   (default cwd). Detects via the
                                   .clawtool/mcp.toml marker.
  clawtool mcp run <path>          Start the project's MCP server in dev
                                   mode (stdio). Defers to v0.17.
  clawtool mcp build <path>        Compile / package the project. Defers
                                   to v0.17.
  clawtool mcp install <path> [--as <instance>]
                                   Build + register the project as
                                   [sources.<instance>] in config.toml.
                                   Defers to v0.17.

Sister surface: clawtool skill (Agent Skills, agentskills.io).
mcp = MCP server source code; skill = agent-side skill folder.

Full design: docs/mcp-authoring.md (lands with the v0.17 generator)
and wiki/decisions/019-mcp-authoring-scaffolder.md (accepted).
`

// runMcp is wired from cli.go's main switch. v0.16.4 implements
// `list` natively + leaves the other verbs for v0.17.
func (a *App) runMcp(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, mcpUsage)
		return 2
	}
	switch argv[0] {
	case "new":
		return dispatchPlainErr(a.Stderr, "mcp new", a.runMcpNewWizard(argv[1:]))
	case "list":
		return dispatchPlainErr(a.Stderr, "mcp list", a.McpList(argv[1:]))
	case "run":
		return dispatchPlainErr(a.Stderr, "mcp run", a.runMcpRun(argv[1:]))
	case "build":
		return dispatchPlainErr(a.Stderr, "mcp build", a.runMcpBuild(argv[1:]))
	case "install":
		return dispatchPlainErr(a.Stderr, "mcp install", a.runMcpInstall(argv[1:]))
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, mcpUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool mcp: unknown subcommand %q\n\n%s", argv[0], mcpUsage)
		return 2
	}
}

// dispatchPlainErr is a tiny helper so error printing is uniform
// across the new verbs. Not promoted to a package helper because
// the existing `dispatchPortalErr` already has its own shape.
func dispatchPlainErr(stderr io.Writer, verb string, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "clawtool %s: %v\n", verb, err)
	return 1
}

// ── mcp list (real walker, ships v0.17) ──────────────────────────

// McpList walks `root` (default cwd) for `.clawtool/mcp.toml`
// markers and prints one line per project. Skips node_modules /
// vendor / .git so a recursive walk doesn't melt on a typical
// repo.
func (a *App) McpList(argv []string) error {
	root := "."
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--root" && i+1 < len(argv) {
			root = argv[i+1]
			i++
		}
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("abs root: %w", err)
	}
	projects, err := walkForMcpProjects(abs)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintf(a.Stdout, "(no MCP server projects under %s — `clawtool mcp new <name>` to scaffold one)\n", abs)
		fmt.Fprintln(a.Stdout, "  marker: <project>/.clawtool/mcp.toml")
		return nil
	}
	fmt.Fprintf(a.Stdout, "%-32s %-12s %s\n", "PROJECT", "LANGUAGE", "PATH")
	for _, p := range projects {
		fmt.Fprintf(a.Stdout, "%-32s %-12s %s\n", p.name, p.language, p.path)
	}
	return nil
}

type mcpProjectInfo struct {
	name     string
	language string
	path     string
}

// walkForMcpProjects returns every directory under root that
// contains a .clawtool/mcp.toml marker. Skips node_modules / .git /
// vendor / dist / build / .venv to keep the walk bounded.
func walkForMcpProjects(root string) ([]mcpProjectInfo, error) {
	var out []mcpProjectInfo
	skip := map[string]bool{
		"node_modules": true, ".git": true, "vendor": true,
		"dist": true, "build": true, ".venv": true, "__pycache__": true,
	}
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if info.IsDir() && skip[info.Name()] {
			return filepath.SkipDir
		}
		if info.IsDir() && info.Name() == ".clawtool" {
			marker := filepath.Join(path, "mcp.toml")
			if _, err := os.Stat(marker); err == nil {
				projDir := filepath.Dir(path)
				if proj, perr := readMcpProject(projDir); perr == nil {
					out = append(out, mcpProjectInfo{
						name:     proj.Project.Name,
						language: proj.Project.Language,
						path:     projDir,
					})
				}
			}
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// ── mcp run / mcp build (thin wrappers around the project's
// own Makefile so we don't replicate per-language toolchains) ─

func (a *App) runMcpRun(argv []string) error {
	if len(argv) == 0 {
		return errors.New("usage: clawtool mcp run <path>")
	}
	return invokeMakefileTarget(a, argv[0], "run")
}

func (a *App) runMcpBuild(argv []string) error {
	if len(argv) == 0 {
		return errors.New("usage: clawtool mcp build <path>")
	}
	return invokeMakefileTarget(a, argv[0], "build")
}

// invokeMakefileTarget shells out to `make <target>` in the
// project dir. Per ADR-007 we don't reinvent build orchestration —
// every scaffold ships a Makefile with build / run / install /
// test, and `mcp run` / `mcp build` just shim through.
func invokeMakefileTarget(a *App, projectPath, target string) error {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(abs, "Makefile")); err != nil {
		return fmt.Errorf("no Makefile at %s — was this directory generated by `clawtool mcp new`?", abs)
	}
	cmd := exec.Command("make", target)
	cmd.Dir = abs
	cmd.Stdout = a.Stdout
	cmd.Stderr = a.Stderr
	return cmd.Run()
}

// errors / io / strings imports keep the file building when the
// stub helpers above are removed.
var (
	_ = errors.New
	_ = io.Discard
)
