// Package catalog ships clawtool's built-in source catalog.
//
// Per ADR-008, the catalog is the fast path for `clawtool source add <name>`.
// Each entry knows the canonical package, runtime, required env vars, and
// auth flow hint for one well-known MCP source server. The catalog is a
// single TOML file embedded in the binary — no network calls during a
// `source add`, no first-run download, fully offline-capable.
//
// External catalog federation (Docker MCP Catalog, MCP Registry, Smithery)
// is a planned extension; this package currently exposes only the built-in.
package catalog

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed builtin.toml
var builtinTOML []byte

// Catalog is the in-memory representation of one catalog file.
type Catalog struct {
	SchemaVersion int              `toml:"schema_version"`
	Entries       map[string]Entry `toml:"entries"`
}

// Entry is one source-server description.
type Entry struct {
	Description string   `toml:"description"`
	Runtime     string   `toml:"runtime"`
	Package     string   `toml:"package"`
	Args        []string `toml:"args,omitempty"`
	RequiredEnv []string `toml:"required_env,omitempty"`
	AuthHint    string   `toml:"auth_hint,omitempty"`
	Homepage    string   `toml:"homepage,omitempty"`
	Maintained  string   `toml:"maintained,omitempty"`
}

// NamedEntry pairs an entry with its catalog key (name) for sorted listing.
type NamedEntry struct {
	Name  string
	Entry Entry
}

// Builtin parses the embedded catalog. Returns an error only when the
// embedded TOML itself is malformed — i.e., a build-time bug we want to
// surface loudly.
func Builtin() (*Catalog, error) {
	var c Catalog
	if err := toml.Unmarshal(builtinTOML, &c); err != nil {
		return nil, fmt.Errorf("parse builtin catalog: %w", err)
	}
	if c.SchemaVersion == 0 {
		return nil, fmt.Errorf("catalog missing schema_version")
	}
	return &c, nil
}

// Lookup resolves a bare name (e.g. "github") to its entry. Returns
// ok=false when the name is not in the catalog so callers can fall back
// to the long-form path or report a curated suggestion.
func (c *Catalog) Lookup(name string) (Entry, bool) {
	e, ok := c.Entries[name]
	return e, ok
}

// List returns all entries sorted by name. Drives `clawtool source list
// --available` (a future flag) and any introspection surface.
func (c *Catalog) List() []NamedEntry {
	out := make([]NamedEntry, 0, len(c.Entries))
	for name, entry := range c.Entries {
		out = append(out, NamedEntry{Name: name, Entry: entry})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SuggestSimilar returns up to `limit` candidate names that share a prefix
// or substring with `name`. Used to nudge users who typo a source name.
func (c *Catalog) SuggestSimilar(name string, limit int) []string {
	if name == "" || limit <= 0 {
		return nil
	}
	lname := strings.ToLower(name)
	var out []string
	for _, e := range c.List() {
		if strings.Contains(strings.ToLower(e.Name), lname) {
			out = append(out, e.Name)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// ToSourceCommand renders the runtime + package + args into the argv that
// would be exec'd to spawn this source. Used by CLI source-add to populate
// `[sources.<name>].command` in config.toml. Variable references like
// `${FILESYSTEM_ROOT}` are preserved verbatim — interpolation happens at
// spawn time against secrets + env, not now.
//
// Mapping (per ADR-008):
//
//	npx     →  npx -y <package> [args...]
//	node    →  node node_modules/<package>/index.js [args...]
//	python  →  uvx <package> [args...]   (uvx is the modern uv-managed runner)
//	docker  →  docker run -i --rm <image> [args...]
//	binary  →  <package> [args...]       (PATH-resolved)
func (e Entry) ToSourceCommand() ([]string, error) {
	switch e.Runtime {
	case "npx":
		return append([]string{"npx", "-y", e.Package}, e.Args...), nil
	case "node":
		return append([]string{"node", "node_modules/" + e.Package + "/index.js"}, e.Args...), nil
	case "python":
		return append([]string{"uvx", e.Package}, e.Args...), nil
	case "docker":
		return append([]string{"docker", "run", "-i", "--rm", e.Package}, e.Args...), nil
	case "binary":
		return append([]string{e.Package}, e.Args...), nil
	default:
		return nil, fmt.Errorf("unknown runtime %q", e.Runtime)
	}
}

// EnvTemplate returns a map keyed by required_env names with `${VAR}`-style
// values that secrets resolution will fill in at spawn time. Empty when
// no auth required.
func (e Entry) EnvTemplate() map[string]string {
	if len(e.RequiredEnv) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.RequiredEnv))
	for _, k := range e.RequiredEnv {
		out[k] = "${" + k + "}"
	}
	return out
}
