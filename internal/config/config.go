// Package config reads, writes, and resolves the clawtool configuration.
//
// Schema mirrors ADR-006: core_tools, sources, tools (per-selector overrides),
// tags, groups, profile. v0.2 implements parsing of the full schema and the
// tool-level + server-level enabled resolver. Tag- and group-level resolution
// land in v0.3 once a source instance has actually been wired so we have
// real tools to tag.
//
// Path resolution honors $XDG_CONFIG_HOME, falling back to ~/.config/clawtool.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/pelletier/go-toml/v2"
)

// Config is the full on-disk shape of ~/.config/clawtool/config.toml.
type Config struct {
	CoreTools     map[string]CoreTool        `toml:"core_tools,omitempty"`
	Sources       map[string]Source          `toml:"sources,omitempty"`
	Tools         map[string]ToolOverride    `toml:"tools,omitempty"`
	Tags          map[string]TagRule         `toml:"tags,omitempty"`
	Groups        map[string]GroupDef        `toml:"groups,omitempty"`
	Profile       ProfileConfig              `toml:"profile,omitempty"`
	Agents        map[string]AgentConfig     `toml:"agents,omitempty"`
	Bridges       map[string]BridgeOverrides `toml:"bridge,omitempty"`
	Dispatch      Dispatch                   `toml:"dispatch,omitempty"`
	Observability ObservabilityConfig        `toml:"observability,omitempty"`
	AutoLint      AutoLintConfig             `toml:"auto_lint,omitempty"`
	Hooks         HooksConfig                `toml:"hooks,omitempty"`
	// Telemetry deliberately drops `omitempty` for the same reason
	// TelemetryConfig.Enabled does — a struct that nests a
	// load-bearing `false` must round-trip to disk explicitly.
	// Without this, a fresh `Default()` (Enabled=false, APIKey="",
	// Host="") would write zero-value fields and the encoder would
	// see the whole TelemetryConfig as empty and skip the section
	// entirely, defeating the v0.22.19+ explicit-opt-out path.
	Telemetry     TelemetryConfig          `toml:"telemetry"`
	Portals       map[string]PortalConfig  `toml:"portals,omitempty"`
	Sandboxes     map[string]SandboxConfig `toml:"sandboxes,omitempty"`
	SandboxWorker SandboxWorkerConfig      `toml:"sandbox_worker,omitempty"`
	Peer          PeerConfig               `toml:"peer,omitempty"`
	Checkpoint    CheckpointConfig         `toml:"checkpoint,omitempty"`
}

// CheckpointConfig carries checkpoint-subsystem toggles. Per ADR-022
// §Resolved the checkpoint umbrella is one TOML section so future
// pieces (snapshot/restore, dirty-tree guard, autocommit cadence)
// land here without churning the schema. Today only [checkpoint.guard]
// is wired.
type CheckpointConfig struct {
	Guard GuardConfig `toml:"guard,omitempty"`
}

// GuardConfig configures the defense-in-depth checkpoint Guard
// middleware (internal/checkpoint.Guard) layered atop ADR-021's
// Read-before-Write invariant. The contract:
//
//   - Enabled = false (default) → middleware is a strict no-op.
//     The hook still fires from Edit/Write but Check() always
//     returns nil. Zero overhead beyond a mutex acquire.
//   - Enabled = true → Guard tracks the number of edits since the
//     last `wip!:` autocommit (or a real Conventional commit) and
//     refuses the next pre-edit when the counter reaches
//     MaxEditsWithoutCheckpoint. Operator unblocks by calling
//     `clawtool checkpoint save` (autocommit) or by landing a
//     real commit.
//
// MaxEditsWithoutCheckpoint defaults to 5 when zero or negative,
// matching the wizard's prompt copy. The constant lives in guard.go
// (DefaultMaxEditsWithoutCheckpoint) so callers and tests reference
// one canonical value.
//
// Why opt-in: defense-in-depth atop Read-before-Write means most
// operators won't need it — RbW already catches the common
// "agent stomped my unread file" failure. Guard is for the small
// set of agent runs that explicitly bypass RbW with
// unsafe_overwrite_without_read=true (e.g. autodev sessions on
// fresh worktrees) where the operator wants a hard cap on
// uncheckpointed mutation depth.
type GuardConfig struct {
	// Enabled gates the entire middleware. Default false; opt-in
	// via wizard or hand-edited config.toml.
	Enabled bool `toml:"enabled,omitempty"`
	// MaxEditsWithoutCheckpoint is the threshold N: once N edits
	// have landed since the last checkpoint, the next pre-edit
	// hook returns ErrCheckpointRequired. Zero or negative → use
	// the package default (5). The hard ceiling is enforced in
	// guard.Check, not here, so config round-trips a literal 0 as
	// "use default" rather than "disable" (Enabled is the disable
	// switch).
	MaxEditsWithoutCheckpoint int `toml:"max_edits_without_checkpoint,omitempty"`
}

