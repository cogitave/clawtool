// Package registry — typed manifest of every clawtool MCP tool.
// Codex's #1 ROI architectural recommendation (BIAM task
// a3ef5af9): collapse server.go's hand-maintained list of
// RegisterX calls + CoreToolDocs's parallel description list +
// the slash-command + skill routing-map cross-references into
// ONE typed source of truth.
//
// Step 1 (this commit): ship the package + types + an empty
// Manifest. server.go is unchanged. Subsequent commits migrate
// tool registration through the registry, one cohesive group at
// a time, with the surface_drift_test guarding each step.
//
// Why type-driven, not config-driven: a TOML manifest would
// need a runtime registry of register funcs anyway. Putting the
// register-fn pointer ON the typed ToolSpec keeps the type
// system honest — a misspelled tool name fails to compile, not
// at boot.
//
// Why a separate package, not a method on core: core/ already
// owns ~30 RegisterX functions. Importing core to build the
// manifest, then having core import registry to look up specs,
// would be a cycle. registry stays a leaf — core (and any future
// tool source) imports it; server.go calls registry.Apply.
package registry

import (
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/search"
	"github.com/mark3labs/mcp-go/server"
)

// ToolSpec is the typed manifest entry for one MCP tool. Every
// shipped tool is described by exactly one ToolSpec. The fields
// match the four planes of the shipping contract
// (docs/feature-shipping-contract.md):
//
//   - Name + Description + Keywords  →  search index + ToolSearch
//   - Category                        →  introspection + grouping
//   - Gate                            →  config.IsEnabled subset
//   - Register                        →  the actual MCP wiring
//
// Slash command + skill row don't live on the spec because
// they're *file*-shaped (commands/clawtool-X.md,
// skills/clawtool/SKILL.md routing rows). The surface drift
// test (internal/server/surface_drift_test.go) cross-references
// the manifest against those files at test time.
type ToolSpec struct {
	// Name is the canonical MCP tool name. PascalCase per ADR-006.
	// MUST be unique within a Manifest; duplicates are a load-time
	// error.
	Name string

	// Description is the one-paragraph human form. Same string the
	// tool surfaces via tools/list AND ToolSearch.
	Description string

	// Keywords feed the bleve BM25 index. Lowercase, single words,
	// 3-12 entries is the sweet spot.
	Keywords []string

	// Category groups tools for introspection / grouping in
	// tools/list and the README. See package-level Category*
	// constants for the canonical set.
	Category Category

	// Gate names the config.IsEnabled key for this tool. Empty =
	// always-on (BridgeAdd / Verify / SemanticSearch / etc.).
	// "Bash" gate also covers BashOutput + BashKill (companions).
	Gate string

	// Register is the MCP wiring callback. Receives the server +
	// per-tool runtime dependencies (search index, secrets store,
	// sources manager) via the Runtime struct. Empty when the
	// tool is documented in the manifest but registered through
	// a legacy direct path — useful during incremental migration.
	Register RegisterFn
}

// Runtime carries the cross-cutting dependencies a register fn
// might need. Passed by value (struct of pointers / interfaces)
// so the manifest stays composable and tests can stub fields
// independently. Add fields as new tools demand them; never
// remove without a deprecation cycle.
type Runtime struct {
	// Index is the bleve search index ToolSearch closes over.
	// Step 4 wires ToolSearch through the manifest, so this
	// field becomes load-bearing rather than aspirational.
	Index *search.Index

	// Secrets is the secrets store WebSearch reads its API key
	// from at registration time. Typed as *secrets.Store at the
	// importer's site (server.go / core); registry stays a leaf
	// by holding it as `any` and letting the per-tool register
	// fn type-assert. The trade-off (slightly worse type safety
	// at registration) is preferable to having registry depend
	// on internal/secrets — keeps the import graph linear.
	Secrets any
}

// RegisterFn is the shape every typed register callback adopts.
// Mirrors mcp-go's AddTool but receives Runtime so register-time
// dependencies stay explicit — no package-level singletons leak
// into tool implementations.
type RegisterFn func(s *server.MCPServer, rt Runtime)

// Category enumerates the canonical groupings. New categories
// require code review — adding one without thinking through the
// existing seven leads to single-tool buckets that no UI can
// surface.
type Category string

