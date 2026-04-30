// Package portal — Bifrost driver stub (phase 1).
//
// maximhq/bifrost (https://github.com/maximhq/bifrost, Apache-2.0)
// is a Go-native AI gateway that composes per-vendor portals under
// one config: unified failover, semantic caching, and budget
// governance across OpenAI / Anthropic / Vertex / Bedrock / local
// llama backends. clawtool's portal layer today registers one entry
// per chat web-UI; Bifrost-shaped portals would route by model with
// a fallback chain instead.
//
// Phase 1 (this file) ships the registration + list surface only.
// Adding the bifrost/core Go module (which transitively pulls a
// large dependency graph: every supported provider's SDK, an
// embedded SQLite for the semantic cache, etc.) is deferred to a
// later commit gated behind the `clawtool_bifrost` build tag — see
// the `Ask` deferred-error message below for the canonical sentinel
// callers can match on.
//
// The driver REGISTERS — it surfaces in `clawtool portal list` as
// `bifrost (deferred)` so operators can discover the integration
// path before phase 2 lands. Calling Ask returns a typed error
// instead of silently no-oping, mirroring the `AskNotImplementedError`
// sentinel pattern the v0.16.1 portal driver used while the CDP
// runtime was in flight.
package portal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Driver is the in-memory portal-driver shape: a registered name,
// a status string surfaced in PortalList, and an Ask entry point.
// Drivers are distinct from config-stored [portals.<name>] stanzas
// — the former are compiled-in integrations (bifrost gateway,
// future LiteLLM / OpenRouter), the latter are user-saved web-UI
// targets.
//
// PortalList merges both: config stanzas keep their per-portal
// row, drivers add a single discovery row each so the operator
// can see what integrations are available without reading source.
type Driver interface {
	// Name is the kebab-case identifier — must match the config
	// key the operator would write under [portals.<name>] if they
	// wanted to override defaults. Stable across releases.
	Name() string

	// Status is the short label rendered in PortalList's STATUS
	// column. Stub drivers return "deferred"; ready drivers will
	// return "ready" once their phase-2 implementation lands.
	Status() string

	// Description is the one-line summary of what the driver wraps.
	// Surfaced in `portal list` table mode and in MCP discovery.
	Description() string

	// Ask drives the gateway with `prompt` and returns the response.
	// Phase-1 drivers return ErrBifrostDeferred (or an analogous
	// sentinel) so the caller's error handling matches the pattern
	// the CDP-runtime placeholder used.
	Ask(ctx context.Context, prompt string) (string, error)
}

// ErrBifrostDeferred is the canonical sentinel returned by the
// phase-1 Bifrost stub. CLI / MCP surfaces match against it via
// errors.Is to give a uniform deferred-feature message; phase 2
// removes it once bifrost/core lands behind the
// `clawtool_bifrost` build tag.
var ErrBifrostDeferred = errors.New("bifrost portal: integration deferred to phase 2 — add bifrost/core via build tag clawtool_bifrost")

// driverRegistry is the in-process catalog of compile-time drivers.
// init() populates it; PortalList merges it with the config-stored
// portals.
var (
	driverMu       sync.RWMutex
	driverRegistry = map[string]Driver{}
)

// RegisterDriver adds d to the global driver registry. Panics on
// empty / duplicate Name — same boot-time-fail pattern the recipe
// registry uses (see internal/setup/recipe.go) so a misspelled
// name surfaces at binary build, not at the user's first run.
func RegisterDriver(d Driver) {
	if d == nil {
		panic("portal: RegisterDriver(nil)")
	}
	name := d.Name()
	if name == "" {
		panic("portal: driver with empty Name")
	}
	driverMu.Lock()
	defer driverMu.Unlock()
	if _, exists := driverRegistry[name]; exists {
		panic(fmt.Sprintf("portal: driver %q already registered", name))
	}
	driverRegistry[name] = d
}

// LookupDriver returns the driver with the given name, or nil if
// absent. Names are unique so the lookup is unambiguous.
func LookupDriver(name string) Driver {
	driverMu.RLock()
	defer driverMu.RUnlock()
	return driverRegistry[name]
}

// Drivers returns every registered driver, sorted by name. Stable
// order so PortalList output and tests don't flake.
func Drivers() []Driver {
	driverMu.RLock()
	defer driverMu.RUnlock()
	names := make([]string, 0, len(driverRegistry))
	for n := range driverRegistry {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Driver, 0, len(names))
	for _, n := range names {
		out = append(out, driverRegistry[n])
	}
	return out
}

// ── bifrost driver ────────────────────────────────────────────────

// BifrostDriverName is the canonical kebab-case identifier exposed
// in PortalList and accepted by `clawtool portal ask bifrost`.
const BifrostDriverName = "bifrost"

// bifrostDriver is the phase-1 stub. Holds no state — Ask returns
// ErrBifrostDeferred unconditionally. Phase 2 swaps the
// implementation behind the `clawtool_bifrost` build tag and adds
// the bifrost/core dependency.
type bifrostDriver struct{}

func (bifrostDriver) Name() string   { return BifrostDriverName }
func (bifrostDriver) Status() string { return "deferred" }
func (bifrostDriver) Description() string {
	return "Bifrost AI gateway: unified failover, semantic cache, budget governance (phase 2)"
}

// Ask is the documented deferred-error entry point. Returning
// ErrBifrostDeferred (not silently succeeding with empty output) is
// load-bearing — the CLI uses errors.Is to print a uniform
// deferred-feature message instead of a generic failure.
func (bifrostDriver) Ask(_ context.Context, _ string) (string, error) {
	return "", ErrBifrostDeferred
}

func init() { RegisterDriver(bifrostDriver{}) }