// PeerConfig holds per-feature toggles for the peer registry / a2a
// surface. AutoClosePanes flips off the SendMessage auto-close
// lifecycle hook for power users who want auto-spawned tmux panes
// to stick around for post-mortem inspection. AutoCloseGraceSeconds
// adds a configurable delay between the task hitting terminal status
// and the pane being killed — useful when an operator wants a few
// seconds of "the agent just finished, let me read the last reply"
// before the window snaps shut. Default is on (close immediately) —
// without it, the user's "şişer" case triggers within minutes of
// normal usage.
type PeerConfig struct {
	// AutoClosePanes is a pointer so nil means default-on. An
	// explicit `false` (operator opt-out) round-trips to disk and
	// flips agents.SetAutoClosePanes(false) at boot, leaving every
	// auto-spawned pane open after its dispatch terminates.
	AutoClosePanes *bool `toml:"auto_close_panes,omitempty"`

	// AutoCloseGraceSeconds defers the kill-pane after a task lands
	// in terminal status by N seconds. Default 0 = immediate close
	// (legacy behaviour). When > 0, the lifecycle hook schedules
	// the kill via time.AfterFunc and cancels the timer if a fresh
	// dispatch lands on the same auto-spawned peer before the
	// grace window elapses (so back-to-back tasks don't kill the
	// pane mid-second-task). Negative values are treated as 0.
	AutoCloseGraceSeconds int `toml:"auto_close_grace_seconds,omitempty"`
}

// SandboxWorkerConfig wires the daemon to a sandbox-worker
// container (ADR-029). When Mode != "off", Bash / Read / Edit /
// Write tool calls route through the worker WebSocket instead of
// shelling out on the host process. Defaults preserve the v0.21.5
// behaviour: Mode="off" — every tool runs in the daemon's own
// process. Operator opts in by flipping Mode to "container" and
// pointing URL at the container's exposed port.
type SandboxWorkerConfig struct {
	// Mode is "off" (default), "host" (worker on the same host),
	// or "container" (worker reachable over the network at URL).
	Mode string `toml:"mode,omitempty"`
	// URL is the worker's WebSocket endpoint, e.g.
	// "ws://127.0.0.1:2024/ws". Required when Mode != "off".
	URL string `toml:"url,omitempty"`
	// TokenFile is the path to the bearer-token file shared with
	// the worker. Default $XDG_CONFIG_HOME/clawtool/worker-token.
	TokenFile string `toml:"token_file,omitempty"`
	// AutoStart asks the daemon to spawn `clawtool sandbox-worker`
	// (or pull + run a container, future work) when no live
	// worker is reachable. Phase 1 surfaces the flag but does not
	// implement spawn — operator runs the worker manually.
	AutoStart bool `toml:"auto_start,omitempty"`
	// Image is the docker image tag the operator built (or
	// pulled) for the worker container. Phase 2 will use it for
	// auto_start; Phase 1 stores it as documentation.
	Image string `toml:"image,omitempty"`
}

// SandboxConfig is one [sandboxes.<name>] profile (ADR-020).
// Engine adapters in internal/sandbox/ render this into the
// host-native sandbox flags (bwrap, sandbox-exec, docker, …).
type SandboxConfig struct {
	Description string         `toml:"description,omitempty"`
	Paths       []SandboxPath  `toml:"paths,omitempty"`
	Network     SandboxNetwork `toml:"network,omitempty"`
	Limits      SandboxLimits  `toml:"limits,omitempty"`
	Env         SandboxEnv     `toml:"env,omitempty"`
}

