// Generic MCP-host adapter — covers Codex / OpenCode / Gemini and any
// other CLI that exposes `<bin> mcp add <name>` / `<bin> mcp remove
// <name>` semantics. These hosts don't let us disable their internal
// Bash/Read/Edit tools the way Claude Code's settings.json deny list
// does, so "claim" here means "register clawtool as an MCP server in
// the host's config" — same operator intent: the model gets clawtool
// tools at all, not just the host's built-ins.
//
// **Fan-in semantics**: by default every host points at ONE shared
// persistent daemon (`internal/daemon`), so BIAM identity, task
// store, and notify channels are unified across hosts. Stdio-spawn
// mode is still available as a fallback (`mode: "stdio"`) but it
// produces N independent identities and breaks cross-host notify —
// don't use it unless the host doesn't accept `--url` style HTTP MCP.
//
// One marker per host at <configDir>/clawtool-mcp.lock. Release
// removes the MCP entry and the marker but leaves the daemon
// running — other hosts may still be bound to it.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/daemon"
)

// mcpHostMode picks the wiring strategy. SharedHTTP is the right
// default; Stdio exists for hosts whose `mcp add` doesn't accept a
// URL transport.
type mcpHostMode int

const (
	mcpHostModeSharedHTTP mcpHostMode = iota
	mcpHostModeStdio
)

func (m mcpHostMode) String() string {
	switch m {
	case mcpHostModeSharedHTTP:
		return "shared-http"
	case mcpHostModeStdio:
		return "stdio"
	default:
		return "?"
	}
}

// mcpHostBinary describes the per-host knobs the generic adapter
// needs. addArgsHTTP is the URL-transport variant; addArgsStdio is
// the spawn-child variant. rmArgs is shared.
type mcpHostBinary struct {
	name         string // adapter name = family name
	binary       string // CLI binary on PATH
	configDir    string // dir under $HOME for marker storage
	mode         mcpHostMode
	addArgsHTTP  func(serverName, url, tokenEnv, token string, requireAuth bool) []string
	addArgsStdio func(serverName, selfPath string) []string
	rmArgs       func(serverName string) []string
	tokenEnvName string // env var name set in the host's mcp entry (HTTP mode only)
}

// codexAddArgsHTTP / geminiAddArgsHTTP / opencodeAddArgsStdio differ
// per-CLI. Codex: `--url ... [--bearer-token-env-var ENV]`. Gemini:
// `<url> -t http [-H "Authorization: Bearer <tok>"] -s user`. Opencode
// has no documented `--url` transport so it stays on stdio.
//
// The bearer-token wiring is gated by the `requireAuth` flag the caller
// derives from Options.RequireAuth. Default install (single-user, the
// daemon listens only on 127.0.0.1) skips the env var entirely so
// Codex doesn't refuse to start when the operator hasn't pre-set
// CLAWTOOL_TOKEN. Daemon / relay deployments flip RequireAuth on.
func codexAddArgsHTTP(name, url, tokenEnv, _ string, requireAuth bool) []string {
	args := []string{"mcp", "add", name, "--url", url}
	if requireAuth {
		args = append(args, "--bearer-token-env-var", tokenEnv)
	}
	return args
}
func codexAddArgsStdio(name, self string) []string {
	return []string{"mcp", "add", name, "--", self, "serve"}
}
func codexRmArgs(name string) []string { return []string{"mcp", "remove", name} }

// geminiAddArgsHTTP bakes the Bearer header into Gemini's mcp config
// (Gemini has no env-var indirection, only a literal -H value). When
// requireAuth is false the header is omitted entirely — the daemon
// is then expected to be running in single-user no-auth mode, so
// Gemini's unauthenticated request is accepted.
func geminiAddArgsHTTP(name, url, _, token string, requireAuth bool) []string {
	args := []string{"mcp", "add", name, url, "-t", "http"}
	if requireAuth {
		args = append(args, "-H", "Authorization: Bearer "+token)
	}
	args = append(args, "-s", "user")
	return args
}
func geminiAddArgsStdio(name, self string) []string {
	return []string{"mcp", "add", name, self, "serve", "-s", "user"}
}
func geminiRmArgs(name string) []string { return []string{"mcp", "remove", name} }

func opencodeAddArgsStdio(name, self string) []string {
	return []string{"mcp", "add", name, "--", self, "serve"}
}
func opencodeRmArgs(name string) []string { return []string{"mcp", "remove", name} }

// MCPServerName is the canonical name we register clawtool under in
// every host. Kept identical so the operator sees the same identifier
// across `codex mcp list`, `gemini mcp list`, etc.
const MCPServerName = "clawtool"

