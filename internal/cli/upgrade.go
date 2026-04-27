package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/creativeprojects/go-selfupdate"
)

const upgradeUsage = `Usage:
  clawtool upgrade               Pull the latest cogitave/clawtool release
                                 and atomically replace the running binary.
  clawtool upgrade --check       Report the latest version without installing.

The release source is github.com/cogitave/clawtool — same artefacts
GoReleaser publishes on tag. Per-OS / per-arch tarballs auto-resolved.

Per ADR-007 we wrap creativeprojects/go-selfupdate (Apache-2.0); we
do not implement the GitHub API client or the atomic-replace logic
ourselves.
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

	currentVersion := readBinaryVersion()
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

	if currentVersion != "" && latest.LessOrEqual(currentVersion) {
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
	return 0
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