// SandboxPath is one filesystem rule. Mode is "ro" | "rw" | "none".
type SandboxPath struct {
	Path string `toml:"path"`
	Mode string `toml:"mode"`
}

// SandboxNetwork covers the egress policy. Policy is one of:
// "none" | "loopback" | "allowlist" | "open".
type SandboxNetwork struct {
	Policy string   `toml:"policy,omitempty"`
	Allow  []string `toml:"allow,omitempty"`
}

// SandboxLimits maps to engine-specific resource flags. Strings
// (e.g. "5m", "1GB") are parsed by the engine adapter so the
// schema stays human-friendly in TOML.
type SandboxLimits struct {
	Timeout      string `toml:"timeout,omitempty"`
	Memory       string `toml:"memory,omitempty"`
	CPUShares    int    `toml:"cpu_shares,omitempty"`
	ProcessCount int    `toml:"process_count,omitempty"`
}

// SandboxEnv selects which host env vars survive into the
// sandboxed process. Allow + deny semantics are AND-ed: deny
// patterns trump matching allow entries.
type SandboxEnv struct {
	Allow []string `toml:"allow,omitempty"`
	Deny  []string `toml:"deny,omitempty"`
}

// PortalConfig is one saved web-UI target (ADR-018). Selectors,
// predicates, and browser flags live here; cookies live in
// secrets.toml under SecretsScope.
//
// Per ADR-017 a portal is a Tool-surface concept, not a Transport.
// PortalAsk drives Obscura's CDP server through the steps declared
// here; new portals are config-only.
type PortalConfig struct {
	Name                  string                `toml:"name,omitempty"`
	BaseURL               string                `toml:"base_url"`
	StartURL              string                `toml:"start_url,omitempty"` // defaults to BaseURL
	SecretsScope          string                `toml:"secrets_scope"`       // points at [scopes."portal.<name>"] in secrets.toml
	AuthCookieNames       []string              `toml:"auth_cookie_names,omitempty"`
	TimeoutMs             int                   `toml:"timeout_ms,omitempty"` // default 180000
	LoginCheck            PortalPredicate       `toml:"login_check,omitempty"`
	ReadyPredicate        PortalPredicate       `toml:"ready_predicate,omitempty"`
	Selectors             PortalSelectors       `toml:"selectors"`
	ResponseDonePredicate PortalPredicate       `toml:"response_done_predicate"`
	Headers               map[string]string     `toml:"headers,omitempty"`
	Browser               PortalBrowserSettings `toml:"browser,omitempty"`
}

// PortalPredicate is a "is this state truthy?" check. Three types:
//
//   - selector_exists  — `value` is a CSS selector; truthy when it matches.
//   - selector_visible — selector matches AND offsetParent != null.
//   - eval_truthy      — `value` is a JS expression evaluated in-page.
type PortalPredicate struct {
	Type  string `toml:"type"`            // selector_exists | selector_visible | eval_truthy
	Value string `toml:"value,omitempty"` // selector or JS expression depending on Type
}

// PortalSelectors carries the three CSS selectors every interactive
// chat portal needs.
type PortalSelectors struct {
	Input    string `toml:"input"`              // textarea / input the prompt goes into
	Submit   string `toml:"submit,omitempty"`   // submit button; optional when Enter dispatch is used
	Response string `toml:"response,omitempty"` // last-rendered assistant message container
}

// PortalBrowserSettings tunes the browser context Obscura spawns.
type PortalBrowserSettings struct {
	Stealth        bool   `toml:"stealth,omitempty"`
	ViewportWidth  int    `toml:"viewport_width,omitempty"`
	ViewportHeight int    `toml:"viewport_height,omitempty"`
	Locale         string `toml:"locale,omitempty"`
}

