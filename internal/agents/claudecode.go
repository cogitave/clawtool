// Claude Code adapter — mutates ~/.claude/settings.json's
// `permissions.deny` array to take native Bash/Read/Edit/Write/Grep/
// Glob/WebFetch/WebSearch out of the model's tool surface, leaving
// only mcp__clawtool__*.
//
// Wire-format note: earlier versions of this adapter wrote to a
// top-level `disabledTools` field. Claude Code 2.x ignores that key
// — the canonical mechanism is `permissions.deny`. On read we still
// recognize the legacy field and migrate it forward; on write we
// only emit `permissions.deny`.
package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func init() {
	Register(&claudeCodeAdapter{})
}

// claudeCodePathOverride lets tests redirect away from the real
// ~/.claude/settings.json. Empty in production.
var claudeCodePathOverride string

// claudeCodeAdapter mutates Claude Code's settings.json.
type claudeCodeAdapter struct{}

func (a *claudeCodeAdapter) Name() string { return "claude-code" }

// settingsPath returns the path to settings.json. Honors a test
// override and falls back to $HOME/.claude/settings.json otherwise.
func (a *claudeCodeAdapter) settingsPath() string {
	if claudeCodePathOverride != "" {
		return claudeCodePathOverride
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "settings.json"
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// markerPath sits next to settings.json. Format:
//
//	{"version": 2, "tools": ["Bash", "Read", ...]}
//
// We use the marker as the source of truth for "which tools did clawtool
// disable" so Release is precise even if the user manually edited
// settings.json between Claim and Release. Version bumped to 2 when
// the deny field moved from disabledTools to permissions.deny.
func (a *claudeCodeAdapter) markerPath() string {
	dir := filepath.Dir(a.settingsPath())
	return filepath.Join(dir, "settings.clawtool.lock")
}

func (a *claudeCodeAdapter) Detected() bool {
	dir := filepath.Dir(a.settingsPath())
	if _, err := os.Stat(dir); err == nil {
		return true
	}
	return false
}

// Claim disables every name in ClaimedToolsForClawtool that isn't
// already denied, persists the updated settings, and writes the
// marker file recording exactly which names this Claim added.
func (a *claudeCodeAdapter) Claim(opts Options) (Plan, error) {
	plan := Plan{
		Adapter:      a.Name(),
		Action:       "claim",
		SettingsPath: a.settingsPath(),
		MarkerPath:   a.markerPath(),
		DryRun:       opts.DryRun,
	}

	settings, err := a.readSettings()
	if err != nil {
		return plan, err
	}

	// Anything we'd add: tools clawtool replaces that aren't already denied.
	currentDeny := stringSet(settings.Deny)
	var toAdd []string
	for _, t := range ClaimedToolsForClawtool {
		if !currentDeny[t] {
			toAdd = append(toAdd, t)
		}
	}

	plan.ToolsAdded = toAdd

	// Force a write when we found a legacy disabledTools field, even if
	// every tool is already in Deny — that's how the orphan key gets
	// removed during a re-run after upgrade.
	if len(toAdd) == 0 && !settings.HadLegacyDisabledTools {
		plan.WasNoop = true
		return plan, nil
	}

	if opts.DryRun {
		return plan, nil
	}

	// Apply: add new entries, sort for stable output, write atomically.
	newDeny := append([]string{}, settings.Deny...)
	newDeny = append(newDeny, toAdd...)
	sort.Strings(newDeny)
	newDeny = dedupSorted(newDeny)
	settings.Deny = newDeny

	if err := a.writeSettings(settings); err != nil {
		return plan, fmt.Errorf("write settings: %w", err)
	}

	if err := a.writeMarker(toAdd); err != nil {
		// We rolled forward on settings; failing to write the marker
		// means future Release won't know what to undo. Best to surface
		// loudly rather than silently.
		return plan, fmt.Errorf("write marker (settings already updated; consider rolling back manually): %w", err)
	}

	return plan, nil
}

// Release reads the marker file and removes exactly those tools from
// permissions.deny — preserving any deny entries the user added for
// unrelated reasons.
func (a *claudeCodeAdapter) Release(opts Options) (Plan, error) {
	plan := Plan{
		Adapter:      a.Name(),
		Action:       "release",
		SettingsPath: a.settingsPath(),
		MarkerPath:   a.markerPath(),
		DryRun:       opts.DryRun,
	}

	marker, err := a.readMarker()
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

	settings, err := a.readSettings()
	if err != nil {
		return plan, err
	}

	toRemove := stringSet(marker.Tools)
	var keep []string
	for _, t := range settings.Deny {
		if !toRemove[t] {
			keep = append(keep, t)
		}
	}
	settings.Deny = keep

	if err := a.writeSettings(settings); err != nil {
		return plan, fmt.Errorf("write settings: %w", err)
	}

	if err := os.Remove(a.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("remove marker: %w", err)
	}

	return plan, nil
}

// Status reads the marker (if present) and reports what's claimed.
func (a *claudeCodeAdapter) Status() (Status, error) {
	s := Status{
		Adapter:      a.Name(),
		Detected:     a.Detected(),
		SettingsPath: a.settingsPath(),
	}
	if !s.Detected {
		s.Notes = "directory ~/.claude not detected"
		return s, nil
	}
	marker, err := a.readMarker()
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

// ── settings.json read / write ─────────────────────────────────────────

// settingsShape holds the deny list plus everything else as an
// untyped map so we round-trip user keys we don't understand.
//
// HadLegacyDisabledTools is set during read when a top-level
// `disabledTools` field is found and folded into Deny + dropped.
// Claim consults it so a re-run after upgrading clawtool always
// rewrites the file (even when the deny list looks already-complete)
// to clean up the orphan field.
type settingsShape struct {
	Deny                   []string
	HadLegacyDisabledTools bool

	// Everything else lives here, including `permissions` minus
	// the `deny` array (which we hoist into Deny).
	rest map[string]any
}

func (a *claudeCodeAdapter) readSettings() (*settingsShape, error) {
	out := &settingsShape{rest: map[string]any{}}
	b, err := os.ReadFile(a.settingsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return out, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", a.settingsPath(), err)
	}
	if raw == nil {
		return out, nil
	}

	// Hoist permissions.deny if present.
	if perms, ok := raw["permissions"].(map[string]any); ok {
		if v, ok := perms["deny"]; ok {
			out.Deny = stringArray(v)
			delete(perms, "deny")
		}
		if len(perms) == 0 {
			delete(raw, "permissions")
		} else {
			raw["permissions"] = perms
		}
	}

	// Migrate legacy top-level disabledTools into Deny if present.
	// Claude Code 2.x ignores this key; we fold it into the canonical
	// permissions.deny list and drop the orphan.
	if v, ok := raw["disabledTools"]; ok {
		legacy := stringArray(v)
		if len(legacy) > 0 {
			out.Deny = append(out.Deny, legacy...)
			sort.Strings(out.Deny)
			out.Deny = dedupSorted(out.Deny)
		}
		delete(raw, "disabledTools")
		out.HadLegacyDisabledTools = true
	}

	out.rest = raw
	return out, nil
}

func (a *claudeCodeAdapter) writeSettings(s *settingsShape) error {
	out := map[string]any{}
	for k, v := range s.rest {
		out[k] = v
	}

	// Re-attach permissions.deny if non-empty. Preserve any other
	// keys under `permissions` the user already had (e.g. `allow`,
	// `defaultMode`).
	if len(s.Deny) > 0 {
		var perms map[string]any
		if existing, ok := out["permissions"].(map[string]any); ok {
			perms = existing
		} else {
			perms = map[string]any{}
		}
		anyDeny := make([]any, 0, len(s.Deny))
		for _, t := range s.Deny {
			anyDeny = append(anyDeny, t)
		}
		perms["deny"] = anyDeny
		out["permissions"] = perms
	} else if existing, ok := out["permissions"].(map[string]any); ok {
		// Empty deny: drop the key but preserve sibling permissions
		// entries the user might depend on.
		delete(existing, "deny")
		if len(existing) == 0 {
			delete(out, "permissions")
		} else {
			out["permissions"] = existing
		}
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.settingsPath()), 0o755); err != nil {
		return err
	}
	return atomicWriteJSON(a.settingsPath(), append(body, '\n'))
}

// stringArray normalizes JSON-decoded `any` back to []string. Used
// on read for both permissions.deny and the legacy disabledTools
// field.
func stringArray(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ── marker read / write ────────────────────────────────────────────────

type markerShape struct {
	Version int      `json:"version"`
	Tools   []string `json:"tools"`
}

func (a *claudeCodeAdapter) readMarker() (markerShape, error) {
	var m markerShape
	b, err := os.ReadFile(a.markerPath())
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse marker %s: %w", a.markerPath(), err)
	}
	return m, nil
}

func (a *claudeCodeAdapter) writeMarker(tools []string) error {
	if err := os.MkdirAll(filepath.Dir(a.markerPath()), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(markerShape{Version: 2, Tools: tools}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteJSON(a.markerPath(), append(body, '\n'))
}

// ── helpers ────────────────────────────────────────────────────────────

// atomicWriteJSON mirrors internal/tools/core/atomic.go's writeAtomic
// but locally so this package doesn't import core. Same temp+rename
// pattern: writers never observe a half-written settings file.
func atomicWriteJSON(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, ".clawtool-agent-"+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stringSet(xs []string) map[string]bool {
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}

func dedupSorted(xs []string) []string {
	if len(xs) <= 1 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

// SetClaudeCodeSettingsPath redirects the adapter to a custom settings
// path. Tests use it; production should never call it.
func SetClaudeCodeSettingsPath(p string) { claudeCodePathOverride = p }
