package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// spawnLockPath returns the sibling .lock file Ensure brackets its
// read-decide-spawn-write sequence with. Lives next to the state file
// so XDG / per-user isolation already applies.
func spawnLockPath() string {
	return filepath.Join(configDir(), "daemon.lock")
}

// acquireSpawnLock takes an OS-level advisory lock on the spawn-lock
// file. The returned func releases the lock + closes the underlying
// FD; callers must defer it. Blocks until the lock is granted (no
// nonblocking try — Ensure is idempotent and the wait window is
// bounded by another process's spawn duration ~1-2 s in the worst
// case).
//
// Implementation lives in spawnlock_unix.go / spawnlock_windows.go;
// this file owns the file-creation + fd ownership so the per-OS
// helpers stay tiny.
func acquireSpawnLock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(spawnLockPath()), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir lock dir: %w", err)
	}
	f, err := os.OpenFile(spawnLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", spawnLockPath(), err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", spawnLockPath(), err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}