// TelemetryConfig drives anonymous PostHog event emission. Pre-1.0
// default = on (config.Default() seeds Enabled=true to match the
// onboard wizard's "default = on" claim); flips to off at v1.0.0.
// Operator opt-out: `clawtool telemetry off`. Per ADR-007 we wrap
// posthog/posthog-go.
//
// Events emitted: command name, version, OS/arch, duration_ms,
// exit_code, error_class. NO prompts, NO paths, NO secrets, NO env
// values — the CLI dispatcher strips arg slices before forwarding.
type TelemetryConfig struct {
	// Enabled deliberately drops `omitempty` — `false` is a load-
	// bearing value (explicit opt-out) that must round-trip to
	// disk so the v0.22.19+ upgrade-merge logic in Load() can
	// distinguish "user wrote enabled = false" from "user wrote
	// nothing, defaults apply." With omitempty, `false` was
	// silently stripped on Save and the next Load saw an absent
	// key, which mergeDefaults then patched back to true — the
	// `clawtool telemetry off` verb appeared to no-op across
	// restarts.
	Enabled bool   `toml:"enabled"`
	APIKey  string `toml:"api_key,omitempty"` // PostHog project key (optional; defaults baked into the binary at release time)
	Host    string `toml:"host,omitempty"`    // override the default https://app.posthog.com endpoint
}

// HooksConfig wires user shell commands to clawtool lifecycle events
// (ADR-014 F3, Claude Code parity). Each event accepts an ordered
// list of HookEntry — when the event fires, every entry runs in
// sequence; failures are logged but never abort the originating
// operation. Empty events are a zero-cost no-op.
//
// Supported events (locked at v0.15):
//
//	pre_send / post_send         — Supervisor.dispatch wrap
//	on_task_complete             — BIAM task hits a terminal state
//	pre_edit / post_edit         — Edit/Write tool wrap
//	pre_bridge_add / post_recipe_apply
//	on_server_start / on_server_stop
type HooksConfig struct {
	Events map[string][]HookEntry `toml:"events,omitempty"`
}

// HookEntry is one shell command + ergonomics. The command runs with
// JSON event metadata on stdin so user scripts can inspect the
// payload (instance, task_id, file path, …) without parsing argv.
type HookEntry struct {
	Cmd        string   `toml:"cmd"`                      // shell snippet evaluated by /bin/sh -c
	Argv       []string `toml:"argv,omitempty"`           // alternative: raw argv (skips the shell)
	TimeoutMs  int      `toml:"timeout_ms,omitempty"`     // per-hook hard cap; default 5000
	BlockOnErr bool     `toml:"block_on_error,omitempty"` // when true, hook failure errors out the originating op
}

// ObservabilityConfig drives the OpenTelemetry instrumentation that
// Supervisor.Send and Transport.startStreamingExec emit. Disabled by
// default — the no-op observer pays no allocation cost beyond a
// pointer check, so leaving it off has zero overhead. See ADR-014
// Phase 4 carry-over (T1) for the full design pulled from the
// 2026-04-26 multi-CLI fan-out.
type ObservabilityConfig struct {
	Enabled     bool    `toml:"enabled,omitempty"`      // master gate; default false
	ExporterURL string  `toml:"exporter_url,omitempty"` // OTLP/HTTP endpoint (e.g. http://localhost:4318)
	SampleRate  float64 `toml:"sample_rate,omitempty"`  // [0.0, 1.0]; 0 or unset → 1.0 when enabled

	// Langfuse-style auth headers. When LangfusePublicKey + Secret are
	// set, the exporter sends `Authorization: Basic base64(public:secret)`
	// and Langfuse picks the spans up via its OTel ingest endpoint. Empty
	// means a generic OTLP collector with no auth.
	LangfuseHost      string `toml:"langfuse_host,omitempty"`
	LangfusePublicKey string `toml:"langfuse_public_key,omitempty"`
	LangfuseSecretKey string `toml:"langfuse_secret_key,omitempty"`

	// ServiceName tags the resource emitted on every span. Defaults
	// to "clawtool" when empty.
	ServiceName string `toml:"service_name,omitempty"`
}

// AutoLintConfig drives the post-write lint hook in Edit/Write. Per
// ADR-014's T2 design (2026-04-26), enabled by default — agents
// self-correct in the next turn from the findings ride-along.
type AutoLintConfig struct {
	Enabled *bool `toml:"enabled,omitempty"` // pointer so nil means default-on; explicit false disables
}

