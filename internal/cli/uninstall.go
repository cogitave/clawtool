// Package cli — `clawtool uninstall` removes every artifact
// clawtool drops on the host. Designed for the tester / dogfooder
// who installs the binary fresh ten times a day and ends up with
// duplicate sources / portals / sticky defaults.
//
// The cleanup is intentionally exhaustive — config + secrets +
// caches + data dirs + sticky pointers + worktrees + BIAM SQLite
// + telemetry id. The binary itself is opt-in (--purge-binary)
// because the user may have installed via Homebrew / curl / Go
// and the right removal command differs by source.
//
// Per ADR-007 doesn't apply here: this is "rm -rf clawtool's own
// files", which is by definition not delegable to an upstream.
// We still rely on stdlib os.RemoveAll for the actual removal.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/xdg"
)

const uninstallUsage = `Usage:
  clawtool uninstall [--yes] [--dry-run] [--purge-binary] [--keep-config]

Removes every artifact clawtool drops on the host:
  - ~/.config/clawtool/        — config, secrets, identity, sticky pointers
  - $XDG_CACHE_HOME/clawtool/  — worktrees, semantic-search index, update cache
  - $XDG_DATA_HOME/clawtool/   — BIAM SQLite, telemetry id

Flags:
  --yes            Skip the confirmation prompt.
  --dry-run        Print what would be removed without touching disk.
  --purge-binary   Also delete the binary at $INSTALL_DIR/clawtool
                   (Makefile installs this to ~/.local/bin/clawtool).
  --keep-config    Preserve config.toml + secrets.toml + identity.ed25519.
                   Drops only caches / data / sticky pointers / BIAM.
`

type uninstallArgs struct {
	yes         bool
	dryRun      bool
	purgeBinary bool
	keepConfig  bool
}

func parseUninstallArgs(argv []string) (uninstallArgs, error) {
	out := uninstallArgs{}
	for _, v := range argv {
		switch v {
		case "--yes", "-y":
			out.yes = true
		case "--dry-run", "-n":
			out.dryRun = true
		case "--purge-binary":
			out.purgeBinary = true
		case "--keep-config":
			out.keepConfig = true
		case "--help", "-h":
			return out, errors.New("help requested")
		default:
			return out, fmt.Errorf("unknown flag %q", v)
		}
	}
	return out, nil
}

// runUninstall is the dispatcher hooked into Run().
func (a *App) runUninstall(argv []string) int {
	args, err := parseUninstallArgs(argv)
	if err != nil {
		if err.Error() == "help requested" {
			fmt.Fprint(a.Stdout, uninstallUsage)
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool uninstall: %v\n\n%s", err, uninstallUsage)
		return 2
	}
	if err := a.Uninstall(args); err != nil {
		fmt.Fprintf(a.Stderr, "clawtool uninstall: %v\n", err)
		return 1
	}
	return 0
}

// Uninstall performs the cleanup. Public so the MCP tool surface
// + integration tests can call it without going through argv.
func (a *App) Uninstall(args uninstallArgs) error {
	targets := planUninstallTargets(args)
	if len(targets) == 0 {
		fmt.Fprintln(a.Stdout, "(nothing to remove — clawtool is already uninstalled)")
		return nil
	}

	verb := "Will remove"
	if args.dryRun {
		verb = "[dry-run] would remove"
	}
	fmt.Fprintf(a.Stdout, "%s:\n", verb)
	for _, t := range targets {
		fmt.Fprintf(a.Stdout, "  %s   %s\n", t.kind, t.path)
	}
	fmt.Fprintln(a.Stdout, "")

	if args.dryRun {
		return nil
	}
	if !args.yes {
		if !confirmUninstall(a) {
			return errors.New("aborted by operator")
		}
	}

	removed := 0
	for _, t := range targets {
		if err := os.RemoveAll(t.path); err != nil {
			fmt.Fprintf(a.Stderr, "  ✗ %s: %v\n", t.path, err)
			continue
		}
		removed++
	}
	fmt.Fprintf(a.Stdout, "✓ removed %d artifact(s)\n", removed)
	if !args.purgeBinary {
		fmt.Fprintln(a.Stdout, "  (binary left in place — re-run with --purge-binary to remove it too)")
	}
	return nil
}

type uninstallTarget struct {
	kind string // "config" | "secrets" | "cache" | "data" | "binary" | "sticky" | "biam"
	path string
}

// planUninstallTargets enumerates every existing artifact that
// matches the requested removal scope. Non-existent files are
// dropped from the plan so the rendered list reflects reality.
func planUninstallTargets(args uninstallArgs) []uninstallTarget {
	var out []uninstallTarget
	add := func(kind, path string) {
		if path == "" {
			return
		}
		if _, err := os.Stat(path); err == nil {
			out = append(out, uninstallTarget{kind: kind, path: path})
		}
	}

	cfgDir := xdg.ConfigDirIfHome()
	cacheDir := xdg.CacheDirIfHome()
	dataDir := xdg.DataDirIfHome()

	if args.keepConfig {
		// Surgical removal: pointers, hooks state, telemetry id —
		// but leave config.toml + secrets.toml + identity.ed25519.
		for _, name := range []string{
			"active_agent", "active_portal", "listener-token",
		} {
			add("sticky", filepath.Join(cfgDir, name))
		}
	} else {
		// Full sweep: everything under ~/.config/clawtool.
		add("config", cfgDir)
	}

	// Caches always go (worktrees, semantic-search index, update cache).
	add("cache", cacheDir)
	// BIAM + telemetry id always go (re-created on next run).
	add("data", dataDir)

	if args.purgeBinary {
		add("binary", binaryInstallPath())
	}
	return out
}

// binaryInstallPath honours the Makefile's INSTALL_DIR convention
// (defaults to ~/.local/bin/clawtool). Operators who installed
// via Homebrew or curl-to-/usr/local/bin should remove manually
// — we don't presume to know which package manager owns the
// binary in those cases.
func binaryInstallPath() string {
	if v := strings.TrimSpace(os.Getenv("CLAWTOOL_INSTALL_DIR")); v != "" {
		return filepath.Join(v, "clawtool")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin", "clawtool")
}

// confirmUninstall prompts on stdin. Returns true on y/yes;
// anything else cancels.
func confirmUninstall(a *App) bool {
	fmt.Fprint(a.Stdout, "Proceed? [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}
