package cli

// Package-level note: targets the microsoft/apm manifest schema
// "Working Draft 0.1" dated 2026-03-06 — see
// https://github.com/microsoft/apm/blob/main/docs/src/content/docs/reference/manifest-schema.md
// Any fields not listed there are ignored on import.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrApmManifestMissing is returned when the apm.yml the operator
// pointed at doesn't exist on disk. Tests pin this typed error.
var ErrApmManifestMissing = errors.New("apm: manifest file not found")

// ErrApmYAMLParse wraps a yaml.v3 decode failure so tests can
// assert errors.Is without depending on the upstream's error type.
var ErrApmYAMLParse = errors.New("apm: yaml parse error")

// apmManifest is the subset of apm.yml we actually look at on
// import. Unknown keys are tolerated (the YAML decoder ignores
// them) — forward-compat per the spec's §4 "ignore unknown keys".
type apmManifest struct {
	Name         string      `yaml:"name"`
	Version      string      `yaml:"version"`
	Description  string      `yaml:"description"`
	Dependencies apmDepBlock `yaml:"dependencies"`
}

type apmDepBlock struct {
	// `apm:` is a list whose entries can be a bare string
	// shorthand OR a typed object. We decode into yaml.Node and
	// project to apmPrimitive at use-time so both forms survive.
	Apm []yaml.Node `yaml:"apm"`
	Mcp []yaml.Node `yaml:"mcp"`
}

// apmPrimitive captures the post-projection view of one entry in
// `dependencies.apm`. Kind is derived from the virtual_path tail
// (per spec §4.1.3) so we can group skills / playbooks / prompts /
// instructions in the import banner.
type apmPrimitive struct {
	Ref  string // raw shorthand or git URL (best-effort display)
	Kind string // "skill" | "playbook" | "prompt" | "instructions" | "agent" | "package"
}

// apmMcpEntry captures the post-projection view of one entry in
// `dependencies.mcp`. Name is the registry name or self-defined
// server name; Transport is the MCP transport (stdio/http/sse/...)
// when explicit.
type apmMcpEntry struct {
	Name      string
	Transport string
}

// addSourceFn is the seam tests stub to observe what `apm import`
// would dispatch into the source registry. Production wires it to
// the real (App).runSourceAdd path; the test rebinds it to capture
// argv invocations without writing config.toml.
var addSourceFn = func(a *App, argv []string) int { return a.runSourceAdd(argv) }

// runApm dispatches `clawtool apm <subcommand>`. Phase 1 only ships
// `import`; reverse export is queued for phase 2.
func (a *App) runApm(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, apmUsage)
		return 2
	}
	switch argv[0] {
	case "import":
		return a.runApmImport(argv[1:])
	default:
		fmt.Fprintf(a.Stderr, "clawtool apm: unknown subcommand %q\n\n%s", argv[0], apmUsage)
		return 2
	}
}

func (a *App) runApmImport(argv []string) int {
	fs := flag.NewFlagSet("apm import", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	dryRun := fs.Bool("dry-run", false, "Print actions without registering sources or writing the recipe stub.")
	repo := fs.String("repo", "", "Repository root for the emitted recipe stub. Default: cwd.")
	if err := fs.Parse(reorderFlagsFirst(argv, map[string]bool{"repo": true})); err != nil {
		return 2
	}
	rest := fs.Args()
	path := "./apm.yml"
	if len(rest) == 1 {
		path = rest[0]
	} else if len(rest) > 1 {
		fmt.Fprint(a.Stderr, "usage: clawtool apm import [<path/to/apm.yml>] [--dry-run] [--repo <path>]\n")
		return 2
	}

	mf, err := loadApmManifest(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool apm import: %v\n", err)
		if errors.Is(err, ErrApmManifestMissing) {
			return 1
		}
		return 1
	}

	mcps := projectMcp(mf.Dependencies.Mcp)
	prims := projectApm(mf.Dependencies.Apm)

	fmt.Fprintf(a.Stdout, "apm.yml: %s (name=%q version=%q)\n", path, mf.Name, mf.Version)
	fmt.Fprintf(a.Stdout, "  %d MCP server(s), %d primitive(s)\n", len(mcps), len(prims))

	// MCP section: emit one `source add ...` invocation per entry.
	// In dry-run we only print the would-be argv.
	for _, e := range mcps {
		short := mcpShortName(e.Name)
		argvAdd := []string{"source", "add", short}
		if short != e.Name {
			// preserve the full registry/qualified name as the
			// instance handle so collisions across e.g. two
			// `github` providers stay distinct.
			argvAdd = append(argvAdd, "--as", short)
		}
		fmt.Fprintf(a.Stdout, "  mcp: %s [transport=%s] -> clawtool %s\n",
			e.Name, orDash(e.Transport), strings.Join(argvAdd, " "))
		if *dryRun {
			continue
		}
		if rc := addSourceFn(a, argvAdd); rc != 0 {
			fmt.Fprintf(a.Stderr, "clawtool apm import: source add %q failed (rc=%d); continuing\n", short, rc)
		}
	}

	// Primitive section: emit a recipe-stub manifest the operator
	// can review before phase 2 wires real recipe application.
	repoRoot := *repo
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	stubPath := filepath.Join(repoRoot, ".clawtool", "apm-imported-manifest.toml")
	stub := renderApmStub(mf, prims, mcps)
	if *dryRun {
		fmt.Fprintf(a.Stdout, "  (dry-run) would write recipe stub: %s\n", stubPath)
	} else {
		if err := os.MkdirAll(filepath.Dir(stubPath), 0o755); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool apm import: %v\n", err)
			return 1
		}
		if err := os.WriteFile(stubPath, []byte(stub), 0o644); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool apm import: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Stdout, "  wrote recipe stub: %s\n", stubPath)
	}

	// Always enumerate the primitives so dry-run output stays
	// useful for review even when nothing hits disk.
	for _, p := range prims {
		fmt.Fprintf(a.Stdout, "  %s: %s\n", p.Kind, p.Ref)
	}
	return 0
}

