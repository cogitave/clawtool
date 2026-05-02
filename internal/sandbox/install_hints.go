// install_hints.go — operator-friendly install instructions for
// the sandbox engines `clawtool sandbox doctor` reports as
// MISSING. Per ADR-020 §Resolved (2026-05-02): the doctor flow
// surfaces hints; it never drives an install. Auto-install would
// require sudo and silently widen the trust surface — operators
// must run the install command themselves so the credential
// prompt + package source stays under their control.
//
// Shape: InstallHint(goos, engine) → multi-line string. Empty
// string means "no hint applies" (engine name unknown for that
// OS, or the engine is intrinsic and never missing — e.g. noop).
//
// Wired into `clawtool sandbox doctor` (see internal/cli/sandbox.go)
// so each engine reported Available=false gets the matching hint
// appended to the human output. JSON output is unchanged — the
// hint is operator-facing prose, not a wire field.
package sandbox

import "strings"

// InstallHint returns operator-friendly multi-line install
// instructions for (goos, engine), or an empty string when no
// hint applies. Caller checks `if hint := InstallHint(...);
// hint != "" { ... }` and renders the hint verbatim.
//
// Recognised engines: "bwrap", "sandbox-exec", "docker", "noop".
// Recognised goos: "linux", "darwin", "windows". Anything else
// returns "".
//
// NOTE: hints describe operator-driven installs. We never run
// sudo, never invoke a package manager, never download a binary
// on the operator's behalf. The function returns prose; the
// human types the command.
func InstallHint(goos, engine string) string {
	goos = strings.ToLower(strings.TrimSpace(goos))
	engine = strings.ToLower(strings.TrimSpace(engine))

	switch engine {
	case "bwrap":
		return bwrapHint(goos)
	case "sandbox-exec":
		return sandboxExecHint(goos)
	case "docker":
		return dockerHint(goos)
	case "noop":
		// noop is the intrinsic fallback — always available, never
		// missing, no install path.
		return ""
	}
	return ""
}

func bwrapHint(goos string) string {
	switch goos {
	case "linux":
		return strings.Join([]string{
			"bubblewrap (bwrap) is the Linux primary sandbox engine — install one of:",
			"  Debian / Ubuntu: sudo apt-get install bubblewrap",
			"  Fedora / RHEL:   sudo dnf install bubblewrap",
			"  Arch / Manjaro:  sudo pacman -S bubblewrap",
			"  Alpine:          sudo apk add bubblewrap",
			"Then re-run `clawtool sandbox doctor`. See docs/sandbox.md for the full profile reference.",
		}, "\n")
	case "darwin":
		// bwrap is Linux-only (uses user namespaces). Surface the
		// macOS-native engine instead of pretending bwrap is an
		// option there.
		return strings.Join([]string{
			"bubblewrap (bwrap) is Linux-only — it relies on Linux user namespaces.",
			"On macOS the primary engine is sandbox-exec (built-in); see `clawtool sandbox doctor` output for the right hint.",
		}, "\n")
	case "windows":
		return strings.Join([]string{
			"bubblewrap (bwrap) is Linux-only — it relies on Linux user namespaces.",
			"On Windows the only supported engine is Docker Desktop. Install Docker Desktop, then re-run `clawtool sandbox doctor`.",
			"WSL2 users: install bubblewrap inside the WSL2 distro (`sudo apt-get install bubblewrap`) and run clawtool from the WSL2 shell.",
		}, "\n")
	}
	return ""
}

func sandboxExecHint(goos string) string {
	switch goos {
	case "darwin":
		// sandbox-exec ships with macOS — there is nothing to
		// install. Explain WHY it can still report unavailable
		// (the .sb compiler is pending, see sandbox_exec_darwin.go)
		// so operators don't waste time hunting for an install.
		return strings.Join([]string{
			"sandbox-exec is built into macOS — no install needed (`/usr/bin/sandbox-exec`).",
			"If `sandbox doctor` reports it unavailable, the binary has been removed or stripped from PATH; restore it from a stock macOS install or use Docker Desktop as the fallback.",
			"Note: clawtool's .sb profile compiler is still landing (v0.18.2). Until it ships, sandbox-exec is detected but Wrap returns a clear \"compiler pending\" error — Docker Desktop is the working alternative on macOS today.",
		}, "\n")
	case "linux", "windows":
		return strings.Join([]string{
			"sandbox-exec is macOS-only (Apple Seatbelt). It does not exist on Linux or Windows.",
			"Use bubblewrap on Linux or Docker Desktop on Windows; see `clawtool sandbox doctor` for the per-engine hint.",
		}, "\n")
	}
	return ""
}

func dockerHint(goos string) string {
	switch goos {
	case "linux":
		return strings.Join([]string{
			"Docker is clawtool's cross-platform sandbox fallback. Install one of:",
			"  Convenience script: curl -fsSL https://get.docker.com | sh   (then `sudo usermod -aG docker $USER` and re-login)",
			"  Debian / Ubuntu:    sudo apt-get install docker.io",
			"  Fedora / RHEL:      sudo dnf install docker",
			"  Arch / Manjaro:     sudo pacman -S docker",
			"After install, start the daemon: `sudo systemctl enable --now docker`. Then re-run `clawtool sandbox doctor`.",
		}, "\n")
	case "darwin":
		return strings.Join([]string{
			"Docker on macOS — install one of:",
			"  Docker Desktop: https://www.docker.com/products/docker-desktop/   (GUI app, recommended)",
			"  Homebrew cask:  brew install --cask docker",
			"  Colima (CLI):   brew install colima docker && colima start       (lighter, no Desktop GUI)",
			"After install, ensure the daemon is running, then re-run `clawtool sandbox doctor`.",
		}, "\n")
	case "windows":
		return strings.Join([]string{
			"Docker Desktop is the only supported sandbox engine on Windows.",
			"  Download:        https://www.docker.com/products/docker-desktop/",
			"  Or via winget:   winget install Docker.DockerDesktop",
			"After install, start Docker Desktop and ensure WSL2 integration is enabled, then re-run `clawtool sandbox doctor`.",
		}, "\n")
	}
	return ""
}
