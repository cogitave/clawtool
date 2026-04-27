//go:build !windows

package worktree

import "syscall"

// syscallZero returns the unix "is the process alive?" probe signal.
// The kernel never delivers signal 0; sending it is a permission +
// existence check. On Windows os.FindProcess + Signal has no exact
// equivalent — see syscall_windows.go.
func syscallZero() syscall.Signal { return syscall.Signal(0) }
