// Package cli — `clawtool mcp install` (ADR-019).
//
// Reads `.clawtool/mcp.toml` from the project at <path>, derives
// the launch command from the project's language + transport,
// writes a [sources.<name>] block into ~/.config/clawtool/config.toml.
// Same surface as `clawtool source add` for catalog entries —
// just auto-discovers the command instead of asking.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/cogitave/clawtool/internal/config"
)

// mcpProject mirrors the [project] block in .clawtool/mcp.toml.
type mcpProject struct {
	Project struct {
		Name        string `toml:"name"`
		Description string `toml:"description"`
		Language    string `toml:"language"`
		Transport   string `toml:"transport"`
		Packaging   string `toml:"packaging"`
		ManagedBy   string `toml:"managed_by"`
	} `toml:"project"`
}

func (a *App) runMcpInstall(argv []string) error {
	var (
		path  string
		alias string
	)
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch v {
		case "--as":
			if i+1 >= len(argv) {
				return errors.New("--as requires a value")
			}
			alias = argv[i+1]
			i++
		default:
			if path != "" {
				return fmt.Errorf("unexpected arg %q", v)
			}
			path = v
		}
	}
	if path == "" {
		return errors.New("usage: clawtool mcp install <path> [--as <instance>]")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	proj, err := readMcpProject(abs)
	if err != nil {
		return err
	}
	if alias == "" {
		alias = proj.Project.Name
	}
	if alias == "" {
		return errors.New("project name missing in .clawtool/mcp.toml; pass --as <instance>")
	}
	command, err := launchCommandFor(abs, proj)
	if err != nil {
		return err
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		return err
	}
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.Source{}
	}
	if _, exists := cfg.Sources[alias]; exists {
		return fmt.Errorf("source %q already exists in %s — pick a different --as or remove it first", alias, cfgPath)
	}
	cfg.Sources[alias] = config.Source{Type: "mcp", Command: command}

	if err := writeFullConfigAtomic(cfgPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ registered [sources.%s] in %s\n", alias, cfgPath)
	fmt.Fprintf(a.Stdout, "  command: %s\n", strings.Join(command, " "))
	fmt.Fprintln(a.Stdout, "")
	fmt.Fprintln(a.Stdout, "Restart `clawtool serve` (or your MCP client) to pick up the new source.")
	return nil
}

func readMcpProject(absDir string) (mcpProject, error) {
	marker := filepath.Join(absDir, ".clawtool", "mcp.toml")
	body, err := os.ReadFile(marker)
	if err != nil {
		return mcpProject{}, fmt.Errorf("read %s: %w (is this a clawtool mcp project?)", marker, err)
	}
	var proj mcpProject
	if err := toml.Unmarshal(body, &proj); err != nil {
		return mcpProject{}, fmt.Errorf("parse %s: %w", marker, err)
	}
	return proj, nil
}

// launchCommandFor derives the argv that should land in
// [sources.X].command. We bake in the absolute project path so
// the command works no matter where `clawtool serve` is invoked
// from.
func launchCommandFor(absProjectDir string, proj mcpProject) ([]string, error) {
	pkg := strings.ReplaceAll(proj.Project.Name, "-", "_")
	if pkg == "" {
		pkg = "server"
	}
	switch strings.ToLower(proj.Project.Packaging) {
	case "docker":
		// Operator builds the image themselves; we register the
		// run command using the project name as the image tag.
		return []string{"docker", "run", "-i", "--rm", proj.Project.Name + ":latest"}, nil
	}
	switch strings.ToLower(proj.Project.Language) {
	case "go":
		return []string{filepath.Join(absProjectDir, "bin", proj.Project.Name)}, nil
	case "python":
		return []string{"python", "-m", pkg}, nil
	case "typescript":
		return []string{"node", filepath.Join(absProjectDir, "dist", "server.js")}, nil
	}
	return nil, fmt.Errorf("unknown language %q in %s/.clawtool/mcp.toml", proj.Project.Language, absProjectDir)
}

// writeFullConfigAtomic mirrors config.AppendBytes' atomic
// temp+rename, but takes a whole Config (not a TOML fragment).
// Avoids round-tripping through MarshalForAppend.
func writeFullConfigAtomic(path string, cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	body, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
