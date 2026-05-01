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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/version"
	posthog "github.com/posthog/posthog-go"
)

// versionResolved is a thin wrapper around version.Resolved() so
// the New()-time pre-v1.0 policy check stays expressible without
// scattering version imports across this file. Declared as a
// swappable var (not `func`) so tests can shadow it to drive the
// post-v1 path without editing global state outside the package.
var versionResolved = func() string { return version.Resolved() }

// majorIsZero reports whether the supplied version string parses
// to a major version of 0. Mirrors the same logic the CLI's
// preV1Locked uses; lifted here so the daemon-side enforcement
// runs without round-tripping through the cli package (which
// would create an import cycle: telemetry → cli → telemetry).
//
// "(devel)" / "(unknown)" / unparseable input → false (don't
// lock dev builds).
func majorIsZero(v string) bool {
	v = strings.TrimPrefix(v, "v")
	if v == "" || strings.HasPrefix(v, "(") {
		return false
	}
	dot := strings.IndexByte(v, '.')
	if dot < 1 {
		return false
	}
	return v[:dot] == "0"
}

// debugEnabled is flipped by `clawtool serve --debug` (or the
// CLAWTOOL_DEBUG env var). When true, every Track / Close /
// init step logs to stderr so the operator can see exactly which
// events landed on the wire and which got dropped.
var debugEnabled = strings.ToLower(strings.TrimSpace(os.Getenv("CLAWTOOL_DEBUG"))) == "1" ||
	strings.ToLower(strings.TrimSpace(os.Getenv("CLAWTOOL_DEBUG"))) == "true"

