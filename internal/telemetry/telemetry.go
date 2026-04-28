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
var allowedKeys = map[string]bool{
	"command":     true,
	"version":     true,
	"os":          true,
	"arch":        true,
	"duration_ms": true,
	"exit_code":   true,
	"error_class": true,
	"agent":       true, // family name only, never instance ID (could leak `claude-personal`)
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
func (c *Client) Track(event string, properties map[string]any) {
	if c == nil || !c.enabled || c.client == nil {
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

// Compile-time guard so errors stays imported when we add stricter
// validation in the next polish patch.
var _ = errors.New