// MCPTokenEnvVar is the env var the host process reads to obtain the
// bearer token when speaking to the shared daemon. Codex sets this
// at server-launch time (per --bearer-token-env-var); Gemini bakes
// the literal token into config so this is unused there.
const MCPTokenEnvVar = "CLAWTOOL_TOKEN"

// Test-overridable hooks. Production uses os.Executable / exec.LookPath
// / exec.Command directly; tests inject deterministic stubs.
var (
	mcpHostExecutable = func() (string, error) { return os.Executable() }
	mcpHostHomeDir    = os.UserHomeDir
	mcpHostExecPath   = exec.LookPath
	mcpHostRun        = func(bin string, args []string) ([]byte, error) {
		out, err := exec.Command(bin, args...).CombinedOutput()
		return out, err
	}
	// daemonEnsure / daemonToken are pluggable so tests don't fork
	// a real persistent process. Production points at the
	// internal/daemon package.
	daemonEnsure = func(ctx context.Context) (*daemon.State, error) { return daemon.Ensure(ctx) }
	daemonToken  = daemon.ReadToken
)

type mcpHostAdapter struct {
	cfg mcpHostBinary
}

func (a *mcpHostAdapter) Name() string { return a.cfg.name }

func (a *mcpHostAdapter) Detected() bool {
	if _, err := mcpHostExecPath(a.cfg.binary); err == nil {
		return true
	}
	if home, err := mcpHostHomeDir(); err == nil && home != "" {
		if _, err := os.Stat(filepath.Join(home, a.cfg.configDir)); err == nil {
			return true
		}
	}
	return false
}

func (a *mcpHostAdapter) markerPath() string {
	home, err := mcpHostHomeDir()
	if err != nil || home == "" {
		return filepath.Join(a.cfg.configDir, "clawtool-mcp.lock")
	}
	return filepath.Join(home, a.cfg.configDir, "clawtool-mcp.lock")
}

// Claim registers clawtool with the host. SharedHTTP path: ensure the
// daemon is up + register the host with --url + bearer token. Stdio
// path: register the host to spawn a child each time. Idempotent in
// both modes.
func (a *mcpHostAdapter) Claim(opts Options) (Plan, error) {
	plan := Plan{
		Adapter:      a.Name(),
		Action:       "claim",
		SettingsPath: filepath.Join(a.markerPath(), "..", "config.toml"),
		MarkerPath:   a.markerPath(),
		DryRun:       opts.DryRun,
	}

	bin, err := mcpHostExecPath(a.cfg.binary)
	if err != nil {
		return plan, fmt.Errorf("%s: binary %q not on PATH", a.cfg.name, a.cfg.binary)
	}

	if existing, err := a.readMarker(); err == nil && existing.Server == MCPServerName && existing.Mode == a.cfg.mode.String() {
		plan.WasNoop = true
		plan.ToolsAdded = []string{"mcp:" + MCPServerName + " (" + existing.Mode + ")"}
		return plan, nil
	}

	plan.ToolsAdded = []string{"mcp:" + MCPServerName + " (" + a.cfg.mode.String() + ")"}
	if opts.DryRun {
		return plan, nil
	}

	var (
		args []string
		url  string
	)

	switch a.cfg.mode {
	case mcpHostModeSharedHTTP:
		st, err := daemonEnsure(context.Background())
		if err != nil {
			return plan, fmt.Errorf("%s: ensure shared daemon: %w", a.cfg.name, err)
		}
		url = st.URL()
		tok, err := daemonToken()
		if err != nil {
			return plan, fmt.Errorf("%s: read daemon token: %w", a.cfg.name, err)
		}
		if a.cfg.addArgsHTTP == nil {
			return plan, fmt.Errorf("%s: shared-http mode unsupported by this host (no addArgsHTTP)", a.cfg.name)
		}
		args = a.cfg.addArgsHTTP(MCPServerName, url, MCPTokenEnvVar, tok, opts.RequireAuth)
	case mcpHostModeStdio:
		self, err := mcpHostExecutable()
		if err != nil {
			return plan, fmt.Errorf("resolve self: %w", err)
		}
		if a.cfg.addArgsStdio == nil {
			return plan, fmt.Errorf("%s: stdio mode unsupported by this host (no addArgsStdio)", a.cfg.name)
		}
		args = a.cfg.addArgsStdio(MCPServerName, self)
	}

	out, err := mcpHostRun(bin, args)
	if err != nil {
		return plan, fmt.Errorf("%s mcp add: %v: %s", a.cfg.name, err, strings.TrimSpace(string(out)))
	}

	if err := a.writeMarker(MCPServerName, a.cfg.mode.String(), url); err != nil {
		return plan, fmt.Errorf("%s: write marker (host registered, marker write failed): %w", a.cfg.name, err)
	}
	return plan, nil
}

