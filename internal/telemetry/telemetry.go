// Package telemetry — anonymous, opt-in PostHog event emission for
// clawtool (ADR-014 F5, gemini's R4 pick).
//
// Strict guarantee: never emits prompts, paths, file contents,
// secrets, or env values. The CLI dispatcher strips arg slices
// before passing to Track; we additionally allow-list the keys that
// can ride on a payload.
//
// Per ADR-007 we wrap github.com/posthog/posthog-go. The client is
// nil-safe; passing nil to Track is a no-op so call sites don't
// need to gate every call.
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	posthog "github.com/posthog/posthog-go"
)

// Embedded cogitave PostHog project credentials. Public client-side
// key — same convention as posthog-js shipping the key in browser
// bundles. Operators who want their telemetry routed to a different
// project override `[telemetry] api_key` / `host` in config.toml; an
// empty operator key falls back to these baked-in defaults so opting
// in via `clawtool onboard` Just Works.
const (
	cogitavePostHogKey  = "phc_uew8RTmHh9TCzwLg7zdsDGdegEaPy9EjJuaoYcEeVTUp"
	cogitavePostHogHost = "https://eu.i.posthog.com"
)

// Client wraps a PostHog client + the per-host anonymous distinct ID.
// Nil-safe: `(*Client)(nil).Track(...)` is a clean no-op.
type Client struct {
	mu         sync.Mutex
	enabled    bool
	distinctID string
	client     posthog.Client
}

// allowedKeys is the strict allow-list for payload properties.
// Anything else gets dropped before the event reaches PostHog.
//
// Every key here MUST be either an enumerable / public-catalog value
// (recipe names, sandbox engine names, agent families) or a
// process-level metric (duration, exit code, error class). NEVER
// add anything that could carry user-typed text, file paths, env
// values, secret material, or instance-specific identifiers
// (`claude-personal`, repo slugs, host names).
var allowedKeys = map[string]bool{
	"command":        true,
	"subcommand":     true, // first sub-arg of a verb (e.g. "source add" → "add")
	"version":        true,
	"os":             true,
	"arch":           true,
	"duration_ms":    true,
	"exit_code":      true,
	"error_class":    true,
	"outcome":        true, // taxonomy: "success" | "error" | "skipped" | "timeout" | "cancelled"
	"agent":          true, // family name only, never instance ID
	"bridge":         true, // bridge family being installed/upgraded/removed
	"recipe":         true, // public recipe name from internal/setup catalog
	"engine":         true, // sandbox engine: bwrap | sandbox-exec | docker | noop
	"event_kind":     true, // optional sub-categorisation for high-cardinality events
	"flags":          true, // CSV of feature-toggle flags used (--async, --unattended, --json, …)
	"install_method": true, // taxonomy: "script" | "brew" | "go-install" | "release" | "docker" | "manual" | "unknown"
	"update_outcome": true, // taxonomy: "up_to_date" | "update_available" | "check_failed"
	"transport":      true, // taxonomy: "stdio" | "http" — distinguishes ServeStdio respawn-per-call from the persistent HTTP daemon (v0.22.23-cycle).
}

// New initialises the client when telemetry is enabled. Disabled
// config returns a nil-friendly client (Track is a no-op). Init
// failures degrade silently — telemetry is never load-bearing.
//
// API key precedence: cfg.APIKey > cogitavePostHogKey baked-in
// default. Same for host. Operator-provided values always win so a
// self-hosted PostHog instance can capture the data instead of the
// shared cogitave project.
func New(cfg config.TelemetryConfig) *Client {
	if !cfg.Enabled {
		return &Client{enabled: false}
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = cogitavePostHogKey
	}
	host := cfg.Host
	if host == "" {
		host = cogitavePostHogHost
	}
	if apiKey == "" {
		return &Client{enabled: false}
	}
	c, err := posthog.NewWithConfig(apiKey, posthog.Config{Endpoint: host})
	if err != nil {
		return &Client{enabled: false}
	}
	id, _ := loadOrCreateAnonymousID()
	return &Client{
		enabled:    true,
		distinctID: id,
		client:     c,
	}
}

// Track emits one event. Properties outside the allow-list are
// silently dropped. Safe to call on a nil receiver.
//
// The c.client nil-check happens under c.mu so a Track racing a
// Close (which sets c.client = nil) can't dereference a nil
// posthog.Client. Pre-fix this checked nil OUTSIDE the lock then
// called Enqueue inside the lock — a Close that won the lock-race
// nil'd the field, and the next Track passed the outside-check
// only to nil-deref under the lock.
func (c *Client) Track(event string, properties map[string]any) {
	if c == nil || !c.enabled {
		return
	}
	clean := posthog.Properties{}
	for k, v := range properties {
		if !allowedKeys[k] {
			continue
		}
		clean[k] = v
	}
	clean["os"] = runtime.GOOS
	clean["arch"] = runtime.GOARCH
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return
	}
	_ = c.client.Enqueue(posthog.Capture{
		DistinctId: c.distinctID,
		Event:      event,
		Properties: clean,
	})
}

