//go:build linux

// bubblewrap (bwrap) adapter — Linux primary engine.
//
// Wrap rewrites the supplied *exec.Cmd to invoke bwrap with the
// flags compiled from Profile, then exec the original binary
// inside the sandbox. We never run unsharing logic ourselves;
// per ADR-007 bwrap owns the namespace setup, FS bind-mounts,
// and capability scrubbing. clawtool's polish layer is the
// Profile→argv translator.
//
// Lifecycle:
//   - Wrap mutates cmd.Path + cmd.Args. The original binary path
//     becomes the trailing argument bwrap exec's.
//   - cmd.Env is REPLACED with the env-allowlisted subset (bwrap
//     itself --setenv preserves; we also re-build cmd.Env for
//     callers that consult Process.Env directly).
//   - sysproc.ApplyGroupWithCtxCancel is the caller's job
//     (supervisor.dispatch). On ctx cancel, the process group
//     SIGKILL reaps bwrap + the agent inside it.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func init() { register(bwrapEngine{}) }

type bwrapEngine struct{}

func (bwrapEngine) Name() string { return "bwrap" }

func (bwrapEngine) Available() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func (bwrapEngine) Wrap(_ context.Context, cmd *exec.Cmd, p *Profile) error {
	if cmd == nil {
		return errors.New("sandbox: nil exec.Cmd")
	}
	if p == nil {
		return errors.New("sandbox: nil Profile")
	}
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return fmt.Errorf("sandbox: bwrap not on PATH: %w", err)
	}
	if cmd.Path == "" || len(cmd.Args) == 0 {
		return errors.New("sandbox: cmd.Path / cmd.Args must be set before Wrap")
	}

	args, err := buildBwrapArgs(p)
	if err != nil {
		return err
	}
	args = append(args, "--", cmd.Path)
	args = append(args, cmd.Args[1:]...) // skip argv[0] — bwrap re-exec replaces it

	// Build the env subset honouring Allow + Deny patterns. bwrap
	// also gets --setenv flags so the inner process sees only
	// what we approved.
	cmd.Env = applyEnvPolicy(currentEnvSnapshot(cmd.Env), p.Env)
	cmd.Path = bwrapPath
	cmd.Args = append([]string{bwrapPath}, args...)
	return nil
}

// buildBwrapArgs translates a Profile into bubblewrap CLI flags.
// We default to a strict baseline (--die-with-parent, no /proc
// unless explicit, no /dev unless explicit) and add only what
// the profile asks for.
func buildBwrapArgs(p *Profile) ([]string, error) {
	args := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
		// /proc + /dev are needed for almost every program; the
		// safer defaults are bwrap's --proc + --dev which mount
		// minimal pseudo-fs without exposing host details.
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}

	// Network: --unshare-net unless the profile asks for "open".
	//
	// Audit fix #203: previously "allowlist" silently degraded to
	// --share-net (full host networking), defeating the policy.
	// Codex c1b00f10 verbatim: "Network allowlist degrades to full
	// host networking via --share-net." Now fail-CLOSED: operator
	// must either drop the allowlist into open/loopback/none, or
	// pair bwrap with a host-side firewall and pass open here. The
	// engine refuses to launch a profile whose network policy it
	// cannot honour. Same rule for resource limits below.
	switch strings.ToLower(p.Network.Mode) {
	case "", "none":
		args = append(args, "--unshare-net")
	case "loopback":
		// bubblewrap doesn't ship a built-in loopback-only mode.
		// We treat loopback like none — egress blocked, only the
		// in-namespace lo interface is visible. This is stricter
		// than the operator might expect (no actual lo iface
		// configured today), but it's the SAFER fail-closed
		// interpretation: the sandboxed process can't reach
		// anything off-host. Future helper will configure lo.
		args = append(args, "--unshare-net")
	case "allowlist":
		return nil, fmt.Errorf(
			"sandbox %q: network.policy=\"allowlist\" cannot be enforced by bwrap alone (bwrap has no egress filter); pair with a host-side firewall and switch to policy=\"open\", or drop allowlist for none|loopback. Refusing to dispatch unsandboxed",
			p.Name)
	case "open":
		args = append(args, "--share-net")
	default:
		return nil, fmt.Errorf("sandbox: unknown network mode %q", p.Network.Mode)
	}

	// Resource limits: bwrap doesn't apply them. If the operator
	// set any, refuse the profile rather than pretend they were
	// honoured. Codex c1b00f10: "resource limits are parsed and not
	// enforced." Operators who want enforcement run inside docker
	// (engine adapter handles cgroup limits there) or pair with
	// systemd-run --scope --p MemoryMax=... etc.
	if p.Limits.MemoryBytes > 0 || p.Limits.CPUShares > 0 || p.Limits.ProcessCount > 0 {
		return nil, fmt.Errorf(
			"sandbox %q: resource limits (memory / cpu_shares / process_count) cannot be enforced by bwrap; switch the profile's engine to docker, run via systemd-run --scope, or drop the limits. Refusing to dispatch with phantom limits",
			p.Name)
	}

	// Filesystem: emit --ro-bind / --bind / --tmpfs depending on
	// the path's mode. Resolve $HOME / ${HOME} / ${workspace}
	// substitutions against the host env.
	for _, rule := range p.Paths {
		path, err := expandPath(rule.Path)
		if err != nil {
			return nil, err
		}
		if path == "" {
			continue
		}
		switch rule.Mode {
		case ModeReadOnly:
			args = append(args, "--ro-bind-try", path, path)
		case ModeReadWrite:
			args = append(args, "--bind-try", path, path)
		case ModeNone:
			// no-op — operator wants the path explicitly
			// inaccessible. bwrap's default is "not visible"
			// when no bind exists.
		}
	}

	// Env allowlist: --setenv each survivor. The host's value is
	// passed through; bwrap doesn't synthesise values.
	hostEnv := envAsMap(os.Environ())
	for _, name := range p.Env.Allow {
		if isWildcard(name) {
			for k, v := range hostEnv {
				if matchesPattern(k, name) && !envDenied(k, p.Env.Deny) {
					args = append(args, "--setenv", k, v)
				}
			}
			continue
		}
		if v, ok := hostEnv[name]; ok && !envDenied(name, p.Env.Deny) {
			args = append(args, "--setenv", name, v)
		}
	}

	// chdir into the first rw path that's a dir, or /tmp as a
	// safe default. Without --chdir bwrap uses / which trips up
	// most CLI tooling.
	if cwd := pickStartingCwd(p.Paths); cwd != "" {
		args = append(args, "--chdir", cwd)
	}
	return args, nil
}

