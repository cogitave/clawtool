// Package mcpgen implements the `clawtool mcp new` generator
// (ADR-019). Per ADR-007 each language adapter wraps the canonical
// SDK in that ecosystem (mcp-go for Go, fastmcp for Python,
// @modelcontextprotocol/sdk for TypeScript). We never re-implement
// MCP wire protocol — the templates emit ~50 LoC of glue around
// each SDK's documented "register a tool" call.
//
// Lifecycle:
//
//   - Spec: the operator's choices captured by the wizard
//     (language, transport, packaging, tool list).
//   - Plan: a list of Files the language adapter wants written.
//   - Apply: write the files atomically + emit the
//     .clawtool/mcp.toml marker.
//
// Adding a fourth language is one new adapter — every language's
// surface goes through the Adapter interface so the wizard /
// install path don't grow per-language switches.
package mcpgen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Spec is the wizard's output: everything the language adapter
// needs to render a project. Tests construct this directly to
// drive Generate without running huh.
type Spec struct {
	Name        string // kebab-case project name (also dir name)
	Description string // server self-description string
	Language    string // "go" | "python" | "typescript"
	Transport   string // "stdio" | "streamable-http"
	Packaging   string // "native" | "docker"
	Tools       []ToolSpec
	Plugin      bool // generate .claude-plugin/ alongside source
}

// ToolSpec describes one MCP tool the generated server registers.
// Schema is stored as a raw JSON Schema object so adapters can
// emit it verbatim into their language's idiomatic shape.
type ToolSpec struct {
	Name        string // snake_case
	Description string
	Schema      string // JSON object string (e.g. `{"type":"object","properties":{...}}`)
}

// File is a single artifact the adapter wants written. Mode 0o755
// for executable scripts, 0o644 for everything else.
type File struct {
	Path string
	Body string
	Mode os.FileMode
}

// Adapter is the per-language template. Each adapter renders a
// Spec into a Plan; the orchestrator (Generate) writes them.
type Adapter interface {
	Language() string
	Plan(spec Spec) ([]File, error)
}

// adapterRegistry holds the registered adapters. Populated via
// init functions in go_adapter.go / python_adapter.go /
// typescript_adapter.go.
var adapterRegistry = map[string]Adapter{}

// Register adds an adapter to the registry. Panics on duplicate
// language to surface programmer error at boot.
func Register(a Adapter) {
	if a == nil {
		panic("mcpgen: nil adapter")
	}
	lang := strings.ToLower(strings.TrimSpace(a.Language()))
	if lang == "" {
		panic("mcpgen: adapter Language() returned empty string")
	}
	if _, dup := adapterRegistry[lang]; dup {
		panic("mcpgen: adapter for " + lang + " already registered")
	}
	adapterRegistry[lang] = a
}

// Languages returns the registered language names, sorted. Used
// by the wizard's huh.Select to enumerate options.
func Languages() []string {
	out := make([]string, 0, len(adapterRegistry))
	for l := range adapterRegistry {
		out = append(out, l)
	}
	// Stable order: place "go" first so the SDK closest to
	// clawtool's own runtime is the visual default.
	priority := map[string]int{"go": 0, "python": 1, "typescript": 2}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if priority[out[i]] > priority[out[j]] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Generate plans + writes a fresh project rooted at outputDir
// (which becomes outputDir/spec.Name). Refuses to overwrite an
// existing directory — operators delete first or pick a new name.
func Generate(outputDir string, spec Spec) (string, error) {
	if err := validateSpec(spec); err != nil {
		return "", err
	}
	adapter, ok := adapterRegistry[strings.ToLower(spec.Language)]
	if !ok {
		return "", fmt.Errorf("mcpgen: no adapter registered for language %q (have: %s)", spec.Language, strings.Join(Languages(), ", "))
	}
	root := filepath.Join(outputDir, spec.Name)
	if _, err := os.Stat(root); err == nil {
		return "", fmt.Errorf("mcpgen: %s already exists; remove it or pick a different name", root)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("mcpgen: stat %s: %w", root, err)
	}
	files, err := adapter.Plan(spec)
	if err != nil {
		return "", fmt.Errorf("mcpgen: plan: %w", err)
	}
	// Always-on files supplied by the orchestrator (independent
	// of language): the .clawtool/mcp.toml marker, README, and
	// the Claude plugin manifest if requested. Adapters can
	// override by emitting the same path — we'd rather a Go
	// adapter that wants a custom README win the conflict.
	files = mergeFiles(commonFiles(spec), files)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("mcpgen: mkdir %s: %w", root, err)
	}
	for _, f := range files {
		if err := writeFile(root, f); err != nil {
			return "", err
		}
	}
	return root, nil
}

func validateSpec(s Spec) error {
	if !isValidProjectName(s.Name) {
		return errors.New("mcpgen: name must match [a-z0-9][a-z0-9-]{1,63}")
	}
	if strings.TrimSpace(s.Description) == "" {
		return errors.New("mcpgen: description is required")
	}
	switch strings.ToLower(s.Language) {
	case "go", "python", "typescript":
	default:
		return fmt.Errorf("mcpgen: unknown language %q (want go | python | typescript)", s.Language)
	}
	switch strings.ToLower(s.Transport) {
	case "", "stdio", "streamable-http":
	default:
		return fmt.Errorf("mcpgen: unknown transport %q (want stdio | streamable-http)", s.Transport)
	}
	switch strings.ToLower(s.Packaging) {
	case "", "native", "docker":
	default:
		return fmt.Errorf("mcpgen: unknown packaging %q (want native | docker)", s.Packaging)
	}
	if len(s.Tools) == 0 {
		return errors.New("mcpgen: at least one tool is required")
	}
	for i, t := range s.Tools {
		if !isValidToolName(t.Name) {
			return fmt.Errorf("mcpgen: tool[%d] name %q must match snake_case [a-z][a-z0-9_]*", i, t.Name)
		}
		if strings.TrimSpace(t.Description) == "" {
			return fmt.Errorf("mcpgen: tool[%d] description is required", i)
		}
	}
	return nil
}

func isValidProjectName(s string) bool {
	if len(s) < 2 || len(s) > 64 {
		return false
	}
	if !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

func isValidToolName(s string) bool {
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

// writeFile creates `root/file.Path` with file.Body. Refuses to
// escape `root` via traversal — adapters must use forward-slash
// relative paths only.
func writeFile(root string, file File) error {
	if filepath.IsAbs(file.Path) {
		return fmt.Errorf("mcpgen: refused absolute file path %q", file.Path)
	}
	clean := filepath.Clean(file.Path)
	if strings.HasPrefix(clean, "..") {
		return fmt.Errorf("mcpgen: refused traversal in file path %q", file.Path)
	}
	target := filepath.Join(root, clean)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := file.Mode
	if mode == 0 {
		mode = 0o644
	}
	return os.WriteFile(target, []byte(file.Body), mode)
}

// mergeFiles overlays `defaults` with `overrides` — when both
// supply the same path, override wins. Order preserved so
// adapter-supplied files render before defaults in the final
// listing.
func mergeFiles(defaults, overrides []File) []File {
	overridden := map[string]bool{}
	for _, f := range overrides {
		overridden[filepath.Clean(f.Path)] = true
	}
	out := make([]File, 0, len(defaults)+len(overrides))
	for _, f := range overrides {
		out = append(out, f)
	}
	for _, f := range defaults {
		if !overridden[filepath.Clean(f.Path)] {
			out = append(out, f)
		}
	}
	return out
}