const (
	CategoryShell      Category = "shell"      // Bash, BashOutput, BashKill, Verify
	CategoryFile       Category = "file"       // Read, Edit, Write, Glob, Grep
	CategoryWeb        Category = "web"        // WebFetch, WebSearch, BrowserFetch, BrowserScrape, Portal*
	CategoryDispatch   Category = "dispatch"   // SendMessage, AgentList, Task*, TaskNotify
	CategoryAuthoring  Category = "authoring"  // McpNew/Run/Build/Install/List, SkillNew, AgentNew
	CategorySetup      Category = "setup"      // Recipe*, Bridge*, Sandbox*
	CategoryDiscovery  Category = "discovery"  // ToolSearch, SemanticSearch
	CategoryCheckpoint Category = "checkpoint" // Commit, RulesCheck (future: Snapshot, Restore)
)

// IsValidCategory is the load-time guard. A typo in a ToolSpec's
// Category field crashes the manifest builder rather than slipping
// into the wild as a tool that no group lists.
func IsValidCategory(c Category) bool {
	switch c {
	case CategoryShell, CategoryFile, CategoryWeb, CategoryDispatch,
		CategoryAuthoring, CategorySetup, CategoryDiscovery, CategoryCheckpoint:
		return true
	}
	return false
}

// Manifest is the ordered collection of ToolSpec entries. Order
// matters for two reasons:
//   - server.go's RegisterX call order today is preserved
//     during incremental migration so behaviour change is
//     observable per-tool.
//   - tools/list output groups by Category but ties break on
//     manifest order; deterministic output simplifies test
//     fixtures.
type Manifest struct {
	specs []ToolSpec
	names map[string]struct{}
}

// New builds an empty Manifest. Add specs via Append.
func New() *Manifest {
	return &Manifest{
		specs: nil,
		names: map[string]struct{}{},
	}
}

// Append registers one ToolSpec. Duplicate names panic — the
// manifest is built at boot, before any user request, so a
// duplicate is a programmer error worth crashing on.
func (m *Manifest) Append(spec ToolSpec) {
	if spec.Name == "" {
		panic("registry.Manifest.Append: empty Name")
	}
	if _, dup := m.names[spec.Name]; dup {
		panic("registry.Manifest.Append: duplicate Name " + spec.Name)
	}
	if !IsValidCategory(spec.Category) {
		panic("registry.Manifest.Append: invalid Category " + string(spec.Category) + " for tool " + spec.Name)
	}
	m.names[spec.Name] = struct{}{}
	m.specs = append(m.specs, spec)
}

// Specs returns the manifest contents in insertion order. Caller
// MUST NOT mutate the slice.
func (m *Manifest) Specs() []ToolSpec {
	if m == nil {
		return nil
	}
	return m.specs
}

// SearchDocs flattens the manifest into search.Doc entries for
// the bleve indexer. Always-on tools always appear; gateable
// tools are filtered by the caller-supplied gate predicate
// (typically `cfg.IsEnabled(name).Enabled`). When pred is nil
// every spec is included.
func (m *Manifest) SearchDocs(pred func(toolName string) bool) []search.Doc {
	if m == nil {
		return nil
	}
	out := make([]search.Doc, 0, len(m.specs))
	for _, s := range m.specs {
		if s.Gate != "" && pred != nil && !pred(s.Gate) {
			continue
		}
		out = append(out, search.Doc{
			Name:        s.Name,
			Description: s.Description,
			Type:        "core",
			Keywords:    s.Keywords,
		})
	}
	return out
}

// Apply walks the manifest and calls each spec's Register fn,
// gated by the caller-supplied predicate. Mirrors server.go's
// hand-maintained `if cfg.IsEnabled(name) { core.RegisterX(s) }`
// chain — once the migration completes, server.go calls
// `manifest.Apply(s, runtime, cfg.IsEnabled)` and that chain
// disappears entirely.
//
// Specs with a nil Register fn are skipped silently. This is
// intentional during incremental migration: a spec added to the
// manifest for documentation purposes (so SearchDocs picks it up)
// without yet being wired to the new register flow stays
// harmless until its turn comes.
func (m *Manifest) Apply(s *server.MCPServer, rt Runtime, pred func(toolName string) bool) {
	if m == nil {
		return
	}
	for _, spec := range m.specs {
		if spec.Register == nil {
			continue
		}
		if spec.Gate != "" && pred != nil && !pred(spec.Gate) {
			continue
		}
		spec.Register(s, rt)
	}
}

// Names returns every spec name in insertion order. Useful for
// diff-against-something tests.
func (m *Manifest) Names() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.specs))
	for _, s := range m.specs {
		out = append(out, s.Name)
	}
	return out
}

// SortedNames returns the manifest's tool names alphabetically.
// Tests that need deterministic output independent of insertion
// order use this; runtime code prefers Names() to preserve the
// gate / display ordering.
func (m *Manifest) SortedNames() []string {
	out := m.Names()
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}
