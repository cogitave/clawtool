package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/cogitave/clawtool/internal/catalog"
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
	if err := runMcpNewWizardWithDeps(context.Background(), "smoke-srv", tmp, "", true, d); err != nil {
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
	if err := runMcpNewWizardWithDeps(context.Background(), "Has Space", t.TempDir(), "", true, d); err == nil {
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
	err := runMcpNewWizardWithDeps(context.Background(), "occupied", tmp, "", true, d)
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

// stubLookup wires a fake catalog into mcpgenDeps so wizard tests don't
// depend on whatever entries happen to live in the embedded TOML.
func stubLookup(entries map[string]catalog.Entry) func(string) (catalog.Entry, bool, error) {
	return func(name string) (catalog.Entry, bool, error) {
		e, ok := entries[name]
		return e, ok, nil
	}
}

func TestMcpNew_FromSourcePrefillsWizard(t *testing.T) {
	tmp := t.TempDir()
	captured := captureLines{}
	entries := map[string]catalog.Entry{
		"github": {
			Description: "GitHub: issues, PRs, code search, repository operations",
			Runtime:     "npx",
			Package:     "@modelcontextprotocol/server-github",
			RequiredEnv: []string{"GITHUB_TOKEN"},
			AuthHint:    "Generate a token at https://github.com/settings/tokens",
		},
	}
	var capturedSpec mcpgen.Spec
	d := mcpgenDeps{
		runForm: func(*huh.Form) error { return nil },
		generate: func(outputDir string, spec mcpgen.Spec) (string, error) {
			capturedSpec = spec
			return filepath.Join(outputDir, spec.Name), nil
		},
		stdoutLn:    captured.recorder(),
		stderrLn:    func(string) {},
		lookupEntry: stubLookup(entries),
	}
	// Drive the wizard with --yes so we deterministically see the
	// catalog defaults flow into the spec without huh interaction.
	if err := runMcpNewWizardWithDeps(context.Background(), "my-fork", tmp, "github", true, d); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	if capturedSpec.Description != entries["github"].Description {
		t.Errorf("description not pre-filled from catalog: got %q want %q",
			capturedSpec.Description, entries["github"].Description)
	}
	if len(capturedSpec.Tools) != 1 {
		t.Fatalf("expected one tool, got %d", len(capturedSpec.Tools))
	}
	if capturedSpec.Tools[0].Description != entries["github"].Description {
		t.Errorf("first tool description not pre-filled from catalog: got %q",
			capturedSpec.Tools[0].Description)
	}
}

func TestMcpNew_FromSourceUnknownErrors(t *testing.T) {
	captured := captureLines{}
	d := mcpgenDeps{
		runForm:     func(*huh.Form) error { return nil },
		generate:    mcpgen.Generate,
		stdoutLn:    captured.recorder(),
		stderrLn:    func(string) {},
		lookupEntry: stubLookup(map[string]catalog.Entry{}), // empty catalog
	}
	err := runMcpNewWizardWithDeps(context.Background(), "my-fork", t.TempDir(), "nonexistent", true, d)
	if err == nil {
		t.Fatal("expected error for unknown --from-source entry")
	}
	msg := err.Error()
	for _, want := range []string{"nonexistent", "catalog", "source list"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing hint substring %q", msg, want)
		}
	}
}