// Release runs the host's `mcp remove` and drops the marker. Daemon
// is left alone — other hosts may still be bound. Idempotent: no
// marker → noop.
func (a *mcpHostAdapter) Release(opts Options) (Plan, error) {
	plan := Plan{
		Adapter:    a.Name(),
		Action:     "release",
		MarkerPath: a.markerPath(),
		DryRun:     opts.DryRun,
	}
	marker, err := a.readMarker()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			plan.WasNoop = true
			return plan, nil
		}
		return plan, err
	}
	plan.ToolsRemoved = []string{"mcp:" + marker.Server}
	if opts.DryRun {
		return plan, nil
	}
	bin, err := mcpHostExecPath(a.cfg.binary)
	if err != nil {
		return plan, fmt.Errorf("%s: binary %q not on PATH", a.cfg.name, a.cfg.binary)
	}
	if out, err := mcpHostRun(bin, a.cfg.rmArgs(marker.Server)); err != nil {
		body := strings.ToLower(string(out))
		if !strings.Contains(body, "not found") && !strings.Contains(body, "no such") {
			return plan, fmt.Errorf("%s mcp remove: %v: %s", a.cfg.name, err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.Remove(a.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("%s: remove marker: %w", a.cfg.name, err)
	}
	return plan, nil
}

func (a *mcpHostAdapter) Status() (Status, error) {
	s := Status{
		Adapter:      a.Name(),
		Detected:     a.Detected(),
		SettingsPath: filepath.Join(a.markerPath(), "..", "config.toml"),
	}
	if !s.Detected {
		s.Notes = a.cfg.binary + " binary not on PATH and " + a.cfg.configDir + "/ not present"
		return s, nil
	}
	marker, err := a.readMarker()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.Notes = "clawtool not registered as MCP server (run `clawtool agents claim " + a.Name() + "`)"
			return s, nil
		}
		return s, err
	}
	if marker.Server != "" {
		s.Claimed = true
		label := "mcp:" + marker.Server
		if marker.Mode != "" {
			label += " (" + marker.Mode + ")"
		}
		s.DisabledByUs = []string{label}
	}
	return s, nil
}

// ── marker shape ─────────────────────────────────────────────────────

type mcpHostMarker struct {
	Version int    `json:"version"`
	Server  string `json:"server"`
	Mode    string `json:"mode,omitempty"`
	URL     string `json:"url,omitempty"`
}

func (a *mcpHostAdapter) readMarker() (mcpHostMarker, error) {
	var m mcpHostMarker
	b, err := os.ReadFile(a.markerPath())
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse marker %s: %w", a.markerPath(), err)
	}
	return m, nil
}

func (a *mcpHostAdapter) writeMarker(server, mode, url string) error {
	if err := os.MkdirAll(filepath.Dir(a.markerPath()), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(mcpHostMarker{
		Version: 2,
		Server:  server,
		Mode:    mode,
		URL:     url,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteJSON(a.markerPath(), append(body, '\n'))
}

// ── concrete registrations ───────────────────────────────────────────

func init() {
	Register(&mcpHostAdapter{cfg: mcpHostBinary{
		name:         "codex",
		binary:       "codex",
		configDir:    ".codex",
		mode:         mcpHostModeSharedHTTP,
		addArgsHTTP:  codexAddArgsHTTP,
		addArgsStdio: codexAddArgsStdio,
		rmArgs:       codexRmArgs,
		tokenEnvName: MCPTokenEnvVar,
	}})
	Register(&mcpHostAdapter{cfg: mcpHostBinary{
		name:         "gemini",
		binary:       "gemini",
		configDir:    ".gemini",
		mode:         mcpHostModeSharedHTTP,
		addArgsHTTP:  geminiAddArgsHTTP,
		addArgsStdio: geminiAddArgsStdio,
		rmArgs:       geminiRmArgs,
		tokenEnvName: MCPTokenEnvVar,
	}})
	Register(&mcpHostAdapter{cfg: mcpHostBinary{
		name:         "opencode",
		binary:       "opencode",
		configDir:    ".local/share/opencode",
		mode:         mcpHostModeStdio, // opencode has no documented --url transport
		addArgsStdio: opencodeAddArgsStdio,
		rmArgs:       opencodeRmArgs,
	}})
}