// Close flushes pending events. Idempotent.
func (c *Client) Close() error {
	if c == nil || !c.enabled || c.client == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.client.Close()
	c.client = nil
	return err
}

// Enabled reports whether the client will actually emit. Useful for
// hot-path skips on expensive payload construction.
func (c *Client) Enabled() bool {
	if c == nil {
		return false
	}
	return c.enabled
}

// loadOrCreateAnonymousID returns a stable per-host random hex ID.
// Stored at $XDG_DATA_HOME/clawtool/telemetry-id (or
// ~/.local/share/clawtool/telemetry-id). NEVER includes hostname,
// username, or anything user-identifying.
func loadOrCreateAnonymousID() (string, error) {
	path := defaultIDPath()
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return id, nil
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	}
	return id, nil
}

func defaultIDPath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "telemetry-id")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "clawtool", "telemetry-id")
	}
	return "telemetry-id"
}

// global is the process-wide client server boot wires once. Nil
// when telemetry is disabled.
var global *Client

// SetGlobal registers the process-wide client. Idempotent.
func SetGlobal(c *Client) { global = c }

// Get returns the process-wide client (or nil when none set).
func Get() *Client { return global }

// SilentDisabled tells callers whether the env var explicitly
// disables telemetry regardless of config (for the "kill switch"
// use case operators want before talking on conference Wi-Fi).
func SilentDisabled() bool {
	v := strings.TrimSpace(os.Getenv("CLAWTOOL_TELEMETRY"))
	return v == "0" || v == "false" || v == "off"
}

// EmitInstallOnce fires a `clawtool.install` event the first time
// it's called on a host AND the telemetry client is enabled. A
// marker file under $XDG_DATA_HOME/clawtool/install-emitted ensures
// every subsequent call is a no-op. Daemon boot is the natural
// place to call this — by the time `clawtool serve` runs on a fresh
// install we've already initialised the telemetry client and the
// marker can be created safely.
//
// install_method comes from $CLAWTOOL_INSTALL_METHOD which the
// install.sh / brew formula / go install wrapper sets at install
// time. Empty / unrecognised falls through to "unknown" so we
// still get the event, just without source attribution.
//
// The marker write happens BEFORE the Track call so a posthog
// outage can't cause repeated events on each retry. Worst case:
// we lose one install event entirely. Better than counting a
// single install ten times because the network was flaky.
func EmitInstallOnce(c *Client, version string) {
	if c == nil || !c.Enabled() {
		return
	}
	path := installMarkerPath()
	if _, err := os.Stat(path); err == nil {
		return // already emitted on this host
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		return
	}
	c.Track("clawtool.install", map[string]any{
		"version":        version,
		"install_method": detectInstallMethod(),
	})
}

// detectInstallMethod reads attribution from two sources, in order:
//
//  1. $CLAWTOOL_INSTALL_METHOD env var — set by the active shell or
//     the installer script in-process.
//  2. ~/.config/clawtool/install-method file — install.sh writes
//     this so the value survives across shells without requiring a
//     rc edit. Brew formula / Go install wrapper / docker entrypoint
//     can write the same file with their respective tag.
//
// Strict taxonomy enforced via the allow-list. Anything outside maps
// to "unknown" so PostHog dashboards have a stable enum to filter on.
func detectInstallMethod() string {
	if v := readInstallMethod(); v != "" {
		switch v {
		case "script", "brew", "go-install", "release", "docker", "manual":
			return v
		}
	}
	return "unknown"
}

func readInstallMethod() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("CLAWTOOL_INSTALL_METHOD"))); v != "" {
		return v
	}
	// Honour XDG_CONFIG_HOME exclusively when set — a test that
	// redirects it to a temp dir doesn't want fall-through to the
	// host's ~/.config/clawtool/install-method file. Production
	// callers that don't set XDG fall through to the home path.
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		if b, err := os.ReadFile(filepath.Join(v, "clawtool", "install-method")); err == nil {
			return strings.ToLower(strings.TrimSpace(string(b)))
		}
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if b, err := os.ReadFile(filepath.Join(home, ".config", "clawtool", "install-method")); err == nil {
			return strings.ToLower(strings.TrimSpace(string(b)))
		}
	}
	return ""
}

func installMarkerPath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "install-emitted")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "clawtool", "install-emitted")
	}
	return "install-emitted"
}

// Compile-time guard so errors stays imported when we add stricter
// validation in the next polish patch.
var _ = errors.New
