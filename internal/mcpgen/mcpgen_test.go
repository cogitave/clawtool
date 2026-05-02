package mcpgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleSpec(lang string) Spec {
	return Spec{
		Name:        "sample-srv",
		Description: "Generator smoke test",
		Language:    lang,
		Transport:   "stdio",
		Packaging:   "native",
		Plugin:      true,
		Tools: []ToolSpec{
			{
				Name:        "echo_back",
				Description: "Return the input string verbatim.",
				Schema:      `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`,
			},
		},
	}
}

func TestLanguagesRegistered(t *testing.T) {
	got := Languages()
	want := map[string]bool{"go": true, "python": true, "typescript": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want languages %v", got, want)
	}
	if got[0] != "go" {
		t.Errorf("Languages() should put go first, got %v", got)
	}
}

func TestGenerate_Go_HappyPath(t *testing.T) {
	root := t.TempDir()
	out, err := Generate(root, sampleSpec("go"))
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, out, "go.mod")
	mustExist(t, out, "Makefile")
	mustExist(t, out, "cmd/sample-srv/main.go")
	mustExist(t, out, "internal/tools/example.go")
	mustExist(t, out, "internal/tools/example_test.go")
	mustExist(t, out, ".clawtool/mcp.toml")
	mustExist(t, out, "README.md")
	mustExist(t, out, ".gitignore")
	mustExist(t, out, ".claude-plugin/plugin.json")
	mustExist(t, out, ".claude-plugin/marketplace.json.template")

	// The example tool's RegisterEchoBack identifier must
	// appear in main.go AND example.go.
	mainBody := mustRead(t, out, "cmd/sample-srv/main.go")
	if !strings.Contains(mainBody, "tools.RegisterEchoBack(s)") {
		t.Errorf("main.go missing RegisterEchoBack call: %s", mainBody)
	}
	if !strings.Contains(mainBody, `"sample-srv"`) {
		t.Errorf("main.go missing server name literal: %s", mainBody)
	}
	example := mustRead(t, out, "internal/tools/example.go")
	if !strings.Contains(example, "func RegisterEchoBack") {
		t.Errorf("example.go missing RegisterEchoBack: %s", example)
	}
	if !strings.Contains(example, `"echo_back"`) {
		t.Errorf("example.go missing tool name: %s", example)
	}

	// mcp.toml should round-trip name + tool name.
	toml := mustRead(t, out, ".clawtool/mcp.toml")
	if !strings.Contains(toml, `name        = "sample-srv"`) {
		t.Errorf("mcp.toml missing project name: %s", toml)
	}
	if !strings.Contains(toml, `name        = "echo_back"`) {
		t.Errorf("mcp.toml missing tool name: %s", toml)
	}
}

func TestGenerate_Python_HappyPath(t *testing.T) {
	root := t.TempDir()
	out, err := Generate(root, sampleSpec("python"))
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, out, "pyproject.toml")
	mustExist(t, out, "Makefile")
	mustExist(t, out, "src/sample_srv/__init__.py")
	mustExist(t, out, "src/sample_srv/__main__.py")
	mustExist(t, out, "src/sample_srv/server.py")
	mustExist(t, out, "src/sample_srv/tools/__init__.py")
	mustExist(t, out, "src/sample_srv/tools/echo_back.py")
	mustExist(t, out, "tests/test_smoke.py")

	server := mustRead(t, out, "src/sample_srv/server.py")
	if !strings.Contains(server, `FastMCP("sample-srv")`) {
		t.Errorf("server.py missing FastMCP init: %s", server)
	}
	tool := mustRead(t, out, "src/sample_srv/tools/echo_back.py")
	if !strings.Contains(tool, `name="echo_back"`) {
		t.Errorf("tool file missing tool name: %s", tool)
	}
}

func TestGenerate_TypeScript_HappyPath(t *testing.T) {
	root := t.TempDir()
	out, err := Generate(root, sampleSpec("typescript"))
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, out, "package.json")
	mustExist(t, out, "tsconfig.json")
	mustExist(t, out, "Makefile")
	mustExist(t, out, "src/server.ts")
	mustExist(t, out, "src/tools/echo_back.ts")
	mustExist(t, out, "test/example.test.ts")

	pkg := mustRead(t, out, "package.json")
	if !strings.Contains(pkg, `"@modelcontextprotocol/sdk"`) {
		t.Errorf("package.json missing SDK dep: %s", pkg)
	}
	srv := mustRead(t, out, "src/server.ts")
	if !strings.Contains(srv, `register_echo_back(server)`) {
		t.Errorf("server.ts missing register call: %s", srv)
	}
}