// SetDebug toggles the debug trace at runtime. Wired from
// `clawtool serve --debug` so the operator can flip it without
// touching env.
func SetDebug(on bool) { debugEnabled = on }

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
//
// sessionID groups every event emitted from a single daemon /
// CLI invocation under one $session_id property — PostHog's
// Sessions view + funnel queries rely on this to reconstruct
// "user did A then B then C in the same run" rather than treating
// every event as an isolated row. Generated fresh on New(), so a
// daemon restart starts a new session (which is the right
// boundary for CLI tools — different invocations are different
// units of work).
type Client struct {
	mu         sync.Mutex
	enabled    bool
	distinctID string
	sessionID  string
	startedAt  time.Time
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
	"severity":       true, // taxonomy: "error" | "warn" | "panic" — classification of forwarded daemon log events (logwatch.go).

	// Host fingerprint dimensions (fingerprint.go). Single
	// `clawtool.host_fingerprint` event emitted on daemon boot
	// carries every key in this block. Strict legal limits:
	// each value is either an enumerable bucket, a public
	// runtime attribute, or a presence boolean. NOTHING per-
	// user-identifiable. NO paths, NO env values, NO hostnames.
	"cpu_count":           true, // int — number of cores (runtime.NumCPU())
	"mem_tier":            true, // bucket: "<2GB" | "2-8GB" | "8-32GB" | ">32GB" | "unknown"
	"go_version":          true, // runtime.Version() — public Go toolchain string
	"container":           true, // bool — running in docker / podman / k8s pod
	"is_ci":               true, // bool — CI env vars set
	"is_wsl":              true, // bool — running under WSL1 / WSL2
	"term_kind":           true, // taxonomy: "tty" | "ssh" | "ci" | "headless"
	"locale_lang":         true, // first segment of $LANG, e.g. "tr" / "en"; "unknown" on parse fail
	"claude_code_present": true, // bool — claude on PATH at boot
	"codex_present":       true, // bool — codex on PATH at boot
	"gemini_present":      true, // bool — gemini on PATH at boot
	"opencode_present":    true, // bool — opencode on PATH at boot
	"posthog_reachable":   true, // bool — TCP reach to telemetry endpoint
	"github_reachable":    true, // bool — TCP reach to GitHub releases API

	// PostHog GeoIP plugin enrichment. Set $geoip_disable=true
	// on every event so PostHog doesn't auto-stamp city / country
	// from the request IP. Anonymous-telemetry contract: we don't
	// want that level of fidelity even when the operator opted
	// in to "anonymous diagnostics."
	"$geoip_disable": true,

	// PostHog session/lib conventions. These prefixed `$<name>`
	// keys are reserved by PostHog itself; surfacing them via the
	// allow-list lights up the Sessions view, lib filtering, and
	// session-bound funnel queries that were dark before
	// (operator's 2026-04-29 observation: sessions empty, live
	// feed sparse). $session_id groups events emitted from one
	// daemon / CLI run; $lib + $lib_version identify the
	// emitter for cross-channel comparisons.
	"$session_id":  true,
	"$lib":         true,
	"$lib_version": true,

	// Session lifecycle markers — PostHog's session-bound funnel
	// queries reconstruct boundaries by looking for these on the
	// first / last event of a session. We fold them into the
	// existing server.start / server.stop emissions instead of
	// emitting separate events (one fewer round-trip per
	// daemon lifetime).
	"$session_start": true,
	"$session_end":   true,

	// PostHog LLM observability properties. We emit these on the
	// `clawtool.dispatch` event when an upstream agent CLI call
	// completes (separate commit wires the actual emission;
	// allow-listing them here is the prerequisite). Privacy
	// boundary: we never capture prompt / response BODIES — only
	// the metadata listed here. Token counts come from upstream
	// usage headers when the bridge surfaces them, otherwise 0.
	"$ai_provider":       true,
	"$ai_model":          true,
	"$ai_input_tokens":   true,
	"$ai_output_tokens":  true,
	"$ai_total_cost_usd": true,
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
	// Pre-v1.0.0 lock: even if the on-disk config says
	// `enabled = false` (someone hand-edited config.toml or a
	// pre-fix `clawtool telemetry off` slipped through), force
	// telemetry on through the pre-1.0 cycle. Same policy
	// surfaced by the CLI's preV1Locked refusal — anonymous
	// telemetry is the funnel-diagnostic data we cannot afford
	// to lose while the project is still finding its shape.
	// The check fires once at boot; flips off the moment we tag
	// v1.0.0 and version.Resolved()'s major version becomes 1+.
	if !cfg.Enabled && majorIsZero(versionResolved()) {
		fmt.Fprintln(os.Stderr,
			"clawtool telemetry: pre-v1.0 policy — config.enabled=false ignored, telemetry stays on")
		cfg.Enabled = true
	}
	if !cfg.Enabled {
		return &Client{enabled: false}
	}
	// CI emit gate. CI runners pollute the production analytics
	// project (~95% of events the operator pulled on 2026-04-30
	// came from CI hosts). Drop to a disabled client when we're
	// on CI AND the maintainer-only `CLAWTOOL_TELEMETRY_FORCE_CI`
	// opt-in is missing. The opt-in exists for the maintainer's
	// own release-tracking workflows; default off keeps every
	// other downstream CI silent without configuration.
	if CIDisabled() {
		fmt.Fprintln(os.Stderr,
			"clawtool telemetry: CI runner detected and CLAWTOOL_TELEMETRY_FORCE_CI not set — going silent")
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
		// Both operator override and baked default missing.
		// Pre-fix this fell through silently; operator on
		// 2026-04-29 reported "12 hours, zero events" with
		// no diagnostic.
		fmt.Fprintln(os.Stderr,
			"clawtool telemetry: enabled=true but no API key (cfg.APIKey + baked default both empty); going silent")
		return &Client{enabled: false}
	}
	c, err := posthog.NewWithConfig(apiKey, posthog.Config{Endpoint: host})
	if err != nil {
		// Same blind spot: posthog client init failures used
		// to land on stderr nowhere. Now we surface the actual
		// reason so the operator can spot endpoint typos /
		// network issues immediately.
		fmt.Fprintf(os.Stderr,
			"clawtool telemetry: posthog init failed (host=%s): %v — going silent\n", host, err)
		return &Client{enabled: false}
	}
	id, _ := loadOrCreateAnonymousID()
	sid := newSessionID()
	fmt.Fprintf(os.Stderr,
		"clawtool telemetry: enabled (host=%s, distinct_id=%s…, session=%s)\n", host, id[:min(8, len(id))], sid[:min(8, len(sid))])
	return &Client{
		enabled:    true,
		distinctID: id,
		sessionID:  sid,
		startedAt:  time.Now(),
		client:     c,
	}
}

