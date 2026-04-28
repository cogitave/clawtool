//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on f. Blocks until granted.
// Released by the caller's deferred unlockFile + Close.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// unlockFile drops the flock. Idempotent; close also releases.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
