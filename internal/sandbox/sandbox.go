// Package sandbox implements ADR-020. Engine adapters wrap an
// exec.Cmd with host-native isolation primitives — bwrap on
// Linux, sandbox-exec on macOS, Docker as a portable fallback,
// noop where nothing is available.
//
// Per ADR-007 each engine shells out to its primitive's binary;
// we never re-implement seccomp / AppContainer / namespaces.
//
// v0.18 (this iteration) ships the surface + Engine interface
// + Profile parser + a working noop engine. Real bwrap /
// sandbox-exec / docker adapters land in v0.18.1+ — the same
// incremental pattern v0.16.4 used for `mcp` before v0.17.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

// Engine wraps an exec.Cmd with sandbox constraints.
type Engine interface {
	// Name is the engine's identifier — e.g. "bwrap",
	// "sandbox-exec", "docker", "noop". Surfaced in
	// `clawtool sandbox doctor` output.
	Name() string

	// Available reports whether the engine's underlying primitive
	// is usable on this host (binary on PATH, kernel feature
	// present, etc.).
	Available() bool

	// Wrap mutates cmd so it runs inside the engine's sandbox
	// using the supplied profile. Caller still calls cmd.Start /
	// cmd.Wait — Wrap doesn't run anything itself.
	Wrap(ctx context.Context, cmd *exec.Cmd, profile *Profile) error
}

// Profile is the typed view of one [sandboxes.<name>] block.
// Engines convert this into their primitive's flags.
type Profile struct {
	Name        string
	Description string
	Paths       []PathRule
	Network     NetworkPolicy
	Limits      Limits
	Env         EnvPolicy
}

// PathRule is one filesystem entry. Path is resolved against the
// caller's CWD when relative; engines bind it into the sandboxed
// view at the same logical location.
type PathRule struct {
	Path string
	Mode PathMode
}

// PathMode controls the bind-mount visibility.
type PathMode string

const (
	ModeReadOnly  PathMode = "ro"
	ModeReadWrite PathMode = "rw"
	ModeNone      PathMode = "none"
)

// NetworkPolicy describes egress restrictions.
type NetworkPolicy struct {
	// Mode is one of: "none" | "loopback" | "allowlist" | "open".
	Mode string
	// Allow is honoured only when Mode == "allowlist". Each
	// entry is "host:port" — engines translate to nft rules /
	// pf anchors / docker --add-host depending on the primitive.
	Allow []string
}

// Limits packages the resource caps.
type Limits struct {
	Timeout      time.Duration // 0 = no per-call timeout
	MemoryBytes  int64         // 0 = unconstrained
	CPUShares    int           // 0 = unconstrained
	ProcessCount int           // 0 = unconstrained (cgroup pids.max)
}

// EnvPolicy filters host env vars. Both Allow and Deny accept
// glob patterns matched via filepath.Match. Allow is checked
// first; Deny then trims matching entries from the result.
type EnvPolicy struct {
	Allow []string
	Deny  []string
}

// ParseProfile turns a config.SandboxConfig into a typed Profile.
// Returns a clear error per malformed field so the wizard / CLI
// can surface exactly what the operator typed wrong.
func ParseProfile(name string, cfg config.SandboxConfig) (*Profile, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("sandbox: name is required")
	}
	p := &Profile{
		Name:        name,
		Description: cfg.Description,
	}
	for i, rule := range cfg.Paths {
		mode, err := parseMode(rule.Mode)
		if err != nil {
			return nil, fmt.Errorf("sandbox %q: paths[%d]: %w", name, i, err)
		}
		path := strings.TrimSpace(rule.Path)
		if path == "" {
			return nil, fmt.Errorf("sandbox %q: paths[%d]: path is required", name, i)
		}
		p.Paths = append(p.Paths, PathRule{Path: path, Mode: mode})
	}
	netMode, err := parseNetworkPolicy(cfg.Network.Policy)
	if err != nil {
		return nil, fmt.Errorf("sandbox %q: network.policy: %w", name, err)
	}
	p.Network = NetworkPolicy{Mode: netMode, Allow: append([]string(nil), cfg.Network.Allow...)}
	if netMode != "allowlist" && len(cfg.Network.Allow) > 0 {
		return nil, fmt.Errorf("sandbox %q: network.allow is only meaningful when policy=\"allowlist\"", name)
	}

	if cfg.Limits.Timeout != "" {
		d, err := time.ParseDuration(cfg.Limits.Timeout)
		if err != nil {
			return nil, fmt.Errorf("sandbox %q: limits.timeout: %w", name, err)
		}
		p.Limits.Timeout = d
	}
	if cfg.Limits.Memory != "" {
		bytes, err := parseBytes(cfg.Limits.Memory)
		if err != nil {
			return nil, fmt.Errorf("sandbox %q: limits.memory: %w", name, err)
		}
		p.Limits.MemoryBytes = bytes
	}
	p.Limits.CPUShares = cfg.Limits.CPUShares
	p.Limits.ProcessCount = cfg.Limits.ProcessCount
	p.Env = EnvPolicy{
		Allow: append([]string(nil), cfg.Env.Allow...),
		Deny:  append([]string(nil), cfg.Env.Deny...),
	}
	return p, nil
}

