package setup

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Stability marks how settled a recipe is. The wizard hides
// experimental recipes by default; recipe_list({stability: "stable"})
// is the default filter.
type Stability string

const (
	StabilityStable       Stability = "stable"
	StabilityBeta         Stability = "beta"
	StabilityExperimental Stability = "experimental"
)

// RecipeMeta is what every recipe declares about itself. Every
// field except OptionalArgs is required at compile time — a recipe
// missing Upstream literally cannot be valid, which makes the
// wrap-don't-reinvent rule code-level enforced.
type RecipeMeta struct {
	// Name is kebab-case, unique within the category. Surfaced as
	// the CLI selector and the MCP recipe_apply argument.
	Name string

	// Category is the typed enum. Registry refuses unknown values.
	Category Category

	// Description is one line shown in the wizard and recipe_list.
	Description string

	// Upstream is the canonical URL of the project this recipe
	// wraps. REQUIRED — registry refuses an empty Upstream so a
	// from-scratch reimplementation can't be smuggled in.
	//
	// Use the project's primary repo URL (e.g.
	// "https://github.com/googleapis/release-please") or — for
	// recipes that wrap a spec rather than a project — the spec
	// URL with a "spec:" prefix (e.g.
	// "spec:https://www.conventionalcommits.org").
	Upstream string

	// Stability defaults to StabilityStable if zero-valued.
	Stability Stability

	// Core marks a recipe as part of clawtool's curated default
	// install. When true, the setup wizard pre-checks the row and
	// `clawtool init --all` applies it without prompting (regardless
	// of Stability — Beta recipes can be Core too). Defaults to
	// false so unset / experimental recipes stay opt-in.
	Core bool
}

// Status is what Detect() returns: the recipe's current state in
// the target repo.
type Status string

const (
	// StatusAbsent: no trace of the recipe in the repo.
	StatusAbsent Status = "absent"
	// StatusPartial: some artifacts exist but config is incomplete
	// or stale; Apply will reconcile.
	StatusPartial Status = "partial"
	// StatusApplied: recipe is fully configured and Verify passes.
	StatusApplied Status = "applied"
	// StatusError: detection itself failed (e.g. permission denied
	// reading a file). Treat as opaque; show the error to the user.
	StatusError Status = "error"
)

// Prereq describes one external dependency the recipe needs before
// Apply can run. The wizard surfaces missing prereqs and offers to
// install them via the platform-canonical command.
type Prereq struct {
	// Name is human-readable: "Node.js 18+", "GitHub CLI", "Obsidian".
	Name string

	// Check returns nil if the prereq is satisfied. Implementations
	// typically exec.LookPath the binary or shell out a version
	// probe.
	Check func(ctx context.Context) error

	// Install is the command (or commands) clawtool offers to run
	// if Check fails and the user consents. Empty means "manual
	// install only" — the wizard will print ManualHint instead.
	Install map[Platform][]string

	// ManualHint is shown when no Install entry matches the host
	// platform or when the user picks the manual route. One short
	// paragraph; usually a URL plus one-liner.
	ManualHint string
}

// Platform is what we dispatch installer commands on. Kept narrow:
// linux/darwin/windows. ARM-vs-x86 is the recipe's problem if it
// matters.
type Platform string

const (
	PlatformLinux   Platform = "linux"
	PlatformDarwin  Platform = "darwin"
	PlatformWindows Platform = "windows"
)

// Options is the per-Apply parameter bag. Free-form because each
// recipe has its own knobs (vault path, license SPDX id, default
// branch). The wizard fills these via prompts; the MCP surface
// fills them via the recipe_apply call's arguments map.
type Options map[string]any

// ForceOption is the canonical key recipes consult to decide
// whether to overwrite a file that exists but is not clawtool-
// managed. The wizard surfaces this as an "overwrite anyway?"
// prompt; the CLI exposes it via `--force` (which parseKV
// translates to opts[force]=true).
const ForceOption = "force"

// IsForced reports whether opts requests an overwrite of unmanaged
// files. Defaults to false. Recipes call this in their Apply to
// gate the "exists but no marker → refuse" check.
func IsForced(o Options) bool {
	v, _ := GetOption[bool](o, ForceOption)
	return v
}

