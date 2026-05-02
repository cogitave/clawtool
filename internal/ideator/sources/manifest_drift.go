// Package sources — manifest_drift source.
//
// Compares the manifest's hardcoded ToolSpec.Description against
// the *live* description each tool's Register fn emits via
// `mcp.WithDescription(...)`. Drift here is the v0.22.110 bug
// class: a hand-edit to one side leaves the search index ranking
// on stale prose. SyncDescriptionsFromRegistration auto-mirrors at
// boot, but this source guards against the inverse — someone
// removing the sync call or accidentally re-introducing the two
// hand-maintained copies.
//
// Cycle note: this package can't import internal/tools/core
// (core registers IdeateRun / IdeateApply, which would import
// sources). The CLI / MCP edge injects the manifest builder via
// SetManifestBuilder before the orchestrator runs; when nothing
// is injected the source is a quiet no-op.
package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/cogitave/clawtool/internal/ideator"
	"github.com/cogitave/clawtool/internal/tools/registry"
)

// ManifestProvider returns the live MCP tool manifest. The CLI /
// MCP edge wraps core.BuildManifest into this shape so this package
// stays free of an import cycle.
type ManifestProvider func() *registry.Manifest

// ManifestDrift implements IdeaSource by diffing the manifest spec
// descriptions against what each Register fn registers live.
type ManifestDrift struct {
	// Provider returns the manifest to inspect. nil → silent no-op.
	Provider ManifestProvider
}

// NewManifestDrift returns a ready-to-use manifest-drift detector.
// Provider must be wired separately via SetProvider or by direct
// field assignment; the CLI / MCP edge does this in
// internal/cli/ideate.go and internal/tools/core/ideator_tool.go
// (those layers can import core without creating a cycle).
func NewManifestDrift() *ManifestDrift {
	return &ManifestDrift{Provider: defaultProvider()}
}

// Name returns the canonical source name.
func (ManifestDrift) Name() string { return "manifest_drift" }

// SetProvider lets a caller swap the manifest builder (tests use
// this to feed a hand-built manifest with deliberately drifted
// descriptions).
func (m *ManifestDrift) SetProvider(p ManifestProvider) { m.Provider = p }

// Scan compares each registered spec description against the live
// `mcp.WithDescription` value. Mismatches surface as Ideas pointing
// the operator at SyncDescriptionsFromRegistration.
func (m ManifestDrift) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if m.Provider == nil {
		return nil, nil
	}
	mf := m.Provider()
	if mf == nil {
		return nil, nil
	}
	live := mf.LiveDescriptions(registry.Runtime{})
	if live == nil {
		return nil, nil
	}
	var ideas []ideator.Idea
	for _, spec := range mf.Specs() {
		want, ok := live[spec.Name]
		if !ok {
			// Tool exposed in the manifest but not live-registered
			// (a companion entry like AutopilotDone whose registrar
			// is shared with AutopilotAdd). Not drift.
			continue
		}
		if want == spec.Description {
			continue
		}
		hash := sha1.Sum([]byte(spec.Name + "|drift"))
		ideas = append(ideas, ideator.Idea{
			Title:             "manifest drift: " + spec.Name,
			Summary:           fmt.Sprintf("Tool %s has a manifest description that differs from the live mcp.WithDescription(...) string. Re-run SyncDescriptionsFromRegistration or align the two sources by hand.", spec.Name),
			Evidence:          "internal/tools/core/manifest.go:" + spec.Name,
			SuggestedPriority: 6,
			SuggestedPrompt: fmt.Sprintf(
				"Repair manifest description drift for the %s MCP tool.\n\n"+
					"The manifest spec carries one description, the registrar emits another via\n"+
					"mcp.WithDescription(...). Decide which is canonical, mirror it onto the\n"+
					"other, and confirm internal/tools/core/manifest_test.go's drift assertion\n"+
					"passes. The 2026-04-30 fix (commit calling\n"+
					"`m.SyncDescriptionsFromRegistration(registry.Runtime{})` at the bottom of\n"+
					"BuildManifest) keeps both in lockstep — verify it still runs.",
				spec.Name),
			DedupeKey: "manifest_drift:" + hex.EncodeToString(hash[:]),
		})
	}
	return ideas, nil
}

// providerRegistry is the package-global slot the CLI / MCP edge
// writes through SetDefaultManifestProvider. The orchestrator wires
// it into NewManifestDrift's default Provider so callers don't have
// to pass it explicitly every time.
var (
	providerMu      sync.RWMutex
	providerDefault ManifestProvider
)

// SetDefaultManifestProvider lets the CLI / MCP edge inject a
// manifest builder. internal/cli/ideate.go calls this at init so
// every freshly-constructed ManifestDrift picks up the canonical
// builder; the function is safe to call multiple times.
func SetDefaultManifestProvider(p ManifestProvider) {
	providerMu.Lock()
	defer providerMu.Unlock()
	providerDefault = p
}

// defaultProvider returns whatever the edge has registered, or
// nil. Reading once at construction lets each NewManifestDrift see
// a stable snapshot.
func defaultProvider() ManifestProvider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	return providerDefault
}
