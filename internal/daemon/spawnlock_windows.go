//go:build windows

package daemon

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive LockFileEx on f. Blocks until granted.
func lockFile(f *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0, 1, 0,
		overlapped,
	)
}

// unlockFile releases the LockFileEx range. Close also releases.
func unlockFile(f *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0, 1, 0,
		overlapped,
	)
}