// AgentConfig declares one runtime agent instance per ADR-006 instance
// scoping. Multiple instances of the same family (claude-personal,
// claude-work, codex1, …) get separate auth scopes and HOME overrides.
// Per ADR-014, the supervisor reads this map plus installed bridges
// to compose its agent registry. Phase 4 fields (Tags, FailoverTo)
// drive the dispatch policies.
type AgentConfig struct {
	Family       string   `toml:"family"`                  // CLI family ("claude", "codex", "opencode", "gemini", "hermes")
	SecretsScope string   `toml:"secrets_scope,omitempty"` // [secrets.X] section to resolve env from; defaults to instance name
	HomeOverride string   `toml:"home,omitempty"`          // optional HOME override (e.g. "~/.claude-personal") so each instance has its own auth dir
	Tags         []string `toml:"tags,omitempty"`          // labels for tag-routed dispatch ("fast", "long-context", …)
	FailoverTo   []string `toml:"failover_to,omitempty"`   // ordered fallback chain of instance names; failover policy cascades through this list on Send error
	Sandbox      string   `toml:"sandbox,omitempty"`       // ADR-020 / #163: name of a [sandboxes.<name>] profile to wrap every dispatch to this instance in. Empty = no sandbox.
}

// Dispatch configures how the supervisor resolves prompts when the
// caller doesn't pin an explicit instance. Phase 4 of ADR-014.
//
//	Mode = ""             → explicit (default; current Phase 1 behaviour)
//	Mode = "round-robin"  → rotate across same-family callable instances
//	Mode = "failover"     → primary + cascade on error (uses AgentConfig.FailoverTo)
//	Mode = "tag-routed"   → caller passes --tag/tag; supervisor picks any matching healthy instance
type Dispatch struct {
	Mode   string         `toml:"mode,omitempty"`
	Limits DispatchLimits `toml:"limits,omitempty"`
}

// DispatchLimits caps how often / concurrently a single instance can
// be dispatched to. Per-call enforcement happens inside Supervisor;
// CLI / MCP / HTTP all share the bucket. v0.15 ROI feature F1 (per
// codex's R3 research).
//
// Rate is "<n>/<duration>" (e.g. "30/m", "5/s", "1000/h"). Empty
// string disables the limiter (no waits, no errors).
// Burst is the token-bucket peak; defaults to Rate when zero.
// MaxConcurrent caps in-flight dispatches per instance; 0 = unlimited.
type DispatchLimits struct {
	Rate          string `toml:"rate,omitempty"`
	Burst         int    `toml:"burst,omitempty"`
	MaxConcurrent int    `toml:"max_concurrent,omitempty"`
}

// BridgeOverrides lets a power user point a bridge family at a
// non-canonical plugin (e.g. internal mirror, fork). Per ADR-014's
// "no install-time plugin shopping on the CLI" rule this is the
// only override surface; the CLI exposes no `--plugin` flag.
type BridgeOverrides struct {
	Plugin string `toml:"plugin,omitempty"` // org/repo of the plugin to install instead of the default
}

// CoreTool toggles a clawtool-shipped tool. Default (missing entry) = enabled.
type CoreTool struct {
	Enabled *bool `toml:"enabled,omitempty"`
}

// Source defines a sourced MCP server instance. internal/sources/manager
// spawns each Source as a child MCP process and proxies its tools through
// the supervisor (visible as `mcp__<source>__*` from the model's view).
type Source struct {
	Type    string            `toml:"type"`              // currently only "mcp"
	Command []string          `toml:"command,omitempty"` // argv to spawn the MCP server
	Env     map[string]string `toml:"env,omitempty"`     // env vars (`${VAR}` expansion at use)
}

// ToolOverride is a per-selector explicit enable/disable. Pointer so absence
// is distinguishable from `false`.
type ToolOverride struct {
	Enabled *bool `toml:"enabled,omitempty"`
}

// TagRule applies an enable/disable across every tool whose selector matches
// any pattern in `match` (glob, evaluated against the selector form).
type TagRule struct {
	Match    []string `toml:"match,omitempty"`
	Disabled bool     `toml:"disabled,omitempty"`
	Enabled  bool     `toml:"enabled,omitempty"`
}

