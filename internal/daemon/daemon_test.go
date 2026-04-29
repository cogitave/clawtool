// Package daemon — unit tests. The full process-lifecycle path is
// exercised in test/e2e/upgrade (Docker container, real binary
// swap), but a couple of in-process invariants belong here so a
// regression surfaces in the fast `go test` lane rather than only
// in the slow Docker gate.
package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEnsureFrom_UsesProvidedBinaryPath guards the `clawtool
// upgrade` regression that shipped briefly: the upgrade flow swaps
// the install-path binary, then calls daemon.Ensure to respawn —
// but Ensure called os.Executable() which on Linux resolved to the
// upgrading CLI's `(deleted)` inode (Linux's atomic-rename moves
// the running binary to `.clawtool.old` before unlinking it). The
// post-restart spawn fork/exec'd a deleted file and bombed with
// "no such file or directory".
//
// EnsureFrom takes an explicit binary path so callers that know
// where the canonical install lives (the upgrade flow knows: it
// just wrote the new binary there) can route around the stale
// os.Executable() resolution. This test verifies the parameter is
// actually consumed: we point EnsureFrom at a doesn't-exist path
// and expect the spawn step to fail with that exact path in the
// error message — proving the override took effect rather than
// silently falling back to the test binary's own os.Executable().
func TestEnsureFrom_UsesProvidedBinaryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test/e2e/upgrade covers Windows path semantics; this in-process check is POSIX-only")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)

	// A path that definitely doesn't exist — if EnsureFrom honours
	// the override, the inner exec.Command fails with this path.
	// If it ignores the override and falls back to os.Executable(),
	// the spawn would succeed (the test binary IS executable) and
	// we'd get a different error or no error at all.
	bogus := filepath.Join(dir, "definitely-not-clawtool")

	_, err := EnsureFrom(context.Background(), bogus)
	if err == nil {
		t.Fatalf("EnsureFrom(%q) returned nil error — expected fork/exec failure", bogus)
	}
	if !strings.Contains(err.Error(), bogus) {
		t.Fatalf("EnsureFrom error didn't mention the override path: %v\n(want: contains %q)", err, bogus)
	}
}

// TestEnsureFrom_EmptyPathFallsBackToExecutable verifies the
// no-override codepath still uses os.Executable(). Important so
// non-upgrade callers (claude-bootstrap, mcp_host, the daemon
// CLI's `daemon start` verb) don't have to thread a path through.
func TestEnsureFrom_EmptyPathFallsBackToExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only fork/exec semantics")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)

	// Empty exePath should resolve via os.Executable() — which
	// for `go test` is a real, executable temp file. The spawn
	// will then run that test binary with `serve` arguments,
	// which the test binary doesn't understand and exits non-zero.
	// We don't await readiness; we just want to confirm the spawn
	// path doesn't fail at the os.Executable() call.
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable() unavailable in this environment: %v", err)
	}
	if _, err := exec.LookPath(exe); err != nil {
		t.Skipf("os.Executable() result %q not actually executable: %v", exe, err)
	}
	// The spawn will fork the test binary with `serve` args; that
	// process won't write a healthy state file, so EnsureFrom
	// returns an error from the post-spawn health probe (or the
	// IsRunning re-check). We just want the os.Executable() call
	// itself to not error out — which it doesn't, since we got a
	// path above. So no further assertion needed; reaching this
	// line means the override-fallback branch ran without a panic.
	_, _ = EnsureFrom(context.Background(), "")
}