func expandPath(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	// ${VAR} expansion via os.Getenv. Doesn't expand $VAR (no
	// braces) — keeps the syntax explicit + matches the rest of
	// clawtool's config conventions.
	out := os.Expand(s, os.Getenv)
	if !filepath.IsAbs(out) {
		// Resolve relative paths against cwd at Wrap time.
		abs, err := filepath.Abs(out)
		if err != nil {
			return "", fmt.Errorf("sandbox: resolve %q: %w", s, err)
		}
		out = abs
	}
	return out, nil
}

func pickStartingCwd(rules []PathRule) string {
	for _, r := range rules {
		if r.Mode != ModeReadWrite {
			continue
		}
		exp, err := expandPath(r.Path)
		if err != nil || exp == "" {
			continue
		}
		if info, err := os.Stat(exp); err == nil && info.IsDir() {
			return exp
		}
	}
	return ""
}

// envAsMap converts an os.Environ-shaped slice to a map.
func envAsMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

// applyEnvPolicy returns the subset of env-vars matching the
// allow/deny patterns. base is the existing cmd.Env — when
// non-empty we honour what the caller already set; when empty we
// fall through to os.Environ.
func applyEnvPolicy(base []string, policy EnvPolicy) []string {
	src := base
	if len(src) == 0 {
		src = os.Environ()
	}
	srcMap := envAsMap(src)
	out := make([]string, 0, len(srcMap))
	for _, allow := range policy.Allow {
		if isWildcard(allow) {
			for k, v := range srcMap {
				if matchesPattern(k, allow) && !envDenied(k, policy.Deny) {
					out = append(out, k+"="+v)
				}
			}
			continue
		}
		if v, ok := srcMap[allow]; ok && !envDenied(allow, policy.Deny) {
			out = append(out, allow+"="+v)
		}
	}
	// If the operator set no allow list, bwrap launches with an
	// effectively empty env. That's safe but breaks PATH-aware
	// binaries; we surface this in the higher-layer error
	// handling rather than silently injecting PATH.
	return out
}

// currentEnvSnapshot picks between an explicit cmd.Env and
// os.Environ. Kept as a separate helper for clarity.
func currentEnvSnapshot(env []string) []string {
	if len(env) > 0 {
		return env
	}
	return os.Environ()
}

func isWildcard(s string) bool { return strings.ContainsAny(s, "*?") }

func matchesPattern(name, pattern string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}

func envDenied(name string, deny []string) bool {
	for _, d := range deny {
		if isWildcard(d) {
			if matchesPattern(name, d) {
				return true
			}
			continue
		}
		if name == d {
			return true
		}
	}
	return false
}
