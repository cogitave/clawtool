// Package agents — genericAdapter is a parameterised adapter
// that mirrors claude-code's settings.json + permissions.deny
// shape but lets the registration site point at any other
// config dir + field name. Used to ship support for new agents
// without writing a full adapter file each time.
//
// Today this is best-effort: hermes-agent and openclaw both
// presumably load a similar JSON config but their exact deny-
// field name may shift. The adapter is structured so a future
// PR can swap the field via a one-line change.
package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// genericAdapter is the mutable settings-file adapter. Given a
// settings path + a JSON field path (single-level for now), it
// claims by appending tool names to that field's array. Marker
// file lives next to the settings file so Release is precise.
type genericAdapter struct {
	name        string
	settingsRel string // relative to home, e.g. ".hermes-agent/settings.json"
	denyField   string // e.g. "disabled_tools"

	// pathOverride lets tests redirect the on-disk paths without
	// touching $HOME. Empty in production.
	pathOverride string
}

func (g *genericAdapter) Name() string { return g.name }

func (g *genericAdapter) settingsPath() string {
	if g.pathOverride != "" {
		return g.pathOverride
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return g.settingsRel
	}
	return filepath.Join(home, g.settingsRel)
}

func (g *genericAdapter) markerPath() string {
	return filepath.Join(filepath.Dir(g.settingsPath()), "settings.clawtool.lock")
}

func (g *genericAdapter) Detected() bool {
	dir := filepath.Dir(g.settingsPath())
	if _, err := os.Stat(dir); err == nil {
		return true
	}
	return false
}

func (g *genericAdapter) Claim(opts Options) (Plan, error) {
	plan := Plan{
		Adapter:      g.Name(),
		Action:       "claim",
		SettingsPath: g.settingsPath(),
		MarkerPath:   g.markerPath(),
		DryRun:       opts.DryRun,
	}

	raw, err := g.readMap()
	if err != nil {
		return plan, err
	}
	deny := readDenyField(raw, g.denyField)
	currentDeny := stringSet(deny)
	var toAdd []string
	for _, t := range ClaimedToolsForClawtool {
		if !currentDeny[t] {
			toAdd = append(toAdd, t)
		}
	}
	plan.ToolsAdded = toAdd

	finalDeny := append([]string{}, deny...)
	finalDeny = append(finalDeny, toAdd...)
	sort.Strings(finalDeny)
	finalDeny = dedupSorted(finalDeny)
	finalDenySet := stringSet(finalDeny)
	var owned []string
	for _, t := range ClaimedToolsForClawtool {
		if finalDenySet[t] {
			owned = append(owned, t)
		}
	}
	sort.Strings(owned)

	if len(toAdd) == 0 {
		if existing, err := g.readMarker(); err == nil && equalStrings(existing.Tools, owned) {
			plan.WasNoop = true
			return plan, nil
		}
	}

	if opts.DryRun {
		return plan, nil
	}

	writeDenyField(raw, g.denyField, finalDeny)
	if err := g.writeMap(raw); err != nil {
		return plan, fmt.Errorf("write settings: %w", err)
	}
	if err := g.writeMarker(owned); err != nil {
		return plan, fmt.Errorf("write marker: %w", err)
	}
	return plan, nil
}

func (g *genericAdapter) Release(opts Options) (Plan, error) {
	plan := Plan{
		Adapter:      g.Name(),
		Action:       "release",
		SettingsPath: g.settingsPath(),
		MarkerPath:   g.markerPath(),
		DryRun:       opts.DryRun,
	}

	marker, err := g.readMarker()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			plan.WasNoop = true
			return plan, nil
		}
		return plan, err
	}
	if len(marker.Tools) == 0 {
		plan.WasNoop = true
		return plan, nil
	}
	plan.ToolsRemoved = append([]string{}, marker.Tools...)

	if opts.DryRun {
		return plan, nil
	}

	raw, err := g.readMap()
	if err != nil {
		return plan, err
	}
	deny := readDenyField(raw, g.denyField)
	toRemove := stringSet(marker.Tools)
	var keep []string
	for _, t := range deny {
		if !toRemove[t] {
			keep = append(keep, t)
		}
	}
	writeDenyField(raw, g.denyField, keep)
	if err := g.writeMap(raw); err != nil {
		return plan, fmt.Errorf("write settings: %w", err)
	}
	if err := os.Remove(g.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("remove marker: %w", err)
	}
	return plan, nil
}

func (g *genericAdapter) Status() (Status, error) {
	s := Status{
		Adapter:      g.Name(),
		Detected:     g.Detected(),
		SettingsPath: g.settingsPath(),
	}
	if !s.Detected {
		s.Notes = "config directory not detected on this host"
		return s, nil
	}
	marker, err := g.readMarker()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	s.Claimed = len(marker.Tools) > 0
	s.DisabledByUs = append([]string{}, marker.Tools...)
	sort.Strings(s.DisabledByUs)
	return s, nil
}

// ── settings IO ────────────────────────────────────────────────────

func (g *genericAdapter) readMap() (map[string]any, error) {
	out := map[string]any{}
	b, err := os.ReadFile(g.settingsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", g.settingsPath(), err)
	}
	return out, nil
}

func (g *genericAdapter) writeMap(m map[string]any) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.settingsPath()), 0o755); err != nil {
		return err
	}
	return atomicWriteJSON(g.settingsPath(), append(body, '\n'))
}

func (g *genericAdapter) readMarker() (markerShape, error) {
	var m markerShape
	b, err := os.ReadFile(g.markerPath())
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse marker: %w", err)
	}
	return m, nil
}

func (g *genericAdapter) writeMarker(tools []string) error {
	if err := os.MkdirAll(filepath.Dir(g.markerPath()), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(markerShape{Version: 2, Tools: tools}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteJSON(g.markerPath(), append(body, '\n'))
}

// readDenyField extracts a top-level []string from m at the given
// key, tolerating absent / wrong-type entries by returning nil.
func readDenyField(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// writeDenyField sets m[key] = []any(values), or deletes the key
// when values is empty (keeps the JSON tidy).
func writeDenyField(m map[string]any, key string, values []string) {
	if len(values) == 0 {
		delete(m, key)
		return
	}
	any := make([]any, 0, len(values))
	for _, v := range values {
		any = append(any, v)
	}
	m[key] = any
}

// ── adapter registrations ─────────────────────────────────────────

// hermesAgentAdapter and openclawAdapter are best-effort. Both are
// recent / niche projects whose config layout may evolve; the
// pathOverride hook lets tests + future patches retarget without
// rewriting the adapter file.
//
// Field name: "disabled_tools" — a generic enough convention that
// works as a starting point. The first user PR that hits a real
// install can correct the path or field if the upstream uses
// something else.
var (
	hermesAgentAdapter = &genericAdapter{
		name:        "hermes-agent",
		settingsRel: ".hermes-agent/settings.json",
		denyField:   "disabled_tools",
	}
	openclawAdapter = &genericAdapter{
		name:        "openclaw",
		settingsRel: ".openclaw/settings.json",
		denyField:   "disabled_tools",
	}
)

// SetGenericAdapterPath retargets one of the generic adapters at a
// custom path. Test-only; production code never calls this.


func init() {
	Register(hermesAgentAdapter)
	Register(openclawAdapter)
}
