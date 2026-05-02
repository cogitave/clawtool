// Package cli — `clawtool upgrade` PATH-shadow sync.
//
// When the operator has multiple clawtool binaries on $PATH (the
// canonical case is `~/go/bin/clawtool` from `go install` AND
// `~/.local/bin/clawtool` from `install.sh`), `os.Executable()` only
// resolves to the one inode we were spawned from. The unupgraded
// twin keeps shadowing $PATH for any consumer that doesn't ask the
// caller's choice — most importantly Claude Code's MCP plugin,
// which spawns clawtool via `mcp add` config that hard-coded a
// specific path at install time. The result is a silent version
// skew: the operator runs `clawtool upgrade`, sees the new
// version on the terminal, but every MCP call still routes
// through the stale binary.
//
// syncPathShadowsTo walks $PATH, finds every clawtool that resolves
// to a different inode than the just-upgraded one, and copies the
// new binary's bytes over each. Best-effort per file: a permission
// error on one shadow doesn't abort the others — the operator
// hears about every one that succeeded and every one that failed,
// and the upgrade as a whole still returns 0 because the canonical
// (`os.Executable()`) path was already updated.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// syncPathShadowsTo discovers every clawtool on $PATH that's a
// different file than `primary` and rewrites it with primary's
// bytes (atomic temp+rename per shadow). Renders progress through
// the upgradeUX so the operator sees exactly which paths got
// updated and which were skipped.
func syncPathShadowsTo(ux *upgradeUX, primary string) {
	primaryAbs, err := filepath.Abs(primary)
	if err != nil {
		return
	}
	primaryReal, err := filepath.EvalSymlinks(primaryAbs)
	if err != nil {
		// Resolved via abs but not symlink-able (e.g. broken
		// symlink chain). Fall back to abs — the equality check
		// below tolerates either.
		primaryReal = primaryAbs
	}

	shadows := discoverPathShadows(primaryReal)
	if len(shadows) == 0 {
		return
	}

	ux.Section(fmt.Sprintf("PATH-shadow sync (%d additional %s found)", len(shadows), pluralize("copy", len(shadows))))

	primaryBytes, err := os.ReadFile(primary)
	if err != nil {
		ux.PhaseStart("Reading just-upgraded binary for mirror")
		ux.PhaseFail(err.Error(), "skipping shadow sync; the canonical path is up-to-date")
		return
	}

	for _, shadow := range shadows {
		ux.PhaseStart(fmt.Sprintf("Mirroring → %s", shadow))
		if err := atomicCopyFileBytes(shadow, primaryBytes, 0o755); err != nil {
			hint := ""
			if os.IsPermission(err) {
				hint = "re-run as the binary's owner (sudo) or delete the stale shadow manually"
			}
			ux.PhaseFail(err.Error(), hint)
			continue
		}
		ux.PhaseDone("synced")
	}
}

// discoverPathShadows walks $PATH and returns absolute paths of
// every `clawtool` (or `clawtool.exe` on Windows) that resolves to
// a different inode than `primaryReal`. Order preserves $PATH
// order so the user sees the same precedence the shell would.
func discoverPathShadows(primaryReal string) []string {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return nil
	}
	binName := "clawtool"
	if runtime.GOOS == "windows" {
		binName = "clawtool.exe"
	}

	seen := make(map[string]struct{})
	seen[primaryReal] = struct{}{}
	var shadows []string
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, binName)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		// Resolve symlinks so two PATH entries pointing to the
		// same physical file don't get processed twice.
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			real = candidate
		}
		if _, dup := seen[real]; dup {
			continue
		}
		seen[real] = struct{}{}
		shadows = append(shadows, candidate)
	}
	return shadows
}

// atomicCopyFileBytes writes `bytes` to `dst` via temp + rename in
// the destination's parent directory. The temp prefix matches the
// dst name so a partial crash leaves an obviously-related leftover
// instead of an orphan tempfile in $TMPDIR (which would also fail
// the rename across filesystem boundaries).
func atomicCopyFileBytes(dst string, bytes []byte, mode os.FileMode) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".upgrade.")
	if err != nil {
		return fmt.Errorf("create tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}
	if _, err := io.Copy(tmp, strings.NewReader(string(bytes))); err != nil {
		cleanup()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename tempfile to %s: %w", dst, err)
	}
	return nil
}

// pluralize is the trivial English plural helper for the
// PATH-shadow Section header. Singular when n==1, plural otherwise.
func pluralize(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "ies" // "copy" → "copies"; only used for "copy" today.
}