// GroupDef bundles selectors. Toggling a group toggles every member.
type GroupDef struct {
	Include []string `toml:"include,omitempty"`
}

// ProfileConfig selects the active profile. Profiles themselves layer on top
// of the same shape; v0.2 ships a single profile as part of the file.
type ProfileConfig struct {
	Active string `toml:"active,omitempty"`
}

// DefaultPath returns the path the config should live at on this machine.
//
// Honors $XDG_CONFIG_HOME, then $HOME/.config/clawtool/config.toml. If neither
// resolves we return a relative path so callers fail predictably with a
// recognizable error rather than reading from "/".
func DefaultPath() string {
	return filepath.Join(xdg.ConfigDir(), "config.toml")
}

// Default returns a Config preloaded with every known core tool enabled.
// Used by `clawtool init` to write a sensible starting point.
func Default() Config {
	enabled := true
	tools := map[string]CoreTool{}
	for _, name := range KnownCoreTools {
		tools[name] = CoreTool{Enabled: &enabled}
	}
	return Config{
		CoreTools: tools,
		Profile:   ProfileConfig{Active: "default"},
		// Pre-1.0 default = on. Matches the wizard form's title
		// ("Anonymous telemetry (pre-1.0 default = on)") + the
		// post-onboard thank-you copy ("Telemetry stays on through
		// v1.0.0 while clawtool is in active development"). The
		// allow-list payload (command + version + duration +
		// exit_code + agent family + recipe / engine / bridge
		// names) carries no prompts, paths, secrets, or env
		// values; opt-out is one command (`clawtool telemetry
		// off`). When v1.0.0 ships we collapse this back to
		// false — tracked in the roadmap.
		Telemetry: TelemetryConfig{Enabled: true},
	}
}

// KnownCoreTools is the compile-time list of core tools clawtool ships.
// Adding a tool here makes it appear in `clawtool init` output and
// `clawtool tools list`.
var KnownCoreTools = []string{
	"Bash",
	"Edit",
	"Glob",
	"Grep",
	"Read",
	"ToolSearch",
	"WebFetch",
	"WebSearch",
	"Write",
}

// Load reads and parses a config file. Returns os.ErrNotExist (wrapped) when
// the file is absent so callers can distinguish "no config" from a parse error.
//
// The on-disk schema uses `omitempty` everywhere — a user who upgraded from
// pre-v0.22.19 has a config.toml that omits `[telemetry] enabled` entirely,
// which TOML unmarshal turns into the zero-value (false). That silently
// flipped existing users to telemetry-off even though Default() / the wizard
// claim "pre-1.0 default = on". To honour the contract on upgrade, fields
// that have a non-zero baseline in Default() must be merged in when the
// on-disk value is absent. We do this for `[telemetry]` here; other sections
// (CoreTools, Profile) stay untouched because their existing on-disk
// representation already encodes the intended state explicitly.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	mergeDefaults(&cfg, b)
	return cfg, nil
}

// mergeDefaults patches fields whose Default() baseline is non-zero but
// whose on-disk representation is missing the relevant TOML key. raw is
// the file bytes so we can string-match the actual presence of a key
// (toml.Unmarshal can't distinguish "absent" from "explicitly false").
//
// Currently scoped to [telemetry] enabled. When a future field needs the
// same upgrade-merge treatment, add another case here rather than
// duplicating the string-match.
func mergeDefaults(cfg *Config, raw []byte) {
	defaults := Default()
	if !hasTelemetryEnabledKey(raw) {
		cfg.Telemetry.Enabled = defaults.Telemetry.Enabled
	}
}

