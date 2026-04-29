package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/cogitave/clawtool/internal/version"
	"github.com/creativeprojects/go-selfupdate"
)

const upgradeUsage = `Usage:
  clawtool upgrade               Pull the latest cogitave/clawtool release,
                                 atomically replace the running binary, AND
                                 restart the daemon onto the new binary.
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

	ux := newUpgradeUX(a.Stdout)

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
		ux.HeaderDelta(currentVersion, currentVersion)
		ux.Note(fmt.Sprintf("already on the latest tagged release (%s)", currentVersion))
		ux.NextSteps([]string{
			"clawtool overview     see the live state of the daemon and any active dispatches",
			"clawtool changelog    full release history",
		})
		return 0
	}

	ux.HeaderDelta(currentVersion, latest.Version())
	if checkOnly {
		ux.Note("--check passed: skipping the actual install")
		ux.NextSteps([]string{
			"clawtool upgrade      install the new release and restart the daemon",
		})
		return 0
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool upgrade: locate self: %v\n", err)
		return 1
	}

	ux.PhaseStart(fmt.Sprintf("Downloading and replacing %s", exe))
	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		// Common case: clawtool sits in /usr/local/bin without write
		// access. Surface a clear hint instead of the raw permission
		// error so the user knows to re-run with sudo (or their own
		// elevation tool).
		if errors.Is(err, os.ErrPermission) {
			ux.PhaseFail(
				fmt.Sprintf("permission denied writing %s", exe),
				"re-run as the binary's owner (sudo) or move the install to ~/.local/bin",
			)
			return 1
		}
		ux.PhaseFail(err.Error(), "")
		return 1
	}
	detail := ""
	if latest.AssetByteSize > 0 {
		detail = humanBytes(int64(latest.AssetByteSize))
	}
	if latest.AssetName != "" && detail != "" {
		detail = fmt.Sprintf("%s · %s", latest.AssetName, detail)
	} else if latest.AssetName != "" {
		detail = latest.AssetName
	}
	ux.PhaseDone(detail)

	// Auto-restart the daemon if one is running. Without this step
	// `clawtool upgrade` swaps the binary on disk but the running
	// daemon stays on the old code in memory — the operator has to
	// pkill+relaunch by hand, and a forgotten restart silently
	// invalidates every "fixed in the new release" claim. Stop()
	// SIGTERMs the old PID; Ensure() spawns a fresh one with the
	// new binary on the same port + token.
	if rc := restartDaemonIfRunning(a, ux); rc != 0 {
		return rc
	}

	// Closing flourish: release notes + next-step prompts. Both
	// are best-effort — a release without notes simply skips the
	// section, and the next-steps list is a static recommendation
	// that always renders. Together they position the upgrade
	// output as one waypoint in a longer flow rather than a
	// dead-end success line.
	ux.ReleaseNotes(latest.ReleaseNotes, 8)
	ux.NextSteps([]string{
		"clawtool overview     verify the live state and check that watch sockets reconnected",
		"clawtool changelog    full release notes",
		fmt.Sprintf("Release page:        %s", latest.URL),
	})
	return 0
}

// restartDaemonIfRunning is the post-upgrade step that swaps the
// running daemon onto the new binary. Idempotent: no-ops when no
// daemon is recorded. On Stop or Ensure failure it surfaces a
// clear hint via the upgrade UX and returns non-zero so the
// installer surface (install.sh / CI) can detect the partial state.
func restartDaemonIfRunning(a *App, ux *upgradeUX) int {
	state, err := daemon.ReadState()
	if err != nil {
		ux.Section("Daemon restart")
		ux.PhaseStart("Reading existing daemon state")
		ux.PhaseFail(err.Error(), "binary upgraded; run `clawtool serve` manually to start a fresh daemon")
		return 1
	}
	if state == nil || !daemon.IsRunning(state) {
		// Nothing to do — common case for fresh installs or when
		// the operator runs upgrade before ever launching a daemon.
		ux.Section("Daemon restart")
		ux.Note("no daemon was running — nothing to restart")
		return 0
	}

	ux.Section("Daemon restart")
	uptime := ""
	if !state.StartedAt.IsZero() {
		uptime = fmt.Sprintf("served %s", time.Since(state.StartedAt).Round(time.Second))
	}
	stopDetail := fmt.Sprintf("pid %d", state.PID)
	if uptime != "" {
		stopDetail = fmt.Sprintf("%s · %s", stopDetail, uptime)
	}
	ux.PhaseStart("Stopping running daemon")
	if err := daemon.Stop(); err != nil {
		ux.PhaseFail(err.Error(), "binary upgraded; run `clawtool serve` manually to start a fresh daemon")
		return 1
	}
	ux.PhaseDone(stopDetail)

	ux.PhaseStart("Spawning new daemon onto the upgraded binary")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fresh, err := daemon.Ensure(ctx)
	if err != nil {
		ux.PhaseFail(err.Error(), "run `clawtool serve` manually to start a fresh daemon")
		return 1
	}
	ux.PhaseDone(fmt.Sprintf("pid %d · %s", fresh.PID, fresh.URL()))
	return 0
}

// humanBytes renders a byte count as a 2-decimal MB or KB string.
// We keep this local to upgrade.go; the only caller is the asset-
// size detail line in the download phase.
func humanBytes(n int64) string {
	const (
		_ int64 = 1 << (10 * iota)
		kb
		mb
	)
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
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


