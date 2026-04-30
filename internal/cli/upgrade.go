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
	//
	// Three-way branch on the current vs latest comparison:
	//   * Equal           — operator IS on the latest tag.
	//   * Ahead (current  > latest) — local build (typically a
	//                                 goreleaser-snapshot or
	//                                 branch build like
	//                                 `0.22.58-tui-responsive`)
	//                                 is newer than the latest
	//                                 published tag. Worth
	//                                 surfacing distinctly so the
	//                                 operator doesn't read
	//                                 "current → current" as
	//                                 "no upgrade available".
	//   * Behind          — fall through to the normal upgrade
	//                       flow further down.
	latestVersion := latest.Version()
	if isComparableVersion(currentVersion) && latest.LessOrEqual(currentVersion) {
		renderUpToDate(ux, currentVersion, latestVersion)
		return 0
	}

	ux.HeaderDelta(currentVersion, latestVersion)
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
	// new binary on the same port + token. Pass `exe` (the install
	// path the new binary just landed at) so the daemon spawn
	// resolves to the post-swap inode — the upgrade CLI process
	// itself is running from `.clawtool.old` (Linux's atomic-rename
	// backup), and `os.Executable()` would resolve to that
	// transient path which the post-swap cleanup may have already
	// unlinked.
	if rc := restartDaemonIfRunning(a, ux, exe); rc != 0 {
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
//
// `exePath` is the install path the upgrade just wrote the new
// binary to; passed through to daemon.EnsureFrom so the new
// daemon spawns from that inode rather than the upgrading CLI's
// own (now-renamed-to-`.clawtool.old`) executable.
func restartDaemonIfRunning(a *App, ux *upgradeUX, exePath string) int {
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
	fresh, err := daemon.EnsureFrom(ctx, exePath)
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

// renderUpToDate handles the "no upgrade needed" rendering. Two
// distinct sub-cases collapse here:
//
//   - Equal: operator IS on the latest tagged release. The "→"
//     arrow lands on the same version on both sides; the note
//     pins that as the published state.
//   - Ahead: operator's local build is *newer* than the latest
//     tagged release — typically a goreleaser-snapshot or
//     branch build like `0.22.58-tui-responsive` that hasn't
//     been published yet. The header arrow shows latest →
//     current so the *direction* of the gap is unambiguous, and
//     the note explains that this is a dev/branch build and no
//     upgrade is necessary. Without this branch the operator
//     used to see "current → current" + "already on the latest
//     tagged release" which read as "no upgrade available" but
//     actually hid the (different, fine) state of being ahead.
//
// Pure rendering: no I/O beyond `ux`. Tested directly with a
// bytes.Buffer-backed upgradeUX in upgrade_ux_test.go.
func renderUpToDate(ux *upgradeUX, current, latest string) {
	if current == latest {
		ux.HeaderDelta(current, current)
		ux.Note(fmt.Sprintf("already on the latest tagged release (%s)", current))
	} else {
		ux.HeaderDelta(latest, current)
		ux.Note(fmt.Sprintf("your local build (%s) is ahead of the latest tagged release (%s) — dev/branch build, no upgrade necessary", current, latest))
	}
	ux.NextSteps([]string{
		"clawtool overview     see the live state of the daemon and any active dispatches",
		"clawtool changelog    full release history",
	})
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
