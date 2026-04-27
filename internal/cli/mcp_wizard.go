// Package cli — `clawtool mcp new` interactive wizard (ADR-019).
//
// huh.Form sequence collects the operator's spec, hands it to
// internal/mcpgen which renders + writes the project. Tests
// substitute mcpgenDeps to drive the wizard without hitting disk.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cogitave/clawtool/internal/mcpgen"
)

// mcpgenDeps lets tests stub the side effects.
type mcpgenDeps struct {
	runForm  func(*huh.Form) error
	generate func(outputDir string, spec mcpgen.Spec) (string, error)
	stdoutLn func(string)
	stderrLn func(string)
}

func (a *App) runMcpNewWizard(argv []string) error {
	var (
		yes       bool
		outputDir string
		name      string
	)
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--yes", "-y":
			yes = true
		case "--output", "-o":
			if i+1 >= len(argv) {
				return errors.New("--output requires a path")
			}
			outputDir = argv[i+1]
			i++
		default:
			if name != "" {
				return fmt.Errorf("unexpected arg %q", v)
			}
			name = v
		}
	}
	if name == "" {
		return errors.New("usage: clawtool mcp new <project-name> [--output <dir>] [--yes]")
	}
	if outputDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		outputDir = cwd
	}
	d := mcpgenDeps{
		runForm:  func(f *huh.Form) error { return f.Run() },
		generate: mcpgen.Generate,
		stdoutLn: func(s string) { fmt.Fprintln(a.Stdout, s) },
		stderrLn: func(s string) { fmt.Fprintln(a.Stderr, s) },
	}
	return runMcpNewWizardWithDeps(context.Background(), name, outputDir, yes, d)
}

func runMcpNewWizardWithDeps(_ context.Context, name, outputDir string, yes bool, d mcpgenDeps) error {
	spec := mcpgen.Spec{
		Name:      name,
		Language:  "go",
		Transport: "stdio",
		Packaging: "native",
		Plugin:    true,
	}

	if !yes {
		intro := huh.NewForm(huh.NewGroup(
			huh.NewNote().
				Title("clawtool mcp new — MCP server scaffolder").
				Description("Generates a fresh MCP server project (ADR-019). Per ADR-007\nthe scaffold wraps the canonical SDK in your chosen language —\nmcp-go for Go, fastmcp for Python, @modelcontextprotocol/sdk\nfor TypeScript. We never re-implement the wire protocol.\n\nThe wizard asks for description, language, transport,\npackaging, and your first tool. You can register the\nresult with `clawtool mcp install . --as <name>` once it builds."),
			huh.NewInput().
				Title("Description").
				Description("One sentence — becomes the server's self-description.").
				Value(&spec.Description).
				Validate(nonEmpty),
			huh.NewSelect[string]().
				Title("Language").
				Options(
					huh.NewOption("Go (mark3labs/mcp-go) — single static binary", "go"),
					huh.NewOption("Python (fastmcp) — concise, decorator-driven", "python"),
					huh.NewOption("TypeScript (@modelcontextprotocol/sdk) — npm distribution", "typescript"),
				).
				Value(&spec.Language),
			huh.NewSelect[string]().
				Title("Transport").
				Options(
					huh.NewOption("stdio — installable as a clawtool source (recommended)", "stdio"),
					huh.NewOption("streamable-HTTP — standalone network service", "streamable-http"),
				).
				Value(&spec.Transport),
			huh.NewSelect[string]().
				Title("Packaging").
				Options(
					huh.NewOption("native — language-default (binary / pip / npm)", "native"),
					huh.NewOption("docker — multi-stage Dockerfile alongside source", "docker"),
				).
				Value(&spec.Packaging),
			huh.NewConfirm().
				Title("Generate Claude Code plugin manifest?").
				Description(".claude-plugin/plugin.json + marketplace.json.template — operators manage the publish lifecycle themselves.").
				Affirmative("Yes, generate manifest").
				Negative("No").
				Value(&spec.Plugin),
		))
		if err := d.runForm(intro); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return errors.New("aborted")
			}
			return err
		}

		// First tool capture.
		var first mcpgen.ToolSpec
		toolForm := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("First tool name (snake_case)").
				Description("Operators frequently start with one tool and add more later.").
				Value(&first.Name).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("required")
					}
					if !mcpgenIsSnake(s) {
						return errors.New("must match snake_case [a-z][a-z0-9_]*")
					}
					return nil
				}),
			huh.NewText().
				Title("First tool description").
				Description("What does this tool do? Keep it one paragraph.").
				Value(&first.Description).
				Validate(nonEmpty),
		))
		if err := d.runForm(toolForm); err != nil {
			return err
		}
		first.Schema = `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`
		spec.Tools = []mcpgen.ToolSpec{first}
	} else {
		// --yes path: minimal viable defaults.
		if spec.Description == "" {
			spec.Description = fmt.Sprintf("MCP server scaffolded by clawtool mcp new (project %q).", name)
		}
		spec.Tools = []mcpgen.ToolSpec{{
			Name:        "echo_back",
			Description: "Return the input string verbatim. Replace with your real tool.",
			Schema:      `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`,
		}}
	}

	root, err := d.generate(outputDir, spec)
	if err != nil {
		return err
	}

	d.stdoutLn(fmt.Sprintf("✓ scaffolded %s", root))
	d.stdoutLn("")
	d.stdoutLn("Next steps:")
	switch strings.ToLower(spec.Language) {
	case "go":
		d.stdoutLn(fmt.Sprintf("  cd %s && make build && ./bin/%s", filepath.Base(root), spec.Name))
	case "python":
		d.stdoutLn(fmt.Sprintf("  cd %s && pip install -e . && python -m %s", filepath.Base(root), strings.ReplaceAll(spec.Name, "-", "_")))
	case "typescript":
		d.stdoutLn(fmt.Sprintf("  cd %s && npm install && npm run build && node dist/server.js", filepath.Base(root)))
	}
	d.stdoutLn(fmt.Sprintf("  clawtool mcp install %s --as %s", root, spec.Name))
	d.stdoutLn("")
	d.stdoutLn("Edit internal/tools/<your-tool> to replace the echo placeholder.")
	d.stdoutLn("Plugin manifest at .claude-plugin/plugin.json — operator-managed.")
	return nil
}

func mcpgenIsSnake(s string) bool {
	if len(s) == 0 {
		return false
	}
	if !(s[0] >= 'a' && s[0] <= 'z') {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}