func parseMode(s string) (PathMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "ro":
		return ModeReadOnly, nil
	case "rw":
		return ModeReadWrite, nil
	case "none":
		return ModeNone, nil
	}
	return "", fmt.Errorf("mode must be ro | rw | none (got %q)", s)
}

func parseNetworkPolicy(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return "none", nil
	case "loopback":
		return "loopback", nil
	case "allowlist":
		return "allowlist", nil
	case "open":
		return "open", nil
	}
	return "", fmt.Errorf("network policy must be none | loopback | allowlist | open (got %q)", s)
}

// parseBytes accepts "1GB", "512M", "1024" (raw bytes), case
// insensitive. Lean parser — no exotic suffixes.
func parseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "GB"), strings.HasSuffix(s, "G"):
		mult = 1 << 30
		s = strings.TrimSuffix(strings.TrimSuffix(s, "GB"), "G")
	case strings.HasSuffix(s, "MB"), strings.HasSuffix(s, "M"):
		mult = 1 << 20
		s = strings.TrimSuffix(strings.TrimSuffix(s, "MB"), "M")
	case strings.HasSuffix(s, "KB"), strings.HasSuffix(s, "K"):
		mult = 1 << 10
		s = strings.TrimSuffix(strings.TrimSuffix(s, "KB"), "K")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	var n int64
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int64(r-'0')
	}
	return n * mult, nil
}

// SelectEngine picks the primary engine available on this host,
// or the noop engine when nothing is. Engines are registered by
// per-OS init() calls into engineRegistry.
func SelectEngine() Engine {
	for _, e := range engineRegistry {
		if e.Available() {
			return e
		}
	}
	return noopEngine{}
}

// engineRegistry is the ordered list of candidates. Per-OS
// adapter files in this package append themselves at init() time.
var engineRegistry []Engine

// register pushes an engine onto the candidate list. Order
// matters — earlier wins SelectEngine when both report Available.
func register(e Engine) { engineRegistry = append(engineRegistry, e) }

// noopEngine is the fallback when nothing better is available.
// Wrap is a passthrough; the dispatcher logs a warning so the
// operator knows their profile was honoured semantically (config
// parsed, profile resolved) but enforcement is absent.
type noopEngine struct{}

func (noopEngine) Name() string    { return "noop" }
func (noopEngine) Available() bool { return true }
func (noopEngine) Wrap(_ context.Context, _ *exec.Cmd, _ *Profile) error {
	return errors.New("sandbox: no host-native engine available; --sandbox is a no-op (install bubblewrap on Linux, sandbox-exec is built-in on macOS, or use Docker)")
}

// AvailableEngines returns every registered engine's Available
// status. Used by `clawtool sandbox doctor`.
type EngineStatus struct {
	Name      string
	Available bool
}

func AvailableEngines() []EngineStatus {
	out := make([]EngineStatus, 0, len(engineRegistry)+1)
	for _, e := range engineRegistry {
		out = append(out, EngineStatus{Name: e.Name(), Available: e.Available()})
	}
	out = append(out, EngineStatus{Name: "noop", Available: true})
	return out
}