// Get is a typed helper that returns the option's value as T or
// the zero value if missing. Recipes use this instead of touching
// the map directly so the keys stay grep-able.
func GetOption[T any](o Options, key string) (T, bool) {
	var zero T
	if o == nil {
		return zero, false
	}
	v, ok := o[key]
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// Recipe is the single interface every entry implements.
type Recipe interface {
	// Meta returns the static descriptor. Called at registration
	// (validated for required fields) and on every list call.
	Meta() RecipeMeta

	// Detect probes the repo at `repo` and returns the recipe's
	// current state plus an explanation string the wizard prints.
	// Returning StatusError surfaces the error verbatim.
	Detect(ctx context.Context, repo string) (Status, string, error)

	// Prereqs lists what must exist on the host before Apply is
	// safe to call. Empty slice = no prereqs.
	Prereqs() []Prereq

	// Apply executes the recipe against repo with opts. May shell
	// out, write files, fetch network resources. Atomic in the
	// success case; on partial-fail the error wraps what was done
	// so the user can recover.
	Apply(ctx context.Context, repo string, opts Options) error

	// Verify is the post-Apply sanity check, also re-runnable
	// later. Returns nil if the recipe's installed state is
	// healthy; otherwise an error describing what's missing.
	Verify(ctx context.Context, repo string) error
}

// Registry is the in-process catalog of available recipes. Recipes
// register themselves at package init time via Register().
type Registry struct {
	mu sync.RWMutex
	// Keyed by recipe name (unique across categories — the
	// taxonomy enforces this at registration).
	byName map[string]Recipe
	// Index by category for fast list-by-category.
	byCategory map[Category][]string
}

var globalRegistry = &Registry{
	byName:     map[string]Recipe{},
	byCategory: map[Category][]string{},
}

// Register adds r to the global registry. Panics on:
//   - empty/duplicate Name
//   - unknown Category
//   - empty Upstream (wrap-don't-reinvent enforcement)
//   - empty Description
//
// Always called from a recipe package's init(); panics fail the
// binary at boot, not at the user's first run.
func Register(r Recipe) {
	m := r.Meta()
	if strings.TrimSpace(m.Name) == "" {
		panic("setup: recipe with empty Name")
	}
	m.Category.MustValid()
	if strings.TrimSpace(m.Upstream) == "" {
		panic(fmt.Sprintf("setup: recipe %q has empty Upstream — every recipe must wrap a real upstream (URL or spec:URL)", m.Name))
	}
	if strings.TrimSpace(m.Description) == "" {
		panic(fmt.Sprintf("setup: recipe %q has empty Description", m.Name))
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	if _, exists := globalRegistry.byName[m.Name]; exists {
		panic(fmt.Sprintf("setup: recipe %q already registered", m.Name))
	}
	globalRegistry.byName[m.Name] = r
	globalRegistry.byCategory[m.Category] = append(globalRegistry.byCategory[m.Category], m.Name)
	sort.Strings(globalRegistry.byCategory[m.Category])
}

// Lookup returns the recipe with the given name, or nil if absent.
// Names are unique across categories so the lookup is unambiguous.
func Lookup(name string) Recipe {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	return globalRegistry.byName[name]
}

// All returns every registered recipe, sorted by category then by
// name within category. Stable order so wizard output and tests
// don't flake.
func All() []Recipe {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	out := make([]Recipe, 0, len(globalRegistry.byName))
	for _, cat := range Categories() {
		for _, name := range globalRegistry.byCategory[cat] {
			out = append(out, globalRegistry.byName[name])
		}
	}
	return out
}

// InCategory returns recipes for the given category in name order.
func InCategory(cat Category) []Recipe {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	names := globalRegistry.byCategory[cat]
	out := make([]Recipe, 0, len(names))
	for _, n := range names {
		out = append(out, globalRegistry.byName[n])
	}
	return out
}

// resetForTest clears the global registry. Test-only — production
// code never calls this.
func resetForTest() {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.byName = map[string]Recipe{}
	globalRegistry.byCategory = map[Category][]string{}
}
