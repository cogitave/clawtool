//go:build windows

package worktree

import "os"

// syscallZero on windows: there's no portable "ping a PID" signal.
// Returning os.Interrupt is a placeholder; processAlive on windows
// will always report false (correct for our v0.14 scope: GC there
// will simply not reap, which is conservative).
func syscallZero() os.Signal { return os.Interrupt }
