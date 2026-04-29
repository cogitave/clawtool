// Package telemetry — host fingerprint collector.
//
// Microsoft-level diagnostics within strict legal/privacy limits: every
// dimension is either an enumerable bucket (CPU-count band, memory
// tier, locale-language head), a public process attribute (Go runtime
// version, GOOS, GOARCH), or a presence-bool (does CLI X exist on
// PATH). NOTHING per-user-identifiable. NO paths, NO env values, NO
// hostnames. Operator can `clawtool telemetry preview` to see the
// exact wire shape before opting in.
//
// Wire shape: one event per daemon boot, `clawtool.host_fingerprint`,
// carrying every dimension this file collects. Keeps PostHog events-
// per-session bounded (server.start + host_fingerprint + per-call
// dispatch + log events) instead of per-property explosion.
package telemetry

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// FingerprintProps returns the property map for a single
// clawtool.host_fingerprint event. Every value is either:
//   - an integer count (cpu_count) or coarse bucket string
//   - a fixed-cardinality enum (locale_lang, term_kind, install_method)
//   - a presence boolean (claude_code_present, etc.)
//   - a public runtime attribute (go_version)
//
// Caller passes the boot-time install method (already known to
// server.go via $CLAWTOOL_INSTALL_METHOD) so we don't re-resolve it.
func FingerprintProps(installMethod string) map[string]any {
	props := map[string]any{
		// Hardware band
		"cpu_count":  runtime.NumCPU(),
		"mem_tier":   memTier(),
		"go_version": runtime.Version(),

		// Environment fingerprint (container / CI / WSL / TTY)
		"container":   detectContainer(),
		"is_ci":       detectCI(),
		"is_wsl":      detectWSL(),
		"term_kind":   detectTermKind(),
		"locale_lang": detectLocaleLang(),

		// Agent CLI presence (boot-time PATH probe). Lights up the
		// "what's the operator's setup look like" view in PostHog
		// without us needing to ask.
		"claude_code_present": cliOnPath("claude"),
		"codex_present":       cliOnPath("codex"),
		"gemini_present":      cliOnPath("gemini"),
		"opencode_present":    cliOnPath("opencode"),
	}
	if installMethod != "" {
		props["install_method"] = installMethod
	}
	// Network reachability — best effort, capped at 1s each. A
	// false here doesn't fail boot; it just tells us the host
	// can't reach the upstream we'd use for upgrades / telemetry.
	props["posthog_reachable"] = reachable("eu.i.posthog.com:443", time.Second)
	props["github_reachable"] = reachable("api.github.com:443", time.Second)
	return props
}

// memTier buckets total system memory into coarse bands. Reading
// /proc/meminfo on Linux; on darwin / windows we skip via stub
// fields and report "unknown" — better to drop the dimension than
// inject mock data.
func memTier() string {
	mem := readMemTotalKB()
	if mem == 0 {
		return "unknown"
	}
	gb := mem / 1024 / 1024
	switch {
	case gb < 2:
		return "<2GB"
	case gb < 8:
		return "2-8GB"
	case gb < 32:
		return "8-32GB"
	default:
		return ">32GB"
	}
}

func readMemTotalKB() int64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	body, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		// Format: "MemTotal:       16384000 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		var n int64
		for _, c := range fields[1] {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

// detectContainer returns true when we're running in a container
// (docker / OCI / podman / k8s pod). Multi-signal: /.dockerenv
// file (Docker), /run/.containerenv (Podman), $KUBERNETES_SERVICE_HOST
// (k8s pod), /proc/1/cgroup mentions docker/containerd. False
// otherwise. Doesn't touch the operator's namespace details.
func detectContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if body, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(body)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods") {
			return true
		}
	}
	return false
}

// detectCI returns true when standard CI env vars are set. Covers
// the common runners (GitHub, GitLab, CircleCI, Travis, Jenkins,
// Buildkite, etc.). Used to distinguish "operator on a laptop" from
// "automated build" for funnel analysis.
func detectCI() bool {
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "TRAVIS", "JENKINS_HOME", "BUILDKITE", "DRONE", "TEAMCITY_VERSION"} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

// detectWSL returns true when running under Windows Subsystem for
// Linux. Read /proc/version: "Microsoft" or "WSL" in the body
// signal WSL1 / WSL2 respectively.
func detectWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	body, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	s := string(body)
	return strings.Contains(s, "Microsoft") || strings.Contains(s, "WSL")
}

// detectTermKind buckets the terminal kind into a small allow-list:
//   - "tty"      : interactive shell (stdin is a tty)
//   - "ssh"      : SSH session (SSH_TTY / SSH_CONNECTION set)
//   - "ci"       : CI env (no tty, CI env vars set)
//   - "headless" : no tty, not CI (cron / systemd / docker logs)
func detectTermKind() string {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return "ssh"
	}
	if isStdinTTY() {
		return "tty"
	}
	if detectCI() {
		return "ci"
	}
	return "headless"
}

// isStdinTTY reports whether stdin looks like a terminal. Pure
// stdlib check — no x/term dependency to keep the telemetry
// package's import surface small.
func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// detectLocaleLang returns the first segment of $LANG (typically
// "tr_TR.UTF-8" → "tr"). Empty / unset → "unknown". Allow-list of
// known languages enforced by the caller via allowedKeys; we only
// emit the head, never the country / encoding portion.
func detectLocaleLang() string {
	v := os.Getenv("LANG")
	if v == "" {
		v = os.Getenv("LC_ALL")
	}
	if v == "" {
		return "unknown"
	}
	v = strings.ToLower(v)
	if i := strings.IndexAny(v, "_."); i > 0 {
		v = v[:i]
	}
	// Only allow ASCII letters; reject anything else as
	// potentially locale-injected text.
	for _, c := range v {
		if (c < 'a' || c > 'z') && c != '-' {
			return "unknown"
		}
	}
	if len(v) > 5 {
		return "unknown"
	}
	return v
}

// cliOnPath returns true when `name` is found on the operator's
// $PATH. Used for the agent-CLI presence map.
func cliOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// reachable does a TCP dial against host:port with the given
// timeout. False on connect refusal / timeout / DNS failure. We
// use net.Dialer rather than http.Client because we don't want
// the cost of a full TLS handshake on every probe — TCP-reach is
// enough to know "the network can talk to this endpoint."
func reachable(addr string, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// httpReachable is a slightly heavier reachability check — full
// HTTP HEAD round-trip. Reserved for cases where TCP-reach isn't
// enough (e.g. confirming a proxy is healthy). Not used in the
// fingerprint hot path; kept in the package so future expansions
// can reach for it without re-implementing.
//
//nolint:unused // public surface for future emitters
func httpReachable(url string, timeout time.Duration) bool {
	c := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}
