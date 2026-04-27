//go:build windows

package daemon

import "os/exec"

// detachCmd is a no-op on Windows; the parent doesn't own a session
// to detach from in the POSIX sense.
func detachCmd(_ *exec.Cmd) {}