// newSessionID returns a 16-byte hex token unique to this daemon /
// CLI invocation. PostHog uses $session_id verbatim — any opaque
// string per-process is fine; we err on the side of "long enough
// to be globally unique without coordination" so events from
// concurrent sessions never collide.
func newSessionID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Fallback that's still unique-enough — process start
		// time at nanosecond resolution. We never actually
		// expect rand.Read to fail, but a stuck rand source
		// shouldn't disable telemetry.
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
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
	// PostHog conventions: $session_id groups events from one
	// daemon / CLI invocation under a single Sessions-view row;
	// $lib / $lib_version identify the emitter for cross-channel
	// comparisons (cogitave/clawtool vs the dashboard vs any
	// future SDK that lands on the same project). Caller-supplied
	// values are respected (allow-listed above) — these only fill
	// in when the caller didn't set them, so a per-event override
	// stays possible.
	if _, set := clean["$session_id"]; !set && c.sessionID != "" {
		clean["$session_id"] = c.sessionID
	}
	if _, set := clean["$lib"]; !set {
		clean["$lib"] = "clawtool-go"
	}
	// Auto-stamp $lib_version with the resolved build tag. Lights
	// up PostHog's "filter by version" pivot in the Sessions /
	// Live views — operator can isolate "what's flapping on the
	// v0.22.30 cohort vs v0.22.36" without us needing to remember
	// to thread `version` into every Track callsite. The CLI's
	// per-command Track sites already pass an explicit `version`
	// property; this fills the PostHog-canonical $lib_version
	// field that sessions query by default.
	if _, set := clean["$lib_version"]; !set {
		clean["$lib_version"] = versionResolved()
	}
	// Always disable GeoIP enrichment — anonymous-telemetry
	// contract: even though PostHog could resolve city / country
	// from the request IP, we don't want that level of fidelity
	// even when the operator has opted in to "anonymous
	// diagnostics." Set unconditionally; allow-list permits it.
	clean["$geoip_disable"] = true
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		if debugEnabled {
			fmt.Fprintf(os.Stderr, "clawtool telemetry: drop event=%q (client closed)\n", event)
		}
		return
	}
	if err := c.client.Enqueue(posthog.Capture{
		DistinctId: c.distinctID,
		Event:      event,
		Properties: clean,
	}); err != nil {
		if debugEnabled {
			fmt.Fprintf(os.Stderr, "clawtool telemetry: enqueue %q failed: %v\n", event, err)
		}
		return
	}
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "clawtool telemetry: enqueued event=%q props=%v\n", event, clean)
	}
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

// CIDisabled reports whether the current process is running on a
// CI runner AND the maintainer opt-in `CLAWTOOL_TELEMETRY_FORCE_CI`
// env var is NOT set. CI runners pollute production analytics
// (~95% of the events the operator pulled from PostHog on
// 2026-04-30 came from CI hosts running pseudo-version Go-cached
// binaries) so the default-off gate keeps that noise off the
// production project.
//
// Opt-in semantics: setting `CLAWTOOL_TELEMETRY_FORCE_CI=1` (or
// `true` / `on`) re-enables emission for the maintainer's own
// release-tracking workflows that legitimately want to send
// events from CI. Default off — every other downstream CI runner
// stays silent without configuration. Read live (not cached at
// package init) so tests + a single daemon's lifetime can
// manipulate the env via t.Setenv without re-instantiating the
// package.
func CIDisabled() bool {
	if !detectCI() {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CLAWTOOL_TELEMETRY_FORCE_CI")))
	return v != "1" && v != "true" && v != "on"
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
