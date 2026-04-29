package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/creativeprojects/go-selfupdate"
)

const upgradeUsage = `Usage:
  clawtool upgrade               Pull the latest cogitave/clawtool release
                                 and atomically replace the running binary.
  clawtool upgrade --check       Report the latest version without installing.

The release source is github.com/cogitave/clawtool — same artefacts
GoReleaser publishes on tag. Per-OS / per-arch tarballs auto-resolved.
`

func (a *App) runUpgrade(argv []string) int {
	checkOnly := false
	for _, v := range argv {
		switch v {
		case "--check":
			checkOnly = true
		case "--help", "-h":
			fmt.Fprint(a.Stderr, upgradeUsage)
			return 0
		default:
			fmt.Fprintf(a.Stderr, "clawtool upgrade: unknown flag %q\n\n%s", v, upgradeUsage)
			return 2
		}
	}

	// Use the unified version resolver — same source overview /
	// claude-bootstrap / telemetry consume, so users never see
	// mismatched numbers across `clawtool upgrade` vs `clawtool
	// overview`.
	currentVersion := version.Resolved()
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: build source: %v\n", err)
		return 1
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: build updater: %v\n", err)
		return 1
	}

	repo := selfupdate.ParseSlug("cogitave/clawtool")
	latest, found, err := updater.DetectLatest(context.Background(), repo)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: detect latest: %v\n", err)
		return 1
	}
	if !found || latest == nil {
		fmt.Fprintln(a.Stderr, "clawtool upgrade: no release found on cogitave/clawtool yet — fall back to install.sh")
		return 1
	}

	// LessOrEqual parses the supplied string as semver and panics on
	// non-semver input — `(devel)` / `(unknown)` from a `go build`
	// without -ldflags='-X version.Version' would crash the upgrade
	// path. Treat anything that isn't a real version as "always
	// outdated" so devs on a hand-built binary still get to upgrade
	// to the latest tagged release.
	if isComparableVersion(currentVersion) && latest.LessOrEqual(currentVersion) {
		fmt.Fprintf(a.Stdout, "clawtool is up to date (%s)\n", currentVersion)
		return 0
	}
	fmt.Fprintf(a.Stdout, "current: %s\nlatest:  %s\n", currentVersion, latest.Version())
	if checkOnly {
		return 0
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: locate self: %v\n", err)
		return 1
	}
	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		// Common case: clawtool sits in /usr/local/bin without write
		// access. Surface a clear hint instead of the raw permission
		// error so the user knows to re-run with sudo (or their own
		// elevation tool).
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintf(a.Stderr,
				"clawtool upgrade: permission denied writing %s — re-run as the binary's owner (sudo) or move the install to ~/.local/bin\n",
				exe)
			return 1
		}
		fmt.Fprintf(a.Stderr, "clawtool upgrade: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ upgraded to %s\n", latest.Version())

	// Auto-restart the daemon if one is running. Without this step
	// `clawtool upgrade` swaps the binary on disk but the running
	// daemon stays on the old code in memory — the operator has to
	// pkill+relaunch by hand, and a forgotten restart silently
	// invalidates every "fixed in the new release" claim. Stop()
	// SIGTERMs the old PID; Ensure() spawns a fresh one with the
	// new binary on the same port + token.
	if rc := restartDaemonIfRunning(a); rc != 0 {
		return rc
	}
	return 0
}

// restartDaemonIfRunning is the post-upgrade step that swaps the
// running daemon onto the new binary. Idempotent: no-ops when no
// daemon is recorded. On Stop or Ensure failure it writes a clear
// hint instead of a stack trace and returns non-zero so the
// installer surface (install.sh / CI) can detect the partial state.
func restartDaemonIfRunning(a *App) int {
	state, err := daemon.ReadState()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: read daemon state: %v (binary upgraded; run `clawtool serve` manually)\n", err)
		return 1
	}
	if state == nil || !daemon.IsRunning(state) {
		// Nothing to do — common case for fresh installs or when
		// the operator runs upgrade before ever launching a daemon.
		return 0
	}
	fmt.Fprintf(a.Stdout, "  → stopping running daemon (pid %d)…\n", state.PID)
	if err := daemon.Stop(); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: stop daemon: %v (binary upgraded; run `clawtool serve` manually)\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, "  → launching new daemon…")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fresh, err := daemon.Ensure(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: relaunch daemon: %v (run `clawtool serve` manually)\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "✓ daemon restarted on new binary: pid %d, %s\n", fresh.PID, fresh.URL())
	return 0
}

// isComparableVersion reports whether v looks like real semver-ish
// version go-selfupdate's LessOrEqual can parse. The runtime debug
// fallbacks "(devel)" and "(unknown)" must not reach the parser.
func isComparableVersion(v string) bool {
	if v == "" || v == "(devel)" || v == "(unknown)" {
		return false
	}
	if v[0] == '(' {
		return false
	}
	return true
}

// readBinaryVersion pulls the build version from runtime/debug. When
// the binary was built without -ldflags='-X version.Version=…' (e.g.
// `go build` from source), debug.ReadBuildInfo's Main.Version reports
// "(devel)"; we surface that verbatim so users see they're on a
// dev build.
func readBinaryVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	v := info.Main.Version
	if v == "" {
		return "(devel)"
	}
	return v
}
