package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/cogitave/clawtool/internal/mcpgen"
)

func TestMcpNewWizard_YesPath_GeneratesProject(t *testing.T) {
	tmp := t.TempDir()
	captured := captureLines{}
	d := mcpgenDeps{
		runForm:  func(*huh.Form) error { return nil }, // never called in --yes
		generate: mcpgen.Generate,
		stdoutLn: captured.recorder(),
		stderrLn: func(string) {},
	}
	if err := runMcpNewWizardWithDeps(context.Background(), "smoke-srv", tmp, true, d); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	root := filepath.Join(tmp, "smoke-srv")
	for _, rel := range []string{"go.mod", "Makefile", "cmd/smoke-srv/main.go", ".clawtool/mcp.toml", "README.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	output := strings.Join(captured.lines, "\n")
	if !strings.Contains(output, "scaffolded") {
		t.Errorf("stdout should announce scaffold; got:\n%s", output)
	}
	if !strings.Contains(output, "clawtool mcp install") {
		t.Errorf("stdout should hint at mcp install; got:\n%s", output)
	}
}

func TestMcpNewWizard_RejectsBadName(t *testing.T) {
	d := mcpgenDeps{
		runForm:  func(*huh.Form) error { return nil },
		generate: mcpgen.Generate,
		stdoutLn: func(string) {},
		stderrLn: func(string) {},
	}
	if err := runMcpNewWizardWithDeps(context.Background(), "Has Space", t.TempDir(), true, d); err == nil {
		t.Fatal("expected validation rejection for bad name")
	}
}

func TestMcpNewWizard_RefusesExistingDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := mcpgenDeps{
		runForm:  func(*huh.Form) error { return nil },
		generate: mcpgen.Generate,
		stdoutLn: func(string) {},
		stderrLn: func(string) {},
	}
	err := runMcpNewWizardWithDeps(context.Background(), "occupied", tmp, true, d)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists', got %v", err)
	}
}

func TestMcpList_FindsScaffoldedProject(t *testing.T) {
	tmp := t.TempDir()
	// Generate a real scaffold so the walker finds the marker.
	if _, err := mcpgen.Generate(tmp, mcpgen.Spec{
		Name:        "discover-me",
		Description: "x",
		Language:    "go",
		Transport:   "stdio",
		Packaging:   "native",
		Tools: []mcpgen.ToolSpec{{
			Name: "ping", Description: "ping", Schema: `{"type":"object"}`,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	projects, err := walkForMcpProjects(tmp)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range projects {
		if p.name == "discover-me" {
			found = true
			if p.language != "go" {
				t.Errorf("language read wrong: %q", p.language)
			}
		}
	}
	if !found {
		t.Errorf("walker missed scaffolded project: %+v", projects)
	}
}

// captureLines is a tiny stdout sink for the wizard tests.
type captureLines struct {
	lines []string
}

func (c *captureLines) recorder() func(string) {
	return func(s string) { c.lines = append(c.lines, s) }
}
