package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDiscoverPathShadows_FindsDistinctCopies plants two clawtool
// binaries in two PATH dirs and asserts the discovery returns the
// non-primary one in PATH order.
func TestDiscoverPathShadows_FindsDistinctCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics here are POSIX-shaped; Windows test deferred")
	}
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("mkdir a: %v", err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatalf("mkdir b: %v", err)
	}
	primary := filepath.Join(dirA, "clawtool")
	shadow := filepath.Join(dirB, "clawtool")
	if err := os.WriteFile(primary, []byte("primary"), 0o755); err != nil {
		t.Fatalf("write primary: %v", err)
	}
	if err := os.WriteFile(shadow, []byte("shadow"), 0o755); err != nil {
		t.Fatalf("write shadow: %v", err)
	}

	t.Setenv("PATH", dirA+string(os.PathListSeparator)+dirB)
	got := discoverPathShadows(primary)
	if len(got) != 1 {
		t.Fatalf("got %d shadows, want 1: %v", len(got), got)
	}
	if got[0] != shadow {
		t.Errorf("got %q, want %q", got[0], shadow)
	}
}

// TestDiscoverPathShadows_DedupesSymlinks ensures two PATH entries
// pointing at the same physical inode don't cause the same shadow
// to be reported twice.
func TestDiscoverPathShadows_DedupesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics are POSIX-shaped; Windows test deferred")
	}
	root := t.TempDir()
	dirReal := filepath.Join(root, "real")
	dirLink := filepath.Join(root, "link")
	if err := os.MkdirAll(dirReal, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	primary := filepath.Join(t.TempDir(), "clawtool")
	if err := os.WriteFile(primary, []byte("primary"), 0o755); err != nil {
		t.Fatalf("write primary: %v", err)
	}
	shadowReal := filepath.Join(dirReal, "clawtool")
	if err := os.WriteFile(shadowReal, []byte("shadow"), 0o755); err != nil {
		t.Fatalf("write shadowReal: %v", err)
	}
	if err := os.Symlink(dirReal, dirLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	t.Setenv("PATH", dirReal+string(os.PathListSeparator)+dirLink)
	got := discoverPathShadows(primary)
	if len(got) != 1 {
		t.Fatalf("got %d shadows, want 1 (symlink-deduped): %v", len(got), got)
	}
}

// TestAtomicCopyFileBytes_ReplacesContent confirms the helper
// replaces an existing file's bytes atomically.
func TestAtomicCopyFileBytes_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "binary")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := atomicCopyFileBytes(dst, []byte("new bytes"), 0o755); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new bytes" {
		t.Errorf("got %q, want %q", string(got), "new bytes")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0o755", info.Mode().Perm())
	}
}

// TestSyncPathShadowsTo_NoOpWhenNoShadows confirms the public
// entry point is silent when only the primary exists on PATH.
func TestSyncPathShadowsTo_NoOpWhenNoShadows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-shaped PATH test")
	}
	dir := t.TempDir()
	primary := filepath.Join(dir, "clawtool")
	if err := os.WriteFile(primary, []byte("primary"), 0o755); err != nil {
		t.Fatalf("write primary: %v", err)
	}
	t.Setenv("PATH", dir)

	var buf strings.Builder
	ux := newUpgradeUX(&buf)
	syncPathShadowsTo(ux, primary)
	if strings.Contains(buf.String(), "PATH-shadow sync") {
		t.Errorf("UX rendered shadow-sync header on no-shadow path: %q", buf.String())
	}
}