func TestGenerate_Docker_OptIn(t *testing.T) {
	for _, lang := range []string{"go", "python", "typescript"} {
		root := t.TempDir()
		s := sampleSpec(lang)
		s.Packaging = "docker"
		out, err := Generate(root, s)
		if err != nil {
			t.Fatal(err)
		}
		mustExist(t, out, "Dockerfile")
		// And without docker, the file is absent:
		root2 := t.TempDir()
		s2 := sampleSpec(lang)
		s2.Name = s2.Name + "-nodocker"
		out2, err := Generate(root2, s2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(out2, "Dockerfile")); err == nil {
			t.Errorf("[%s] native packaging should NOT emit Dockerfile", lang)
		}
	}
}

func TestGenerate_NoPlugin_OmitsClaudeFolder(t *testing.T) {
	root := t.TempDir()
	s := sampleSpec("go")
	s.Plugin = false
	out, err := Generate(root, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, ".claude-plugin")); err == nil {
		t.Errorf("Plugin=false should NOT emit .claude-plugin/")
	}
}

func TestGenerate_RefusesExistingDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sample-srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Generate(root, sampleSpec("go"))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' refusal, got %v", err)
	}
}

func TestValidateSpec_RejectsBadName(t *testing.T) {
	for _, bad := range []string{"", "X", "Has Space", "UPPER", "../escape", "a"} {
		s := sampleSpec("go")
		s.Name = bad
		if err := validateSpec(s); err == nil {
			t.Errorf("expected error for name %q", bad)
		}
	}
}

func TestValidateSpec_RejectsBadToolName(t *testing.T) {
	s := sampleSpec("go")
	s.Tools[0].Name = "BadCase"
	if err := validateSpec(s); err == nil {
		t.Error("expected snake_case validator to reject BadCase")
	}
}

func TestValidateSpec_RequiresAtLeastOneTool(t *testing.T) {
	s := sampleSpec("go")
	s.Tools = nil
	if err := validateSpec(s); err == nil {
		t.Error("expected error when Tools is empty")
	}
}

func TestValidateSpec_RejectsUnknownLanguage(t *testing.T) {
	s := sampleSpec("rust")
	if err := validateSpec(s); err == nil {
		t.Error("expected error for unknown language")
	}
}

// TestMcpNew_GeneratesCIWorkflowPerLang asserts that every scaffold
// — Go / Python / TypeScript — drops a `.github/workflows/ci.yml`
// containing the language-appropriate step names. ADR-019
// §Resolved 2026-05-02 mandates a minimal CI template per language
// generated unconditionally.
func TestMcpNew_GeneratesCIWorkflowPerLang(t *testing.T) {
	cases := []struct {
		language  string
		stepNames []string // substrings every workflow must contain
	}{
		{
			language: "go",
			stepNames: []string{
				"actions/setup-go@v6",
				"go-version-file: go.mod",
				"go mod download",
				"gofmt -l .",
				"go vet ./...",
				"go test -race ./...",
			},
		},
		{
			language: "python",
			stepNames: []string{
				"actions/setup-python@v6",
				"python-version-file: pyproject.toml",
				"pip install -e .[dev]",
				"ruff check .",
				"pytest",
			},
		},
		{
			language: "typescript",
			stepNames: []string{
				"actions/setup-node@v6",
				"pnpm install --frozen-lockfile",
				"pnpm test",
				"pnpm lint",
			},
		},
	}
	// Bits every workflow must share regardless of language.
	universal := []string{
		"name: CI",
		"on:",
		"branches: [main]",
		"pull_request:",
		"concurrency:",
		"cancel-in-progress: true",
	}
	for _, tc := range cases {
		t.Run(tc.language, func(t *testing.T) {
			root := t.TempDir()
			out, err := Generate(root, sampleSpec(tc.language))
			if err != nil {
				t.Fatalf("Generate(%s): %v", tc.language, err)
			}
			mustExist(t, out, ".github/workflows/ci.yml")
			body := mustRead(t, out, ".github/workflows/ci.yml")
			for _, want := range universal {
				if !strings.Contains(body, want) {
					t.Errorf("[%s] ci.yml missing %q\n---\n%s", tc.language, want, body)
				}
			}
			for _, want := range tc.stepNames {
				if !strings.Contains(body, want) {
					t.Errorf("[%s] ci.yml missing %q\n---\n%s", tc.language, want, body)
				}
			}
		})
	}
}

// ── helpers ─────────────────────────────────────────────────────

func mustExist(t *testing.T, root, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		t.Fatalf("missing %s: %v", rel, err)
	}
}

func mustRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}
