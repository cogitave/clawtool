//go:build !unix

package sandbox

import "os/exec"

// applyProcessGroup is a no-op on non-unix platforms. Default
// cmd.Cancel handler (SIGKILL on cancel) is good enough on Windows.
func applyProcessGroup(cmd *exec.Cmd) {}
