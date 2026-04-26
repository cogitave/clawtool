//go:build !unix

package core

import "os/exec"

// applyProcessGroup is a no-op on non-unix platforms. Default cmd.Cancel
// (SIGKILL to the single process) is the best we can do without per-OS
// job-object plumbing.
func applyProcessGroup(cmd *exec.Cmd) {}