// hasTelemetryEnabledKey reports whether the raw TOML explicitly sets
// `enabled` under `[telemetry]`. Not a TOML parser — we already have the
// parsed struct; we just need to know "did the user write this key at all
// or is the false we got from unmarshal really zero-value drift". A
// regex-free string scan is enough because TOML's grammar makes the
// section header + key shape unambiguous.
func hasTelemetryEnabledKey(raw []byte) bool {
	s := string(raw)
	idx := strings.Index(s, "[telemetry]")
	if idx < 0 {
		return false
	}
	// Walk forward until the next section header or EOF, looking for a
	// line whose first non-whitespace token is `enabled`.
	rest := s[idx+len("[telemetry]"):]
	if next := strings.Index(rest, "\n["); next >= 0 {
		rest = rest[:next]
	}
	for _, line := range strings.Split(rest, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "enabled") {
			// Allow `enabled =` or `enabled=`, both are TOML.
			after := strings.TrimPrefix(t, "enabled")
			after = strings.TrimSpace(after)
			if strings.HasPrefix(after, "=") {
				return true
			}
		}
	}
	return false
}

// LoadOrDefault returns Load if the file exists, or Default() with no error
// when the file is missing. Used by `serve` so a fresh user can run without
// running `init` first.
func LoadOrDefault(path string) (Config, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	return Config{}, err
}

// Save writes the config to path, creating parent directories. File
// mode is 0600 because env values may carry secrets. Atomic via
// temp+rename so a crash / kill / ENOSPC mid-write can't truncate
// the durable config — Load hard-fails parse errors at config.go's
// reader, and a half-written config.toml would brick every subsequent
// `clawtool` invocation until the operator deletes it manually.
func (c Config) Save(path string) error {
	b, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return atomicfile.WriteFileMkdir(path, b, 0o600, 0o700)
}

// Resolution holds the result of resolving an enable/disable check.
type Resolution struct {
	Enabled bool
	Rule    string // "tools.<sel>", "core_tools.<name>", "default"
}

// IsEnabled answers "should clawtool expose this tool?" for the given
// selector (CLI-form, e.g. "Bash" or "github-personal.create_issue").
//
// Precedence per ADR-004 — v0.2 implements tool > server only:
//
//  1. tools."<selector>".enabled (per-tool explicit override)
//  2. core_tools.<Name>.enabled (only for core tools, where selector is the bare PascalCase name)
//  3. default = true
//
// Tag and group precedence land in v0.3.
func (c Config) IsEnabled(selector string) Resolution {
	if override, ok := c.Tools[selector]; ok && override.Enabled != nil {
		return Resolution{Enabled: *override.Enabled, Rule: "tools." + quoteIfNeeded(selector)}
	}
	if isCoreToolSelector(selector) {
		if t, ok := c.CoreTools[selector]; ok && t.Enabled != nil {
			return Resolution{Enabled: *t.Enabled, Rule: "core_tools." + selector}
		}
	}
	return Resolution{Enabled: true, Rule: "default"}
}

// SetToolEnabled writes (or creates) an explicit per-tool override for a
// selector. Used by `clawtool tools enable / disable`.
func (c *Config) SetToolEnabled(selector string, enabled bool) {
	if c.Tools == nil {
		c.Tools = map[string]ToolOverride{}
	}
	c.Tools[selector] = ToolOverride{Enabled: &enabled}
}

// ListCoreTools returns the alphabetic list of known core tools paired with
// their resolved enabled state.
func (c Config) ListCoreTools() []ToolListEntry {
	out := make([]ToolListEntry, 0, len(KnownCoreTools))
	for _, name := range KnownCoreTools {
		out = append(out, ToolListEntry{
			Selector:   name,
			Resolution: c.IsEnabled(name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Selector < out[j].Selector })
	return out
}

// ToolListEntry is one row of `clawtool tools list`.
type ToolListEntry struct {
	Selector   string
	Resolution Resolution
}

// isCoreToolSelector returns true if selector names a clawtool core tool.
// Per ADR-006, core tools are PascalCase and contain no `__`.
func isCoreToolSelector(selector string) bool {
	if selector == "" || strings.Contains(selector, "__") || strings.Contains(selector, ".") {
		return false
	}
	c := selector[0]
	return c >= 'A' && c <= 'Z'
}

// quoteIfNeeded returns the selector wrapped in quotes when it contains a
// dot, so the rule string read back by humans matches the on-disk TOML form
// (`tools."github-personal.create_issue"`).
func quoteIfNeeded(selector string) string {
	if strings.Contains(selector, ".") {
		return `"` + selector + `"`
	}
	return selector
}