// loadApmManifest reads the file at path and decodes the subset
// we care about. ErrApmManifestMissing is returned when the file
// is absent; ErrApmYAMLParse wraps every decode failure.
func loadApmManifest(path string) (*apmManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrApmManifestMissing, path)
		}
		return nil, err
	}
	var mf apmManifest
	if err := yaml.Unmarshal(raw, &mf); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrApmYAMLParse, path, err)
	}
	return &mf, nil
}

// projectMcp collapses each yaml.Node into an apmMcpEntry.
// Both string-shorthand and object forms (per §4.2) are accepted.
func projectMcp(nodes []yaml.Node) []apmMcpEntry {
	out := make([]apmMcpEntry, 0, len(nodes))
	for _, n := range nodes {
		switch n.Kind {
		case yaml.ScalarNode:
			out = append(out, apmMcpEntry{Name: n.Value})
		case yaml.MappingNode:
			var obj struct {
				Name      string `yaml:"name"`
				Transport string `yaml:"transport"`
			}
			_ = n.Decode(&obj)
			if obj.Name != "" {
				out = append(out, apmMcpEntry{Name: obj.Name, Transport: obj.Transport})
			}
		}
	}
	return out
}

// projectApm collapses each `dependencies.apm` node into an
// apmPrimitive and tags its kind from the virtual_path tail.
func projectApm(nodes []yaml.Node) []apmPrimitive {
	out := make([]apmPrimitive, 0, len(nodes))
	for _, n := range nodes {
		var ref string
		switch n.Kind {
		case yaml.ScalarNode:
			ref = n.Value
		case yaml.MappingNode:
			var obj struct {
				Git  string `yaml:"git"`
				Path string `yaml:"path"`
			}
			_ = n.Decode(&obj)
			ref = strings.TrimSpace(obj.Git + "#" + obj.Path)
		}
		if ref == "" {
			continue
		}
		out = append(out, apmPrimitive{Ref: ref, Kind: classifyPrimitive(ref)})
	}
	return out
}

// classifyPrimitive maps a primitive ref to a kind tag using the
// detection table from spec §4.1.3 plus the colloquial "playbook"
// label (a `/playbooks/` virtual path) so the import banner can
// report skills + playbooks distinctly.
func classifyPrimitive(ref string) string {
	low := strings.ToLower(ref)
	switch {
	case strings.HasSuffix(low, ".prompt.md"):
		return "prompt"
	case strings.HasSuffix(low, ".instructions.md"):
		return "instructions"
	case strings.HasSuffix(low, ".agent.md"):
		return "agent"
	case strings.Contains(low, "/skills/"):
		return "skill"
	case strings.Contains(low, "/playbooks/"):
		return "playbook"
	case strings.Contains(low, "/collections/"):
		return "collection"
	default:
		return "package"
	}
}

// mcpShortName extracts a kebab-friendly handle from a registry
// reference like "io.github.github/github-mcp-server" → "github-mcp-server".
// Falls back to the raw name when no `/` is present.
func mcpShortName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// renderApmStub emits a TOML-shaped manifest that lists what was
// imported. Phase 2 will consume this stub via the recipe engine;
// for now the operator reviews it manually.
func renderApmStub(mf *apmManifest, prims []apmPrimitive, mcps []apmMcpEntry) string {
	var b strings.Builder
	b.WriteString("# clawtool apm-imported-manifest (phase 1: review-only stub)\n")
	b.WriteString("# Generated from apm.yml. Recipe wiring lands in phase 2.\n\n")
	fmt.Fprintf(&b, "[meta]\nname = %q\nversion = %q\ndescription = %q\nschema = \"microsoft/apm manifest 0.1 (2026-03-06)\"\n\n",
		mf.Name, mf.Version, mf.Description)
	// Group primitives by kind, alphabetised, for stable output.
	groups := map[string][]string{}
	for _, p := range prims {
		groups[p.Kind] = append(groups[p.Kind], p.Ref)
	}
	kinds := make([]string, 0, len(groups))
	for k := range groups {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(&b, "[apm.%s]\nrefs = [\n", k)
		for _, r := range groups[k] {
			fmt.Fprintf(&b, "  %q,\n", r)
		}
		b.WriteString("]\n\n")
	}
	if len(mcps) > 0 {
		b.WriteString("[mcp]\nservers = [\n")
		for _, e := range mcps {
			fmt.Fprintf(&b, "  { name = %q, transport = %q },\n", e.Name, e.Transport)
		}
		b.WriteString("]\n")
	}
	return b.String()
}

const apmUsage = `Usage:
  clawtool apm import [<path/to/apm.yml>] [--dry-run] [--repo <path>]
                              Read an apm.yml manifest (microsoft/apm
                              format, schema 0.1) and register its MCP
                              servers via 'clawtool source add'. Skills,
                              playbooks, and other agent primitives are
                              recorded in
                              <repo>/.clawtool/apm-imported-manifest.toml
                              for the operator to review; phase 2 will
                              wire those into the recipe engine.

  Default path: ./apm.yml. --dry-run prints what would be done without
  touching config or writing the stub. --repo overrides the recipe-stub
  destination root (default: cwd).
`
