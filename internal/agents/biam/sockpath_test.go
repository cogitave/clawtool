package biam

import (
	"os"
	"path/filepath"
	"testing"
)

// shortSockDir returns a tempdir whose path stays well under the
// 104-byte sun_path limit darwin enforces on Unix domain sockets.
// `t.TempDir()` lands under macOS's $TMPDIR (`/var/folders/.../T/...`)
// which already eats ~70 bytes before the test name + suffix push
// the full sock path past the limit (`bind: invalid argument` from
// the kernel). Linux's 108-byte limit + shorter `/tmp` prefix means
// this never bites in CI on linux, but the macOS runner does.
//
// Pattern: drop the directory directly under `/tmp` (a symlink to
// `/private/tmp` on darwin) with a tiny prefix, register cleanup,
// hand back the path. Callers append "<name>.sock" and stay safe.
func shortSockDir(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if _, err := os.Stat("/tmp"); err == nil {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "ct-")
	if err != nil {
		t.Fatalf("shortSockDir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// shortSockPath joins shortSockDir + name and asserts the result
// fits under the macOS 104-byte limit so the test fails loudly if
// the helper ever drifts past it on a future runner with a longer
// $TMPDIR.
func shortSockPath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(shortSockDir(t), name)
	if len(p) > 100 {
		t.Fatalf("socket path too long for darwin (%d bytes): %s", len(p), p)
	}
	return p
}
